package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const opScheme = "op://"

// OnePassword resolves and stores secrets using the 1Password CLI (`op`).
// Set OpPath to override the binary location; defaults to "op" (or "op.exe"
// on WSL2, which integrates with the 1Password desktop app on the Windows host).
// OnePassword implements both Resolver (for Chain-based resolution) and
// Vault (for setup-time store/retrieve operations).
type OnePassword struct {
	OpPath string
}

// Handles reports whether value is an op:// URI.
func (o *OnePassword) Handles(value string) bool {
	return strings.HasPrefix(value, opScheme)
}

// Resolve runs `op read --no-newline <value>` and returns the secret plaintext.
// Implements the Resolver interface for use in Chain.
func (o *OnePassword) Resolve(ctx context.Context, value string) (string, error) {
	out, err := exec.CommandContext(ctx, o.bin(), "read", "--no-newline", value).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("op read %q: %s", value, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("op read %q: %w", value, err)
	}
	return string(out), nil
}

// Get returns the plaintext value for ref. Implements the Vault interface.
func (o *OnePassword) Get(ctx context.Context, ref string) (string, error) {
	return o.Resolve(ctx, ref)
}

// Available reports whether the `op` (or `op.exe`) binary is findable.
func (o *OnePassword) Available() bool {
	_, err := exec.LookPath(o.bin())
	return err == nil
}

// Preflight checks that the op CLI is installed and has at least one account
// configured. It uses `op account list` rather than `op whoami` because the
// former works with the 1Password desktop app integration (no explicit
// `op signin` required when the desktop app is running).
func (o *OnePassword) Preflight(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, o.bin(), "account", "list").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("1Password CLI not ready: %s\n  → ensure the 1Password desktop app is running, or run `op signin`", msg)
	}
	if strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("1Password CLI not ready: no accounts found\n  → run `op signin` to add an account")
	}
	return nil
}

// Store creates (or replaces) a 1Password item with a single concealed field
// and returns the op:// reference for later retrieval via Get.
// Any existing item with the same title is deleted first to avoid duplicates
// (avoids --upsert which is absent in some op CLI versions).
func (o *OnePassword) Store(ctx context.Context, vault, title, label, value string) (string, error) {
	type opField struct {
		Label string `json:"label"`
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	type opTemplate struct {
		Title    string            `json:"title"`
		Vault    map[string]string `json:"vault"`
		Category string            `json:"category"`
		Fields   []opField         `json:"fields"`
	}

	tmpl := opTemplate{
		Title:    title,
		Vault:    map[string]string{"name": vault},
		Category: "API_CREDENTIAL",
		Fields:   []opField{{Label: label, Type: "CONCEALED", Value: value}},
	}
	templateJSON, err := json.Marshal(tmpl)
	if err != nil {
		return "", fmt.Errorf("marshal template: %w", err)
	}

	// Write template to a temp file: older op versions don't support --template - (stdin).
	tmpFile, err := os.CreateTemp("", "cypher-op-*.json")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(templateJSON); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("write template: %w", err)
	}
	tmpFile.Close()

	// Delete any pre-existing item to avoid accumulating duplicates.
	o.deleteExisting(ctx, vault, title)

	// op.exe is a Windows binary and cannot open a Linux path like /tmp/...
	// Convert to a Windows UNC path via wslpath so it can access the file.
	templateArg := tmpPath
	if strings.HasSuffix(o.bin(), ".exe") {
		if winPath, err := wslToWindowsPath(ctx, tmpPath); err == nil {
			templateArg = winPath
		}
	}

	// Set Stdin explicitly to an empty reader. On WSL2, the interop layer passes
	// the Windows console handle to .exe processes regardless of Go's nil-stdin
	// convention, causing `op item create` to see both --template and a live stdin
	// and reject the call with "cannot create an item from template and stdin at
	// the same time".
	cmd := exec.CommandContext(ctx, o.bin(), "item", "create",
		"--template", templateArg,
		"--vault", vault,
	)
	cmd.Stdin = bytes.NewReader(nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("op item create: %s", msg)
	}

	return fmt.Sprintf("op://%s/%s/%s", vault, title, label), nil
}

// deleteExisting looks up an item by title and deletes it if found.
// Errors are silently ignored — the item simply may not exist yet.
func (o *OnePassword) deleteExisting(ctx context.Context, vault, title string) {
	out, err := exec.CommandContext(ctx, o.bin(), "item", "get", title,
		"--vault", vault, "--format", "json").Output()
	if err != nil {
		return
	}
	var item struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &item) != nil || item.ID == "" {
		return
	}
	exec.CommandContext(ctx, o.bin(), "item", "delete", item.ID, "--vault", vault).Run() //nolint:errcheck
}

// wslToWindowsPath converts a Linux path to a Windows UNC path using wslpath,
// so that Windows binaries (e.g. op.exe) can access files on the WSL2 filesystem.
func wslToWindowsPath(ctx context.Context, linuxPath string) (string, error) {
	out, err := exec.CommandContext(ctx, "wslpath", "-w", linuxPath).Output()
	if err != nil {
		return "", fmt.Errorf("wslpath: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// bin returns the op binary to use. When OpPath is set it is used directly.
// On Linux (including WSL2) with no explicit override, op.exe is preferred
// over op because it integrates with the 1Password desktop app on the Windows
// host without requiring a separate `op signin`.
func (o *OnePassword) bin() string {
	if o.OpPath != "" {
		return o.OpPath
	}
	if runtime.GOOS == "linux" {
		if p, err := exec.LookPath("op.exe"); err == nil {
			return p
		}
	}
	return "op"
}
