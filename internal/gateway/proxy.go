package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Vendor identifies an LLM API provider.
type Vendor string

const (
	VendorGemini    Vendor = "gemini"
	VendorAnthropic Vendor = "anthropic"
	VendorOllama    Vendor = "ollama"
	VendorOpenAI    Vendor = "openai"
	// VendorLMStudio routes to an LM Studio server running an OpenAI-compatible
	// API. Use model strings like "lmstudio/qwen2.5-coder:32b". No API key
	// required; configure the endpoint via CYPHER_LMSTUDIO_URL.
	VendorLMStudio Vendor = "lmstudio"
)

// defaultEndpoints maps each vendor to its public API base URL.
// Override at runtime via CYPHER_OLLAMA_URL and CYPHER_LMSTUDIO_URL.
var defaultEndpoints = map[Vendor]string{
	VendorGemini:    "https://generativelanguage.googleapis.com",
	VendorAnthropic: "https://api.anthropic.com",
	VendorOllama:    "http://localhost:11434",
	VendorOpenAI:    "https://api.openai.com",
	VendorLMStudio:  "http://localhost:1234",
}

// hopByHopHeaders are connection-scoped headers that must not be forwarded.
var hopByHopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"TE", "Trailers", "Transfer-Encoding", "Upgrade",
}

// Router proxies inbound LLM requests to the correct vendor endpoint based
// on the model field in the request body.
type Router struct {
	client      *http.Client
	endpoints   map[Vendor]string
	credentials *CredentialStore // nil = no injection
}

// NewRouter creates a Router using the provided HTTP client, optional endpoint
// overrides (for tests), and an optional CredentialStore (nil = no injection).
func NewRouter(client *http.Client, overrides map[Vendor]string, creds *CredentialStore) *Router {
	endpoints := make(map[Vendor]string, len(defaultEndpoints))
	for k, v := range defaultEndpoints {
		endpoints[k] = v
	}
	for k, v := range overrides {
		endpoints[k] = v
	}
	return &Router{client: client, endpoints: endpoints, credentials: creds}
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	vendor, err := parseVendor(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	base, ok := r.endpoints[vendor]
	if !ok {
		http.Error(w, fmt.Sprintf("no endpoint configured for vendor %q", vendor), http.StatusBadGateway)
		return
	}

	target := base + req.URL.RequestURI()
	outbound, err := http.NewRequestWithContext(req.Context(), req.Method, target, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "failed to build upstream request", http.StatusInternalServerError)
		return
	}

	copyHeaders(outbound.Header, req.Header)
	r.credentials.Inject(vendor, outbound.Header)

	resp, err := r.client.Do(outbound)
	if err != nil {
		http.Error(w, fmt.Sprintf("upstream request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}

// parseVendor extracts the vendor prefix from the model field in a JSON body.
// Expects model strings in the form "vendor/model-name" (e.g. "gemini/gemini-2.0-flash").
func parseVendor(body []byte) (Vendor, error) {
	var envelope struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("invalid JSON body: %w", err)
	}
	if envelope.Model == "" {
		return "", fmt.Errorf("request body missing required field: model")
	}

	prefix, _, found := strings.Cut(envelope.Model, "/")
	if !found {
		return "", fmt.Errorf("model %q must be in vendor/name format (e.g. gemini/gemini-2.0-flash)", envelope.Model)
	}

	v := Vendor(strings.ToLower(prefix))
	if _, known := defaultEndpoints[v]; !known {
		return "", fmt.Errorf("unknown vendor %q in model %q", prefix, envelope.Model)
	}
	return v, nil
}

func copyHeaders(dst, src http.Header) {
	hop := make(map[string]bool, len(hopByHopHeaders))
	for _, h := range hopByHopHeaders {
		hop[http.CanonicalHeaderKey(h)] = true
	}
	for k, vals := range src {
		if !hop[k] {
			for _, v := range vals {
				dst.Add(k, v)
			}
		}
	}
}
