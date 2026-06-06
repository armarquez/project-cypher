package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testDockerClient(t *testing.T, baseURL string) *dockerClient {
	t.Helper()
	return newDockerClientAt(http.DefaultClient, baseURL)
}

// dockerServer starts a test server that routes Docker API calls by method+path.
type dockerServer struct {
	srv      *httptest.Server
	handlers map[string]http.HandlerFunc // key: "METHOD /path"
}

func newDockerServer(t *testing.T) *dockerServer {
	t.Helper()
	ds := &dockerServer{handlers: make(map[string]http.HandlerFunc)}
	ds.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		// Strip query string for routing.
		if h, ok := ds.handlers[key]; ok {
			h(w, r)
			return
		}
		// Fallback: check prefix match (e.g. container ID in path).
		for k, h := range ds.handlers {
			parts := strings.SplitN(k, " ", 2)
			if parts[0] == r.Method && strings.HasPrefix(r.URL.Path, parts[1]) {
				h(w, r)
				return
			}
		}
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(ds.srv.Close)
	return ds
}

func (ds *dockerServer) on(method, path string, fn http.HandlerFunc) {
	ds.handlers[method+" "+path] = fn
}

func (ds *dockerServer) replyJSON(status int, body any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			json.NewEncoder(w).Encode(body) //nolint:errcheck
		}
	}
}

func (ds *dockerServer) replyNoContent() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- createContainer ---

func TestCreateContainer_ReturnsID(t *testing.T) {
	ds := newDockerServer(t)
	ds.on("POST", "/containers/create",
		ds.replyJSON(http.StatusCreated, map[string]string{"Id": "abc123", "Warnings": ""}))

	id, err := testDockerClient(t, ds.srv.URL).createContainer(context.Background(), ContainerConfig{
		Image:       "ghcr.io/all-hands-ai/openhands:main",
		NetworkMode: "none",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "abc123" {
		t.Errorf("id = %q, want abc123", id)
	}
}

func TestCreateContainer_SendsCorrectBody(t *testing.T) {
	var gotBody map[string]any
	ds := newDockerServer(t)
	ds.on("POST", "/containers/create", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"Id": "x1"}) //nolint:errcheck
	})

	cfg := ContainerConfig{
		Image:       "my-image",
		Env:         []string{"FOO=bar"},
		Binds:       []string{"/host:/container"},
		NetworkMode: "none",
	}
	testDockerClient(t, ds.srv.URL).createContainer(context.Background(), cfg) //nolint:errcheck

	if gotBody["Image"] != "my-image" {
		t.Errorf("Image = %v, want my-image", gotBody["Image"])
	}
	hc, _ := gotBody["HostConfig"].(map[string]any)
	if hc["NetworkMode"] != "none" {
		t.Errorf("NetworkMode = %v, want none", hc["NetworkMode"])
	}
}

func TestCreateContainer_EmptyIDError(t *testing.T) {
	ds := newDockerServer(t)
	ds.on("POST", "/containers/create",
		ds.replyJSON(http.StatusCreated, map[string]string{"Id": ""}))

	_, err := testDockerClient(t, ds.srv.URL).createContainer(context.Background(), ContainerConfig{Image: "img"})
	if err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
}

func TestCreateContainer_APIError(t *testing.T) {
	ds := newDockerServer(t)
	ds.on("POST", "/containers/create",
		ds.replyJSON(http.StatusNotFound, map[string]string{"message": "No such image"}))

	_, err := testDockerClient(t, ds.srv.URL).createContainer(context.Background(), ContainerConfig{Image: "bad"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "No such image") {
		t.Errorf("error %q missing API message", err.Error())
	}
}

// --- startContainer ---

func TestStartContainer_Success(t *testing.T) {
	ds := newDockerServer(t)
	ds.on("POST", "/containers/abc/start", ds.replyNoContent())

	err := testDockerClient(t, ds.srv.URL).startContainer(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartContainer_APIError(t *testing.T) {
	ds := newDockerServer(t)
	ds.on("POST", "/containers/abc/start",
		ds.replyJSON(http.StatusNotFound, map[string]string{"message": "No such container"}))

	err := testDockerClient(t, ds.srv.URL).startContainer(context.Background(), "abc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- stopContainer ---

func TestStopContainer_Success(t *testing.T) {
	ds := newDockerServer(t)
	ds.on("POST", "/containers/abc/stop", ds.replyNoContent())

	err := testDockerClient(t, ds.srv.URL).stopContainer(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- removeContainer ---

func TestRemoveContainer_Success(t *testing.T) {
	ds := newDockerServer(t)
	ds.on("DELETE", "/containers/abc", ds.replyNoContent())

	err := testDockerClient(t, ds.srv.URL).removeContainer(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveContainer_APIError(t *testing.T) {
	ds := newDockerServer(t)
	ds.on("DELETE", "/containers/abc",
		ds.replyJSON(http.StatusConflict, map[string]string{"message": "container is running"}))

	err := testDockerClient(t, ds.srv.URL).removeContainer(context.Background(), "abc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("error %q should contain status code 409", err.Error())
	}
}
