package skills

import (
	"fmt"
	"strings"
)

// AssemblePrompt concatenates the context_pack fragments from the named
// bundles in order. Returns an error if a named bundle is not in the map.
func AssemblePrompt(bundles map[string]*Bundle, names []string) (string, error) {
	var parts []string
	for _, name := range names {
		b, ok := bundles[name]
		if !ok {
			return "", fmt.Errorf("skill bundle %q not found", name)
		}
		if b.ContextPack != "" {
			parts = append(parts, strings.TrimSpace(b.ContextPack))
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// AssembleTools returns the union of tools from the named bundles in order.
// Returns an error if a named bundle is not in the map.
func AssembleTools(bundles map[string]*Bundle, names []string) ([]Tool, error) {
	var tools []Tool
	for _, name := range names {
		b, ok := bundles[name]
		if !ok {
			return nil, fmt.Errorf("skill bundle %q not found", name)
		}
		tools = append(tools, b.Tools...)
	}
	return tools, nil
}
