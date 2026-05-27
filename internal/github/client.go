package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const defaultBaseURL = "https://api.github.com"

// Client is an authenticated GitHub API client. It holds the PAT and is the
// only component in Cypher that talks to the GitHub API directly.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a Client using the provided PAT and HTTP client.
// baseURL overrides the GitHub API base for testing; pass "" for production.
func NewClient(token string, httpClient *http.Client, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: httpClient,
	}
}

// Ping validates the client's credentials by calling GET /rate_limit.
// Returns nil on success; works for both PAT and App installation tokens.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, "/rate_limit", nil)
}

// do executes an authenticated GET request and decodes the JSON response into dst.
func (c *Client) do(ctx context.Context, path string, dst any) error {
	return c.doMethod(ctx, http.MethodGet, path, nil, dst)
}

// doMethod executes an authenticated request with an optional JSON body.
// body may be nil. dst may be nil if the response body is not needed.
func (c *Client) doMethod(ctx context.Context, method, path string, body, dst any) error {
	var reqBody *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	} else {
		reqBody = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errBody struct {
			Message string `json:"message"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody) //nolint:errcheck
		return fmt.Errorf("github API %s %s: %d %s", method, path, resp.StatusCode, errBody.Message)
	}

	if dst != nil {
		return json.NewDecoder(resp.Body).Decode(dst)
	}
	return nil
}
