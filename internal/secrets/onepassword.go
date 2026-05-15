package secrets

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const opScheme = "op://"

// OnePassword resolves op:// secret references using the 1Password CLI (`op`).
// Set OpPath to override the binary location; defaults to "op" from PATH.
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

// Available reports whether the `op` binary is installed and findable in PATH.
func (o *OnePassword) Available() bool {
	_, err := exec.LookPath(o.bin())
	return err == nil
}

// Preflight checks that the op CLI is installed and the user is signed in.
// Call this before any operation that stores or reads 1Password secrets so
// auth errors surface immediately rather than after expensive earlier steps.
func (o *OnePassword) Preflight(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, o.bin(), "whoami").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("1Password CLI not ready: %s\n  → run `op signin` and try again", msg)
	}
	return nil
}

func (o *OnePassword) bin() string {
	if o.OpPath != "" {
		return o.OpPath
	}
	return "op"
}
