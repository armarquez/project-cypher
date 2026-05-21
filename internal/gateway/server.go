package gateway

import (
	"context"
	"fmt"
	"net/http"
)

// Server is the Control Plane HTTP gateway. Workers route all LLM API
// calls through it; it proxies them to the appropriate vendor endpoint.
type Server struct {
	srv *http.Server
}

// NewServer creates a gateway Server listening on addr. webhook may be nil —
// when non-nil, it is registered at POST /webhook. Call Start to accept connections.
func NewServer(addr string, router *Router, webhook *WebhookServer) *Server {
	mux := http.NewServeMux()
	mux.Handle("/health", http.HandlerFunc(handleHealth))
	if webhook != nil {
		mux.Handle("/webhook", webhook)
	}
	mux.Handle("/", router)

	return &Server{
		srv: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// Start begins listening and blocks until the server stops.
func (s *Server) Start() error {
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("gateway server: %w", err)
	}
	return nil
}

// Shutdown gracefully drains active connections.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
}
