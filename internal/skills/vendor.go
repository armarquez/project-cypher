package skills

// This file converts the internal Tool type to each LLM vendor's tool-calling
// schema. The Control Plane calls these before opening a worker session.

// --- Gemini (function_declarations) ---

type GeminiProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type GeminiParameters struct {
	Type       string                    `json:"type"`
	Properties map[string]GeminiProperty `json:"properties,omitempty"`
}

type GeminiFunction struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Parameters  GeminiParameters `json:"parameters"`
}

// ToGemini converts tools to Gemini's function_declarations format.
func ToGemini(tools []Tool) []GeminiFunction {
	out := make([]GeminiFunction, len(tools))
	for i, t := range tools {
		props := make(map[string]GeminiProperty, len(t.Parameters))
		for name, p := range t.Parameters {
			props[name] = GeminiProperty{Type: p.Type, Description: p.Description}
		}
		out[i] = GeminiFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  GeminiParameters{Type: "object", Properties: props},
		}
	}
	return out
}

// --- Anthropic (tools array with input_schema) ---

type AnthropicProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type AnthropicInputSchema struct {
	Type       string                       `json:"type"`
	Properties map[string]AnthropicProperty `json:"properties,omitempty"`
	Required   []string                     `json:"required"`
}

type AnthropicTool struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	InputSchema AnthropicInputSchema `json:"input_schema"`
}

// ToAnthropic converts tools to Anthropic's tools array format.
func ToAnthropic(tools []Tool) []AnthropicTool {
	out := make([]AnthropicTool, len(tools))
	for i, t := range tools {
		props := make(map[string]AnthropicProperty, len(t.Parameters))
		for name, p := range t.Parameters {
			props[name] = AnthropicProperty{Type: p.Type, Description: p.Description}
		}
		out[i] = AnthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: AnthropicInputSchema{
				Type:       "object",
				Properties: props,
				Required:   []string{},
			},
		}
	}
	return out
}

// --- OpenAI-compatible (tools array with function wrapper) ---

type OpenAIProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type OpenAIParameters struct {
	Type       string                    `json:"type"`
	Properties map[string]OpenAIProperty `json:"properties,omitempty"`
}

type OpenAIFunction struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Parameters  OpenAIParameters `json:"parameters"`
}

type OpenAITool struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

// ToOpenAI converts tools to the OpenAI-compatible tools format
// (used by Ollama, LiteLLM, and any OpenAI-API-compatible endpoint).
func ToOpenAI(tools []Tool) []OpenAITool {
	out := make([]OpenAITool, len(tools))
	for i, t := range tools {
		props := make(map[string]OpenAIProperty, len(t.Parameters))
		for name, p := range t.Parameters {
			props[name] = OpenAIProperty{Type: p.Type, Description: p.Description}
		}
		out[i] = OpenAITool{
			Type: "function",
			Function: OpenAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  OpenAIParameters{Type: "object", Properties: props},
			},
		}
	}
	return out
}
