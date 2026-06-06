package gateway

import (
	"net/http"
	"os"
)

// CredentialStore holds API keys for each vendor, loaded at orchestrator
// startup. Workers never see these values — the store injects them
// server-side into each outbound request.
type CredentialStore struct {
	keys map[Vendor]string
}

// NewCredentialStore creates a store from a pre-built key map. Use
// LoadCredentials for production; this constructor is for tests.
func NewCredentialStore(keys map[Vendor]string) *CredentialStore {
	return &CredentialStore{keys: keys}
}

// LoadCredentials reads vendor API keys from environment variables using
// bare os.Getenv — values are used as-is without op:// resolution.
// Only use this in tests or environments where credentials are plain strings.
// Production code in cmd/cypher uses loadResolvedCredentials (via secrets.ResolveEnv)
// so that op:// vault references are resolved before injection.
//
//	GEMINI_API_KEY    — Google Generative AI
//	ANTHROPIC_API_KEY — Anthropic
//	OPENAI_API_KEY    — OpenAI-compatible endpoints
func LoadCredentials() *CredentialStore {
	keys := make(map[Vendor]string)
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		keys[VendorGemini] = k
	}
	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		keys[VendorAnthropic] = k
	}
	if k := os.Getenv("OPENAI_API_KEY"); k != "" {
		keys[VendorOpenAI] = k
	}
	return &CredentialStore{keys: keys}
}

// Inject sets the appropriate authentication headers for vendor on h.
// It is a no-op when the store is nil or has no key for the vendor.
func (cs *CredentialStore) Inject(vendor Vendor, h http.Header) {
	if cs == nil {
		return
	}
	key := cs.keys[vendor]

	switch vendor {
	case VendorGemini:
		if key != "" {
			h.Set("Authorization", "Bearer "+key)
		}
	case VendorAnthropic:
		if key != "" {
			h.Set("x-api-key", key)
		}
		// anthropic-version is required by the API regardless of key presence.
		if h.Get("anthropic-version") == "" {
			h.Set("anthropic-version", "2023-06-01")
		}
	case VendorOpenAI, VendorLMStudio:
		if key != "" {
			h.Set("Authorization", "Bearer "+key)
		}
	// VendorOllama: no authentication required.
	}
}
