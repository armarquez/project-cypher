package secrets

import "context"

// Vault stores and retrieves secrets. Implementations may be backed by
// the 1Password CLI (OnePassword), an in-memory map (FakeVault, for tests),
// or any future provider. The interface is intentionally provider-agnostic;
// the op:// URI scheme is an implementation detail of OnePassword, not the
// interface contract.
type Vault interface {
	// Preflight verifies the vault backend is reachable and authenticated.
	// It must be called before Store or Get to surface auth failures early.
	Preflight(ctx context.Context) error

	// Store saves value as a concealed field named label in an item named
	// title within vault. It returns a provider-specific reference (e.g.
	// op://vault/title/label) suitable for passing back to Get.
	Store(ctx context.Context, vault, title, label, value string) (ref string, err error)

	// Get returns the plaintext value for ref.
	Get(ctx context.Context, ref string) (string, error)

	// Handles reports whether ref belongs to this vault implementation
	// (e.g. op:// prefix for 1Password).
	Handles(ref string) bool
}
