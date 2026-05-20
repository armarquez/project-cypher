package secrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- fake Resolver for Chain tests ---

type fakeResolver struct {
	scheme  string
	result  string
	callErr error
}

func (f *fakeResolver) Handles(value string) bool {
	return len(value) >= len(f.scheme) && value[:len(f.scheme)] == f.scheme
}

func (f *fakeResolver) Resolve(_ context.Context, _ string) (string, error) {
	if f.callErr != nil {
		return "", f.callErr
	}
	return f.result, nil
}

// --- Chain ---

func TestChain_DispatchFirstMatch(t *testing.T) {
	a := &fakeResolver{scheme: "a://", result: "from-a"}
	b := &fakeResolver{scheme: "b://", result: "from-b"}
	c := NewChain(a, b)

	got, err := c.Resolve(context.Background(), "a://item")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-a" {
		t.Errorf("got %q, want %q", got, "from-a")
	}
}

func TestChain_DispatchSecondMatch(t *testing.T) {
	a := &fakeResolver{scheme: "a://", result: "from-a"}
	b := &fakeResolver{scheme: "b://", result: "from-b"}
	c := NewChain(a, b)

	got, err := c.Resolve(context.Background(), "b://item")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-b" {
		t.Errorf("got %q, want %q", got, "from-b")
	}
}

func TestChain_Passthrough(t *testing.T) {
	a := &fakeResolver{scheme: "a://"}
	c := NewChain(a)

	got, err := c.Resolve(context.Background(), "plaintext-value")
	if err != nil {
		t.Fatal(err)
	}
	if got != "plaintext-value" {
		t.Errorf("got %q, want plaintext-value", got)
	}
}

func TestChain_EmptyChain(t *testing.T) {
	c := NewChain()
	got, err := c.Resolve(context.Background(), "any-value")
	if err != nil {
		t.Fatal(err)
	}
	if got != "any-value" {
		t.Errorf("got %q, want any-value", got)
	}
}

func TestChain_PropagatesError(t *testing.T) {
	a := &fakeResolver{scheme: "a://", callErr: fmt.Errorf("backend error")}
	c := NewChain(a)

	_, err := c.Resolve(context.Background(), "a://item")
	if err == nil {
		t.Fatal("expected error from resolver")
	}
}

// --- OnePassword.Handles ---

