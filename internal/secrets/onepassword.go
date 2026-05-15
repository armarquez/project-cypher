package secrets

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const opScheme = "op://"

// OnePassword resolves op:// secret references using the 1Password CLI (`op`).
// Set OpPath to override the binary location; defaults to "op" (or "op.exe" on
// WSL2, which integrates with the 1Password desktop app on the Windows host).
type OnePassword struct {
	OpPath string
}

// Handles reports whether value is an op:// URI.
func (o *OnePassword) Handles(value string) bool {
	return strings.HasPrefix(value, opScheme)
}

// Resolve runs `op read --no-newline <value>` and returns the secret plaintext.
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
