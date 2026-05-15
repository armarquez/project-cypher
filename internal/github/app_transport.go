package github

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// InstallationTransport is an http.RoundTripper that authenticates as a GitHub
// App installation. It generates a JWT, exchanges it for an installation access
// token, and refreshes the token automatically before it expires.
type InstallationTransport struct {
	appID          int64
	installationID int64
	pemData        []byte
	base           http.RoundTripper
	apiBase        string

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// RoundTrip implements http.RoundTripper. It injects a valid installation access
// token into the Authorization header before forwarding the request.
func (t *InstallationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tok, err := t.getToken(req.Context())
	if err != nil {
		return nil, fmt.Errorf("get installation token: %w", err)
	}
	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "token "+tok)
	return t.base.RoundTrip(req2)
}

func (t *InstallationTransport) getToken(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Return cached token if it has more than 1 minute remaining.
	if t.token != "" && time.Now().Add(time.Minute).Before(t.expiresAt) {
		return t.token, nil
	}
	return t.refreshToken(ctx)
}

func (t *InstallationTransport) refreshToken(ctx context.Context) (string, error) {
	jwtToken, err := makeAppJWT(t.appID, t.pemData)
	if err != nil {
		return "", fmt.Errorf("generate App JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", t.apiBase, t.installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return "", fmt.Errorf("fetch installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("GitHub returned %d fetching installation token", resp.StatusCode)
	}

	var body struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	exp, err := time.Parse(time.RFC3339, body.ExpiresAt)
	if err != nil {
		// Default to 1 hour if parsing fails.
		exp = time.Now().Add(time.Hour)
	}
	t.token = body.Token
	t.expiresAt = exp
	return t.token, nil
}

// makeAppJWT creates a GitHub App JWT (RS256, valid 10 minutes) for appID.
func makeAppJWT(appID int64, pemData []byte) (string, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return "", fmt.Errorf("no PEM block found in private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse RSA key: %w", err)
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": fmt.Sprintf("%d", appID),
	})
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signed, nil
}

// NewClientFromApp returns a Client that authenticates as a GitHub App installation.
// pemData is the PEM-encoded RSA private key for the App. The client automatically
// fetches and refreshes installation access tokens.
//
// baseURL overrides the GitHub API base for testing; pass "" for production.
// base is the underlying HTTP client; pass nil to use http.DefaultClient.
func NewClientFromApp(appID, installationID int64, pemData []byte, base *http.Client, baseURL string) (*Client, error) {
	// Validate the PEM key up front so callers get a clear error immediately.
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("invalid GitHub App private key: no PEM block found")
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return nil, fmt.Errorf("invalid GitHub App private key: %w", err)
	}

	baseTransport := http.DefaultTransport
	if base != nil && base.Transport != nil {
		baseTransport = base.Transport
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	transport := &InstallationTransport{
		appID:          appID,
		installationID: installationID,
		pemData:        pemData,
		base:           baseTransport,
		apiBase:        baseURL,
	}
	return &Client{
		baseURL:    baseURL,
		token:      "", // transport injects the installation token
		httpClient: &http.Client{Transport: transport},
	}, nil
}

