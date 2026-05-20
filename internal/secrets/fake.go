package secrets

import (
	"context"
	"fmt"
	"strings"
)

// FakeVault is an in-memory Vault for use in tests. Errors can be injected
// via PreflightErr, StoreErr, GetErr, and DeleteErr. Store/Get form a
// round-trip through an in-memory map so tests can assert the right values
// were stored.
type FakeVault struct {
	PreflightErr error
	StoreErr     error
	GetErr       error
	DeleteErr    error
	items        map[string]string
}

// NewFakeVault returns a FakeVault with an initialised item store.
func NewFakeVault() *FakeVault {
	return &FakeVault{items: make(map[string]string)}
}

func (f *FakeVault) Preflight(_ context.Context) error { return f.PreflightErr }

func (f *FakeVault) Store(_ context.Context, vault, title, label, value string) (string, error) {
	if f.StoreErr != nil {
		return "", f.StoreErr
	}
	if f.items == nil {
		f.items = make(map[string]string)
	}
	ref := fmt.Sprintf("op://%s/%s/%s", vault, title, label)
	f.items[ref] = value
	return ref, nil
}

func (f *FakeVault) Get(_ context.Context, ref string) (string, error) {
	if f.GetErr != nil {
		return "", f.GetErr
	}
	if v, ok := f.items[ref]; ok {
		return v, nil
	}
	return "", fmt.Errorf("FakeVault: ref not found: %s", ref)
}

func (f *FakeVault) Delete(_ context.Context, ref string) error {
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if f.items != nil {
		delete(f.items, ref)
	}
	return nil
}

func (f *FakeVault) Handles(ref string) bool {
	return strings.HasPrefix(ref, "op://")
}
