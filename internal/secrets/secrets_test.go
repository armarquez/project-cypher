package secrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	bin := writeFakeOp(t, "exit 0")
	o := &OnePassword{OpPath: bin}
	if err := o.Preflight(context.Background()); err != nil {
		t.Errorf("expected nil error when op succeeds, got: %v", err)
	}
}

func TestOnePassword_Preflight_NotSignedIn(t *testing.T) {
	bin := writeFakeOp(t, "echo '[ERROR] account is not signed in' >&2; exit 1")
	o := &OnePassword{OpPath: bin}
	err := o.Preflight(context.Background())
	if err == nil {
		t.Fatal("expected error when op is not signed in")
	}
	if !containsAny(err.Error(), "not signed in", "not ready") {
		t.Errorf("expected auth-related message in error, got: %v", err)
	}
}

func TestOnePassword_Preflight_BinaryMissing(t *testing.T) {
	o := &OnePassword{OpPath: "/nonexistent/op"}
	err := o.Preflight(context.Background())
	if err == nil {
		t.Fatal("expected error when op binary is missing")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
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
