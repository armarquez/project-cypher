package architect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	defaultBaseURL        = "https://api.anthropic.com"
	anthropicVersion      = "2023-06-01"
	defaultMaxTokens      = 1024
)

// Client calls the Anthropic Messages API on behalf of the Control Plane.
// It is intended for Architect-tier tasks (classification, review) — not for
// proxying worker traffic (see internal/gateway for that).
type Client struct {
	model      string
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// New creates a Client targeting the given model. httpClient may be nil (uses
// http.DefaultClient). baseURL may be empty (uses the Anthropic public API).
// Pass a non-empty baseURL only in tests via httptest.
func New(model, apiKey string, httpClient *http.Client) *Client {
	return newWithBase(model, apiKey, defaultBaseURL, httpClient)
}

func newWithBase(model, apiKey, baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		model:      model,
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: httpClient,
	}
}

// Complete sends systemPrompt + a single user message to the Anthropic Messages
// API and returns the assistant's text response.
func (c *Client) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	reqBody := messagesRequest{
		Model:     c.model,
		MaxTokens: defaultMaxTokens,
		System:    systemPrompt,
		Messages: []message{
			{Role: "user", Content: userMessage},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("architect: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("architect: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("architect: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("architect: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("architect: API returned %d: %s", resp.StatusCode, respBytes)
	}

	var respBody messagesResponse
	if err := json.Unmarshal(respBytes, &respBody); err != nil {
		return "", fmt.Errorf("architect: unmarshal response: %w", err)
	}

	if len(respBody.Content) == 0 {
		return "", fmt.Errorf("architect: empty content in response")
	}

	return respBody.Content[0].Text, nil
}

// --- API wire types ---

type messagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
