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

func (o *OnePassword) bin() string {
	if o.OpPath != "" {
		return o.OpPath
	}
	return "op"
}