func TestOnePassword_Handles(t *testing.T) {
	o := &OnePassword{}
	cases := []struct {
		value string
		want  bool
	}{
		{"op://vault/item/field", true},
		{"op://", true},
		{"plaintext", false},
		{"vault://something", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := o.Handles(tc.value); got != tc.want {
			t.Errorf("Handles(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// --- OnePassword.Resolve using a fake `op` binary ---

func writeFakeOp(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "op")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake op: %v", err)
	}
	return bin
}

func TestOnePassword_Resolve_Success(t *testing.T) {
	bin := writeFakeOp(t, "printf '%s' mysecret")
	o := &OnePassword{OpPath: bin}

	got, err := o.Resolve(context.Background(), "op://vault/item/field")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mysecret" {
		t.Errorf("got %q, want %q", got, "mysecret")
	}
}

func TestOnePassword_Resolve_ExitError(t *testing.T) {
	bin := writeFakeOp(t, "echo 'not signed in' >&2; exit 1")
	o := &OnePassword{OpPath: bin}

	_, err := o.Resolve(context.Background(), "op://vault/item/field")
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if got := err.Error(); len(got) == 0 {
		t.Error("expected non-empty error message")
	}
}

func TestOnePassword_Resolve_BinaryNotFound(t *testing.T) {
	o := &OnePassword{OpPath: "/nonexistent/op"}

	_, err := o.Resolve(context.Background(), "op://vault/item/field")
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
}

// --- OnePassword.Available ---

func TestOnePassword_Available_True(t *testing.T) {
	bin := writeFakeOp(t, "exit 0")
	o := &OnePassword{OpPath: bin}
	if !o.Available() {
		t.Error("expected Available() = true when binary exists")
	}
}

func TestOnePassword_Available_False(t *testing.T) {
	o := &OnePassword{OpPath: "/nonexistent/op"}
	if o.Available() {
		t.Error("expected Available() = false when binary is missing")
	}
}

// --- OnePassword.Preflight ---

func TestOnePassword_Preflight_Success(t *testing.T) {
	// Simulate `op account list` returning an account entry.
	bin := writeFakeOp(t, "echo 'myaccount.1password.com'")
	o := &OnePassword{OpPath: bin}
	if err := o.Preflight(context.Background()); err != nil {
		t.Errorf("expected nil error when op succeeds, got: %v", err)
	}
}

func TestOnePassword_Preflight_CLIError(t *testing.T) {
	// op exits non-zero (e.g. desktop app not running, no session).
	bin := writeFakeOp(t, "echo '[ERROR] no accounts found' >&2; exit 1")
	o := &OnePassword{OpPath: bin}
	err := o.Preflight(context.Background())
	if err == nil {
		t.Fatal("expected error when op account list fails")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("expected 'not ready' in error, got: %v", err)
	}
}

func TestOnePassword_Preflight_NoAccounts(t *testing.T) {
	// op exits 0 but returns no accounts (unconfigured install).
	bin := writeFakeOp(t, "echo ''")
	o := &OnePassword{OpPath: bin}
	err := o.Preflight(context.Background())
	if err == nil {
		t.Fatal("expected error when account list is empty")
	}
	if !strings.Contains(err.Error(), "no accounts found") {
		t.Errorf("expected 'no accounts found' in error, got: %v", err)
	}
}

func TestOnePassword_Preflight_BinaryMissing(t *testing.T) {
	o := &OnePassword{OpPath: "/nonexistent/op"}
	err := o.Preflight(context.Background())
	if err == nil {
		t.Fatal("expected error when op binary is missing")
	}
}

// --- Package-level convenience functions ---

func TestResolveEnv_Unset(t *testing.T) {
	t.Setenv("CYPHER_TEST_SECRET_UNSET", "")
	got, err := ResolveEnv(context.Background(), "CYPHER_TEST_SECRET_UNSET")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string for unset var", got)
	}
}

func TestResolveEnv_Plaintext(t *testing.T) {
	t.Setenv("CYPHER_TEST_SECRET_PLAIN", "ghp_mytoken")
	got, err := ResolveEnv(context.Background(), "CYPHER_TEST_SECRET_PLAIN")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ghp_mytoken" {
		t.Errorf("got %q, want %q", got, "ghp_mytoken")
	}
}

func TestDefault_NonNil(t *testing.T) {
	if Default() == nil {
		t.Error("Default() should return a non-nil Chain")
	}
}

func TestResolve_Plaintext(t *testing.T) {
	got, err := Resolve(context.Background(), "plainvalue")
	if err != nil {
		t.Fatal(err)
	}
	if got != "plainvalue" {
		t.Errorf("got %q, want plainvalue", got)
	}
}

// --- OnePassword.Store ---

