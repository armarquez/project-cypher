package secrets

import (
	"context"
	"os"
)

// Resolver resolves a secret reference to its plaintext value.
// Implementations declare which references they handle via Handles,
// allowing a Chain to dispatch to the correct backend.
// To add a new backend (e.g. HashiCorp Vault, AWS Secrets Manager),
// implement this interface and register it with NewChain.
type Resolver interface {
	// Handles reports whether this resolver can resolve the given reference.
	Handles(value string) bool
	// Resolve returns the plaintext for the given reference.
	Resolve(ctx context.Context, value string) (string, error)
}

// Chain dispatches to the first Resolver that claims to handle a value.
// If no resolver handles it, the value is returned unchanged (pass-through).
type Chain struct {
	resolvers []Resolver
}

// NewChain returns a Chain that tries resolvers in the given order.
func NewChain(resolvers ...Resolver) *Chain {
	return &Chain{resolvers: resolvers}
}

// Resolve dispatches to the first matching resolver.
// Returns value unchanged if no resolver handles it.
func (c *Chain) Resolve(ctx context.Context, value string) (string, error) {
	for _, r := range c.resolvers {
		if r.Handles(value) {
			return r.Resolve(ctx, value)
		}
	}
	return value, nil
}

// Default returns a Chain with all built-in resolvers registered.
// Currently: 1Password (op://).
func Default() *Chain {
	return NewChain(&OnePassword{})
}

// Resolve resolves a secret reference using the default resolver chain.
// Plain values are returned unchanged.
func Resolve(ctx context.Context, value string) (string, error) {
	return Default().Resolve(ctx, value)
}

// ResolveEnv reads the env var key and resolves it through the default chain.
// Returns empty string (no error) when the variable is unset.
func ResolveEnv(ctx context.Context, key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", nil
	}
	return Resolve(ctx, v)
}
