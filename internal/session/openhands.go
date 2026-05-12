package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ConversationStatus represents the lifecycle state of an OpenHands conversation.
type ConversationStatus string

const (
	StatusRunning  ConversationStatus = "running"
	StatusFinished ConversationStatus = "finished"
	StatusError    ConversationStatus = "error"
)

// IsTerminal reports whether the status indicates the conversation has ended,
// either successfully or in failure.
func (s ConversationStatus) IsTerminal() bool {
	return s == StatusFinished || s == StatusError
}

// Event is a single item from the OpenHands event stream. Text contains
// whatever agent-produced content was in the event — the orchestrator scans
// this for HITL markers and completion signals.
type Event struct {
	ID     int    `json:"id"`
	Source string `json:"source"` // "agent", "user", or "environment"
	Text   string `json:"text"`   // extracted agent message or observation content
}

// OpenHandsClient is an HTTP client for the OpenHands REST API running inside
// a worker container. baseURL is typically http://localhost:{mapped-port}.
//
// API shapes are based on OpenHands main branch as of implementation. Field
// names may need adjustment for a specific pinned image version.
type OpenHandsClient struct {
	http    *http.Client
	baseURL string
}

// NewOpenHandsClient returns a client targeting the OpenHands API at baseURL.
func NewOpenHandsClient(httpClient *http.Client, baseURL string) *OpenHandsClient {
	return &OpenHandsClient{http: httpClient, baseURL: baseURL}
}

func (c *OpenHandsClient) do(ctx context.Context, method, path string, body, dst any) error {
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

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openhands request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errBody struct {
			Detail string `json:"detail"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
		return fmt.Errorf("openhands API %s %s: %d %s", method, path, resp.StatusCode, errBody.Detail)
	}

	if dst != nil {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}

// StartTask submits a task prompt to OpenHands and returns the conversation ID.
// The conversation begins immediately; use FetchEvents to stream progress.
func (c *OpenHandsClient) StartTask(ctx context.Context, taskPrompt string) (string, error) {
	body := struct {
		InitialMsg string `json:"initial_user_msg"`
	}{InitialMsg: taskPrompt}

	var resp struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/conversations", body, &resp); err != nil {
		return "", fmt.Errorf("start task: %w", err)
	}
	if resp.ConversationID == "" {
		return "", fmt.Errorf("start task: empty conversation ID in response")
	}
	return resp.ConversationID, nil
}

// rawEvent is the OpenHands wire format. Depending on whether it is an action
// or an observation, content lives in different fields.
type rawEvent struct {
	ID          int            `json:"id"`
	Source      string         `json:"source"`
	Action      string         `json:"action,omitempty"`
	Observation string         `json:"observation,omitempty"`
	Args        map[string]any `json:"args,omitempty"`
	Content     string         `json:"content,omitempty"`
}

// extractText pulls the human-readable text out of a raw event so the
// orchestrator can scan it for HITL markers without knowing OpenHands internals.
func extractText(e rawEvent) string {
	// Agent action events carry text in args["content"].
	if e.Source == "agent" && e.Args != nil {
		if v, ok := e.Args["content"].(string); ok && v != "" {
			return v
		}
	}
	// Observation events carry text directly in content.
	if e.Content != "" {
		return e.Content
	}
	return ""
}

// FetchEvents returns events for a conversation starting after sinceID.
// Pass sinceID=0 to fetch from the beginning. The caller should track the
// highest event ID seen and pass it as sinceID on successive calls to avoid
// re-processing events.
func (c *OpenHandsClient) FetchEvents(ctx context.Context, conversationID string, sinceID int) ([]Event, error) {
	path := fmt.Sprintf("/api/conversations/%s/events?limit=100&start_id=%d", conversationID, sinceID)

	var raw struct {
		Events []rawEvent `json:"events"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, fmt.Errorf("fetch events %s: %w", conversationID, err)
	}

	events := make([]Event, 0, len(raw.Events))
	for _, e := range raw.Events {
		events = append(events, Event{
			ID:     e.ID,
			Source: e.Source,
			Text:   extractText(e),
		})
	}
	return events, nil
}

// GetStatus returns the current lifecycle status of a conversation.
func (c *OpenHandsClient) GetStatus(ctx context.Context, conversationID string) (ConversationStatus, error) {
	path := "/api/conversations/" + conversationID

	var resp struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return StatusError, fmt.Errorf("get status %s: %w", conversationID, err)
	}
	return ConversationStatus(resp.Status), nil
}
