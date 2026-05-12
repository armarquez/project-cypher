package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// dockerAPI is the subset of Docker Engine operations used by the session manager.
// Defined as an interface so tests can substitute a fake without a live daemon.
type dockerAPI interface {
	createContainer(ctx context.Context, cfg ContainerConfig) (string, error)
	startContainer(ctx context.Context, id string) error
	stopContainer(ctx context.Context, id string) error
	removeContainer(ctx context.Context, id string) error
}

// dockerClient talks to the Docker daemon over its Unix socket using stdlib
// net/http. No external SDK is required.
type dockerClient struct {
	http    *http.Client
	baseURL string
}

// newDockerClient returns a client that dials the Docker Unix socket at socketPath.
func newDockerClient(socketPath string) *dockerClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &dockerClient{
		http:    &http.Client{Transport: transport},
		baseURL: "http://localhost",
	}
}

// newDockerClientAt builds a client using the given HTTP client and base URL.
// Used in tests to point at an httptest.Server instead of the Unix socket.
func newDockerClientAt(httpClient *http.Client, baseURL string) *dockerClient {
	return &dockerClient{http: httpClient, baseURL: baseURL}
}

func (d *dockerClient) do(ctx context.Context, method, path string, body, dst any) error {
	var reqBody *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	} else {
		reqBody = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("docker request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errBody struct {
			Message string `json:"message"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
		return fmt.Errorf("docker API %s %s: %d %s", method, path, resp.StatusCode, errBody.Message)
	}

	if dst != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}

// createContainerRequest is the Docker Engine API body for POST /containers/create.
type createContainerRequest struct {
	Image      string     `json:"Image"`
	Env        []string   `json:"Env,omitempty"`
	HostConfig hostConfig `json:"HostConfig"`
}

type hostConfig struct {
	Binds       []string `json:"Binds,omitempty"`
	NetworkMode string   `json:"NetworkMode,omitempty"`
}

type createContainerResponse struct {
	ID string `json:"Id"`
}

func (d *dockerClient) createContainer(ctx context.Context, cfg ContainerConfig) (string, error) {
	body := createContainerRequest{
		Image: cfg.Image,
		Env:   cfg.Env,
		HostConfig: hostConfig{
			Binds:       cfg.Binds,
			NetworkMode: cfg.NetworkMode,
		},
	}
	var resp createContainerResponse
	if err := d.do(ctx, http.MethodPost, "/v1.41/containers/create", body, &resp); err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	if resp.ID == "" {
		return "", fmt.Errorf("create container: empty ID in response")
	}
	return resp.ID, nil
}

func (d *dockerClient) startContainer(ctx context.Context, id string) error {
	path := "/v1.41/containers/" + id + "/start"
	if err := d.do(ctx, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("start container %s: %w", id, err)
	}
	return nil
}

func (d *dockerClient) stopContainer(ctx context.Context, id string) error {
	path := "/v1.41/containers/" + id + "/stop?t=10"
	if err := d.do(ctx, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("stop container %s: %w", id, err)
	}
	return nil
}

// removeContainer force-removes the container (stops it first if running).
func (d *dockerClient) removeContainer(ctx context.Context, id string) error {
	path := "/v1.41/containers/" + id + "?force=true"
	if err := d.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return fmt.Errorf("remove container %s: %w", id, err)
	}
	return nil
}