// writeFakeOpStore returns a fake op binary that handles:
//   - "account list" → exits 0 with an account line
//   - "item get <title> --vault ..." → exits 1 (no existing item)
//   - "item create --template ..." → exits 0
func writeFakeOpStore(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
if [ "$1" = "account" ] && [ "$2" = "list" ]; then echo "test.1password.com"; exit 0; fi
if [ "$1" = "item" ] && [ "$2" = "get" ]; then exit 1; fi
if [ "$1" = "item" ] && [ "$2" = "create" ]; then echo "{}"; exit 0; fi
exit 0
`
	dir := t.TempDir()
	bin := filepath.Join(dir, "op")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake op: %v", err)
	}
	return bin
}

func TestOnePasswordStore_ReturnsOPRef(t *testing.T) {
	bin := writeFakeOpStore(t)
	o := &OnePassword{OpPath: bin}

	ref, err := o.Store(context.Background(), "MyVault", "my-title", "private key", "pem-data")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if !strings.HasPrefix(ref, "op://") {
		t.Errorf("expected op:// ref, got %q", ref)
	}
	if ref != "op://MyVault/my-title/private key" {
		t.Errorf("unexpected ref format: %q", ref)
	}
}

func TestOnePasswordStore_BinaryMissing(t *testing.T) {
	o := &OnePassword{OpPath: "/nonexistent/op"}

	_, err := o.Store(context.Background(), "vault", "title", "label", "value")
	if err == nil {
		t.Fatal("expected error when op binary is missing")
	}
}

func TestOnePasswordStore_ItemCreateFails(t *testing.T) {
	script := `#!/bin/sh
if [ "$1" = "item" ] && [ "$2" = "get" ]; then exit 1; fi
if [ "$1" = "item" ] && [ "$2" = "create" ]; then echo "create failed" >&2; exit 1; fi
exit 0
`
	dir := t.TempDir()
	bin := filepath.Join(dir, "op")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake op: %v", err)
	}

	o := &OnePassword{OpPath: bin}
	_, err := o.Store(context.Background(), "vault", "title", "label", "value")
	if err == nil {
		t.Fatal("expected error when item create fails")
	}
	if !strings.Contains(err.Error(), "op item create") {
		t.Errorf("expected 'op item create' in error, got: %v", err)
	}
}

func TestOnePasswordStore_DeletesExistingItem(t *testing.T) {
	// "item get" succeeds (item exists), "item delete" is called, "item create" succeeds.
	deleteCalled := false
	script := `#!/bin/sh
if [ "$1" = "item" ] && [ "$2" = "get" ]; then printf '{"id":"abc123"}'; exit 0; fi
if [ "$1" = "item" ] && [ "$2" = "delete" ]; then touch /tmp/cypher-test-delete-called; exit 0; fi
if [ "$1" = "item" ] && [ "$2" = "create" ]; then echo "{}"; exit 0; fi
exit 0
`
	dir := t.TempDir()
	bin := filepath.Join(dir, "op")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake op: %v", err)
	}

	// Use a marker file in a temp dir instead of /tmp to avoid cross-test pollution.
	markerDir := t.TempDir()
	markerPath := filepath.Join(markerDir, "delete-called")
	markerScript := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "item" ] && [ "$2" = "get" ]; then printf '{"id":"abc123"}'; exit 0; fi
if [ "$1" = "item" ] && [ "$2" = "delete" ]; then touch %s; exit 0; fi
if [ "$1" = "item" ] && [ "$2" = "create" ]; then echo "{}"; exit 0; fi
exit 0
`, markerPath)
	if err := os.WriteFile(bin, []byte(markerScript), 0o755); err != nil {
		t.Fatalf("write fake op: %v", err)
	}

	o := &OnePassword{OpPath: bin}
	_, err := o.Store(context.Background(), "vault", "title", "label", "value")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		deleteCalled = false
	} else {
		deleteCalled = true
	}
	if !deleteCalled {
		t.Error("expected existing item to be deleted before creating new one")
	}
}

// --- FakeVault ---

func TestFakeVault_RoundTrip(t *testing.T) {
	fv := NewFakeVault()
	ctx := context.Background()

	ref, err := fv.Store(ctx, "vault", "title", "field", "myvalue")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if !strings.HasPrefix(ref, "op://") {
		t.Errorf("expected op:// ref, got %q", ref)
	}

	got, err := fv.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "myvalue" {
		t.Errorf("got %q, want %q", got, "myvalue")
	}
}

func TestFakeVault_GetNotFound(t *testing.T) {
	fv := NewFakeVault()
	_, err := fv.Get(context.Background(), "op://vault/missing/field")
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
}

func TestFakeVault_PreflightErr(t *testing.T) {
	fv := &FakeVault{PreflightErr: fmt.Errorf("not ready")}
	if err := fv.Preflight(context.Background()); err == nil {
		t.Fatal("expected preflight error")
	}
}

func TestFakeVault_StoreErr(t *testing.T) {
	fv := &FakeVault{StoreErr: fmt.Errorf("store failed")}
	_, err := fv.Store(context.Background(), "v", "t", "l", "val")
	if err == nil {
		t.Fatal("expected store error")
	}
}

func TestFakeVault_GetErr(t *testing.T) {
	fv := &FakeVault{GetErr: fmt.Errorf("get failed")}
	_, err := fv.Get(context.Background(), "op://v/t/l")
	if err == nil {
		t.Fatal("expected get error")
	}
}

func TestFakeVault_Handles(t *testing.T) {
	fv := NewFakeVault()
	if !fv.Handles("op://vault/item/field") {
		t.Error("expected Handles=true for op:// ref")
	}
	if fv.Handles("plaintext") {
		t.Error("expected Handles=false for plaintext")
	}
}
