//go:build integration

package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Integration tests for OnePassword against the real op CLI.
//
// Requirements:
//   - CYPHER_TEST_OP_VAULT must be set to an existing 1Password vault name.
//   - The op CLI must be installed and authenticated (desktop app running or
//     `op signin` completed).
//
// Run with:
//
//	CYPHER_TEST_OP_VAULT=Private just test-integration

func requireVault(t *testing.T) string {
	t.Helper()
	v := os.Getenv("CYPHER_TEST_OP_VAULT")
	if v == "" {
		t.Skip("CYPHER_TEST_OP_VAULT not set — skipping integration tests")
	}
	return v
}

func uniqueTitle(prefix string) string {
	return fmt.Sprintf("cypher-test-%s-%d", prefix, time.Now().UnixNano())
}

func TestIntegration_OnePassword_Preflight(t *testing.T) {
	requireVault(t)
	o := &OnePassword{}
	if err := o.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight failed: %v", err)
	}
}

func TestIntegration_OnePassword_StoreAndGet(t *testing.T) {
	vault := requireVault(t)
	o := &OnePassword{}
	title := uniqueTitle("store-get")

	ref, err := o.Store(context.Background(), vault, title, "token", "test-secret-value")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	t.Logf("stored ref: %s", ref)

	if !strings.HasPrefix(ref, "op://") {
		t.Errorf("expected op:// ref, got %q", ref)
	}

	got, err := o.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "test-secret-value" {
		t.Errorf("round-trip value mismatch: got %q, want %q", got, "test-secret-value")
	}

	// Clean up.
	o.deleteExisting(context.Background(), vault, title)
}

func TestIntegration_OnePassword_StoreOverwritesExisting(t *testing.T) {
	vault := requireVault(t)
	o := &OnePassword{}
	title := uniqueTitle("overwrite")

	ref1, err := o.Store(context.Background(), vault, title, "token", "original-value")
	if err != nil {
		t.Fatalf("first Store: %v", err)
	}

	ref2, err := o.Store(context.Background(), vault, title, "token", "updated-value")
	if err != nil {
		t.Fatalf("second Store: %v", err)
	}

	if ref1 != ref2 {
		t.Errorf("refs differ: %q vs %q", ref1, ref2)
	}

	got, err := o.Get(context.Background(), ref2)
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if got != "updated-value" {
		t.Errorf("expected updated value, got %q", got)
	}

	o.deleteExisting(context.Background(), vault, title)
}

func TestIntegration_OnePassword_GetMissingRef(t *testing.T) {
	requireVault(t)
	o := &OnePassword{}
	_, err := o.Get(context.Background(), "op://nonexistent-vault/no-such-item/field")
	if err == nil {
		t.Fatal("expected error for non-existent ref")
	}
}
