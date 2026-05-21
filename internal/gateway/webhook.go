package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Handler is called for a verified webhook event.
type Handler func(ctx context.Context, event Event) error

// Event carries a parsed GitHub webhook event.
type Event struct {
	Type    string          // X-GitHub-Event value, e.g. "pull_request"
	Action  string          // action field from the payload, e.g. "opened"
	Payload json.RawMessage // raw JSON body for further parsing
}

// WebhookServer receives and dispatches GitHub webhook events. It verifies
// HMAC-SHA256 signatures before dispatching to registered handlers.
type WebhookServer struct {
	secret   []byte
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewWebhookServer creates a WebhookServer that verifies payloads with secret.
// An empty secret disables signature verification (tests only — not for production).
func NewWebhookServer(secret string) *WebhookServer {
	return &WebhookServer{
		secret:   []byte(secret),
		handlers: make(map[string]Handler),
	}
}

// Register adds a handler for the given event type (e.g. "pull_request").
// Replaces any previously registered handler for that type.
func (s *WebhookServer) Register(eventType string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[eventType] = h
}

// ServeHTTP verifies the HMAC signature, writes 200 OK immediately, and
// dispatches the event to a registered handler in a background goroutine so
// GitHub does not time out waiting for the handler to complete.
func (s *WebhookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	if len(s.secret) > 0 {
		if !verifySignature(s.secret, body, r.Header.Get("X-Hub-Signature-256")) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	eventType := r.Header.Get("X-GitHub-Event")

	var envelope struct {
		Action string `json:"action"`
	}
	json.Unmarshal(body, &envelope) //nolint:errcheck — action may be absent for some event types

	s.mu.RLock()
	h, ok := s.handlers[eventType]
	s.mu.RUnlock()

	w.WriteHeader(http.StatusOK)

	if !ok {
		return
	}

	event := Event{
		Type:    eventType,
		Action:  envelope.Action,
		Payload: json.RawMessage(body),
	}
	// Use a detached context — the request context will be cancelled once the
	// HTTP connection closes, but the handler must outlive the response.
	go h(context.Background(), event) //nolint:errcheck
}

// verifySignature checks that signature matches sha256=HMAC-SHA256(secret, body).
func verifySignature(secret, body []byte, signature string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	sigBytes, err := hex.DecodeString(signature[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), sigBytes)
}
