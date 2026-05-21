package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func prBody(action string) []byte {
	b, _ := json.Marshal(map[string]string{"action": action, "number": "42"})
	return b
}

func webhookRequest(method, body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(method, "/webhook", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// --- verifySignature unit tests ---

func TestVerifySignature_Valid(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(secret, body, sig) {
		t.Fatal("expected valid signature to pass")
	}
}

func TestVerifySignature_WrongSecret(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	sig := sign("correct-secret", body)
	if verifySignature([]byte("wrong-secret"), body, sig) {
		t.Fatal("expected wrong secret to fail")
	}
}

func TestVerifySignature_TamperedBody(t *testing.T) {
	secret := []byte("secret")
	sig := sign("secret", []byte(`{"action":"opened"}`))
	if verifySignature(secret, []byte(`{"action":"closed"}`), sig) {
		t.Fatal("expected tampered body to fail")
	}
}

func TestVerifySignature_MissingPrefix(t *testing.T) {
	body := []byte(`{}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil)) // missing "sha256=" prefix
	if verifySignature([]byte("secret"), body, sig) {
		t.Fatal("expected missing prefix to fail")
	}
}

func TestVerifySignature_InvalidHex(t *testing.T) {
	if verifySignature([]byte("secret"), []byte("body"), "sha256=ZZZZ") {
		t.Fatal("expected invalid hex to fail")
	}
}

func TestVerifySignature_EmptySignature(t *testing.T) {
	if verifySignature([]byte("secret"), []byte("body"), "") {
		t.Fatal("expected empty signature to fail")
	}
}

// --- WebhookServer handler tests ---

func TestWebhookServer_ValidRequest_Dispatches(t *testing.T) {
	srv := NewWebhookServer("my-secret")

	var received Event
	var wg sync.WaitGroup
	wg.Add(1)
	srv.Register("pull_request", func(_ context.Context, e Event) error {
		received = e
		wg.Done()
		return nil
	})

	body := prBody("opened")
	req := webhookRequest(http.MethodPost, string(body), map[string]string{
		"X-GitHub-Event":    "pull_request",
		"X-Hub-Signature-256": sign("my-secret", body),
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	wg.Wait()
	if received.Type != "pull_request" {
		t.Errorf("event.Type = %q, want %q", received.Type, "pull_request")
	}
	if received.Action != "opened" {
		t.Errorf("event.Action = %q, want %q", received.Action, "opened")
	}
	if len(received.Payload) == 0 {
		t.Error("event.Payload is empty")
	}
}

func TestWebhookServer_InvalidSignature_Returns401(t *testing.T) {
	srv := NewWebhookServer("my-secret")
	body := prBody("opened")
	req := webhookRequest(http.MethodPost, string(body), map[string]string{
		"X-GitHub-Event":    "pull_request",
		"X-Hub-Signature-256": sign("wrong-secret", body),
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestWebhookServer_MissingSignature_Returns401(t *testing.T) {
	srv := NewWebhookServer("my-secret")
	body := prBody("opened")
	req := webhookRequest(http.MethodPost, string(body), map[string]string{
		"X-GitHub-Event": "pull_request",
		// no X-Hub-Signature-256
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestWebhookServer_UnknownEventType_Returns200Silently(t *testing.T) {
	srv := NewWebhookServer("my-secret")
	// no handler registered

	body := []byte(`{"action":"created"}`)
	req := webhookRequest(http.MethodPost, string(body), map[string]string{
		"X-GitHub-Event":    "issues",
		"X-Hub-Signature-256": sign("my-secret", body),
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for unregistered event type", w.Code)
	}
}

func TestWebhookServer_NoSecret_SkipsVerification(t *testing.T) {
	srv := NewWebhookServer("") // no secret

	called := false
	var wg sync.WaitGroup
	wg.Add(1)
	srv.Register("push", func(_ context.Context, _ Event) error {
		called = true
		wg.Done()
		return nil
	})

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := webhookRequest(http.MethodPost, string(body), map[string]string{
		"X-GitHub-Event": "push",
		// no signature — should still work with empty secret
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	wg.Wait()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestWebhookServer_PRActions(t *testing.T) {
	cases := []string{"opened", "synchronize", "reopened", "closed"}
	for _, action := range cases {
		t.Run(action, func(t *testing.T) {
			srv := NewWebhookServer("secret")
			var wg sync.WaitGroup
			wg.Add(1)
			var gotAction string
			srv.Register("pull_request", func(_ context.Context, e Event) error {
				gotAction = e.Action
				wg.Done()
				return nil
			})

			body := prBody(action)
			req := webhookRequest(http.MethodPost, string(body), map[string]string{
				"X-GitHub-Event":    "pull_request",
				"X-Hub-Signature-256": sign("secret", body),
			})
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			wg.Wait()
			if gotAction != action {
				t.Errorf("action = %q, want %q", gotAction, action)
			}
		})
	}
}

func TestWebhookServer_Register_Replaces(t *testing.T) {
	srv := NewWebhookServer("")

	var wg sync.WaitGroup
	wg.Add(1)
	calls := 0
	srv.Register("ping", func(_ context.Context, _ Event) error {
		calls++
		wg.Done()
		return nil
	})
	srv.Register("ping", func(_ context.Context, _ Event) error {
		calls += 10
		wg.Done()
		return nil
	})

	body := []byte(`{}`)
	req := webhookRequest(http.MethodPost, string(body), map[string]string{
		"X-GitHub-Event": "ping",
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	wg.Wait()

	if calls != 10 {
		t.Errorf("calls = %d, want 10 (second handler should replace first)", calls)
	}
}
