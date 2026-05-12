package session

import (
	"context"
	"fmt"
	"sync"
)

// ContainerConfig holds the Docker configuration for an OpenHands worker container.
// NetworkMode should be set to a network that allows the container to reach the
// Control Plane gateway but blocks direct internet access (e.g. a custom bridge).
type ContainerConfig struct {
	Image       string
	Env         []string // KEY=VALUE pairs injected into the container
	Binds       []string // host:container volume mount pairs
	NetworkMode string   // Docker network mode ("none", "host", or named network)
}

// Session manages the lifecycle of a single OpenHands container.
// It satisfies the hitl.Session interface (Pause/Resume) via the Docker stop/start cycle.
type Session struct {
	containerID string
	docker      dockerAPI
	mu          sync.Mutex
}

// ContainerID returns the Docker container ID assigned at creation.
func (s *Session) ContainerID() string {
	return s.containerID
}

// Pause stops the container without removing it. Satisfies hitl.Session.
// The container retains its filesystem state and can be resumed.
func (s *Session) Pause(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.docker.stopContainer(ctx, s.containerID); err != nil {
		return fmt.Errorf("session pause: %w", err)
	}
	return nil
}

// Resume restarts a stopped container. Satisfies hitl.Session.
func (s *Session) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.docker.startContainer(ctx, s.containerID); err != nil {
		return fmt.Errorf("session resume: %w", err)
	}
	return nil
}

// Destroy force-removes the container. Called after the task completes or fails.
// The container is gone after this call regardless of its running state.
func (s *Session) Destroy(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.docker.removeContainer(ctx, s.containerID); err != nil {
		return fmt.Errorf("session destroy: %w", err)
	}
	return nil
}

// Manager creates fresh OpenHands sessions on demand.
type Manager struct {
	docker dockerAPI
	cfg    ContainerConfig
}

// NewManager returns a Manager that communicates with Docker via socketPath.
func NewManager(socketPath string, cfg ContainerConfig) *Manager {
	return &Manager{docker: newDockerClient(socketPath), cfg: cfg}
}

// Create starts a fresh container and returns the active Session.
// If the container is created but fails to start, it is removed before returning
// the error so no orphaned containers are left behind.
func (m *Manager) Create(ctx context.Context) (*Session, error) {
	id, err := m.docker.createContainer(ctx, m.cfg)
	if err != nil {
		return nil, fmt.Errorf("manager create: %w", err)
	}

	if err := m.docker.startContainer(ctx, id); err != nil {
		m.docker.removeContainer(ctx, id) //nolint:errcheck
		return nil, fmt.Errorf("manager create: start: %w", err)
	}

	return &Session{containerID: id, docker: m.docker}, nil
}
