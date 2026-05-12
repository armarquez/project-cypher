package hitl

import (
	"regexp"
	"strings"
)

// TriggerKind names the category of escalation condition.
type TriggerKind string

const (
	TriggerNewDependency TriggerKind = "new external dependency"
	TriggerArchitectural TriggerKind = "architectural change"
	TriggerSecurity      TriggerKind = "security implication"
)

// Trigger represents a single detected escalation condition.
type Trigger struct {
	Kind   TriggerKind
	Detail string // free-text context extracted from the marker line
}

// markerPattern matches lines emitted by workers that signal a HITL condition.
// Format: [HITL:<kind>] <optional detail text>
// Workers are instructed to emit these via their skill bundle system prompt.
var markerPattern = regexp.MustCompile(`\[HITL:(new-dependency|architectural-change|security)\]\s*(.*)`)

var markerToKind = map[string]TriggerKind{
	"new-dependency":       TriggerNewDependency,
	"architectural-change": TriggerArchitectural,
	"security":             TriggerSecurity,
}

// Detect scans text for HITL marker lines and returns all triggers found.
// Multiple triggers may be present in a single block of output.
func Detect(text string) []Trigger {
	var triggers []Trigger
	for _, line := range strings.Split(text, "\n") {
		m := markerPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		triggers = append(triggers, Trigger{
			Kind:   markerToKind[m[1]],
			Detail: strings.TrimSpace(m[2]),
		})
	}
	return triggers
}
