package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
//
// Field assignment syntax is used instead of --template to avoid the stdin
// conflict: op CLI 2.x treats any non-TTY stdin as piped JSON, which conflicts
// with --template in scripted/test contexts where stdin is a pipe, not a TTY.
func (o *OnePassword) Store(ctx context.Context, vault, title, label, value string) (string, error) {
	// Delete any pre-existing item to avoid accumulating duplicates.
	o.deleteExisting(ctx, vault, title)

	// Pass the value as a field assignment arg. Go exec passes this as a single
	// OS argument so newlines (e.g. in PEM keys) are handled correctly without
	// shell escaping.
	out, err := exec.CommandContext(ctx, o.bin(), "item", "create",
		"--vault", vault,
		"--category", "API Credential",
		"--title", title,
		label+"[concealed]="+value,
	).CombinedOutput()
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
