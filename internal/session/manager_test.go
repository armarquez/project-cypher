package session

import (
	"context"
	"errors"
	"testing"
)

// fakeDocker is an in-memory implementation of dockerAPI for Manager and Session tests.
type fakeDocker struct {
	createID  string
	createErr error
	startErr  error
	stopErr   error
	removeErr error

	created int
	started []string
	stopped []string
	removed []string
}

func (f *fakeDocker) createContainer(_ context.Context, _ ContainerConfig) (string, error) {
	f.created++
	return f.createID, f.createErr
}

func (f *fakeDocker) startContainer(_ context.Context, id string) error {
	f.started = append(f.started, id)
	return f.startErr
}

func (f *fakeDocker) stopContainer(_ context.Context, id string) error {
	f.stopped = append(f.stopped, id)
	return f.stopErr
}

func (f *fakeDocker) removeContainer(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return f.removeErr
}

func newFakeManager(d *fakeDocker) *Manager {
	return &Manager{docker: d, cfg: ContainerConfig{Image: "test-image"}}
}

// --- Manager.Create ---

func TestCreate_StartsContainerAndReturnsSession(t *testing.T) {
	d := &fakeDocker{createID: "ctr-1"}
	sess, err := newFakeManager(d).Create(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.ContainerID() != "ctr-1" {
		t.Errorf("ContainerID = %q, want ctr-1", sess.ContainerID())
	}
	if d.created != 1 {
		t.Errorf("createContainer called %d times, want 1", d.created)
	}
	if len(d.started) != 1 || d.started[0] != "ctr-1" {
		t.Errorf("startContainer calls = %v, want [ctr-1]", d.started)
	}
}

func TestCreate_CreateErrorPropagates(t *testing.T) {
	d := &fakeDocker{createErr: errors.New("image not found")}
	_, err := newFakeManager(d).Create(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreate_StartErrorRemovesContainer(t *testing.T) {
	d := &fakeDocker{createID: "ctr-2", startErr: errors.New("start failed")}
	_, err := newFakeManager(d).Create(context.Background())
	if err == nil {
		t.Fatal("expected error when start fails")
	}
	if len(d.removed) != 1 || d.removed[0] != "ctr-2" {
		t.Errorf("removeContainer calls = %v; should clean up ctr-2 after start failure", d.removed)
	}
}

// --- Session.Pause ---

func TestPause_StopsContainer(t *testing.T) {
	d := &fakeDocker{}
	sess := &Session{containerID: "ctr-3", docker: d}

	if err := sess.Pause(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.stopped) != 1 || d.stopped[0] != "ctr-3" {
		t.Errorf("stopContainer calls = %v, want [ctr-3]", d.stopped)
	}
}

func TestPause_ErrorPropagates(t *testing.T) {
	d := &fakeDocker{stopErr: errors.New("daemon unavailable")}
	sess := &Session{containerID: "ctr-4", docker: d}

	err := sess.Pause(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- Session.Resume ---

func TestResume_StartsContainer(t *testing.T) {
	d := &fakeDocker{}
	sess := &Session{containerID: "ctr-5", docker: d}

	if err := sess.Resume(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.started) != 1 || d.started[0] != "ctr-5" {
		t.Errorf("startContainer calls = %v, want [ctr-5]", d.started)
	}
}

func TestResume_ErrorPropagates(t *testing.T) {
	d := &fakeDocker{startErr: errors.New("cannot start")}
	sess := &Session{containerID: "ctr-6", docker: d}

	err := sess.Resume(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- Session.Destroy ---

func TestDestroy_RemovesContainer(t *testing.T) {
	d := &fakeDocker{}
	sess := &Session{containerID: "ctr-7", docker: d}

	if err := sess.Destroy(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.removed) != 1 || d.removed[0] != "ctr-7" {
		t.Errorf("removeContainer calls = %v, want [ctr-7]", d.removed)
	}
}

func TestDestroy_ErrorPropagates(t *testing.T) {
	d := &fakeDocker{removeErr: errors.New("permission denied")}
	sess := &Session{containerID: "ctr-8", docker: d}

	err := sess.Destroy(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- hitl.Session interface compliance ---

// sessionInterface is a compile-time check that *Session satisfies hitl.Session.
// hitl.Session requires Pause and Resume — both are implemented above.
var _ interface {
	Pause(context.Context) error
	Resume(context.Context) error
} = (*Session)(nil)
