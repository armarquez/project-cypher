package hitl

import (
	"testing"
)

func TestDetect_NoMarkers(t *testing.T) {
	triggers := Detect("normal worker output with no markers")
	if len(triggers) != 0 {
		t.Errorf("got %d triggers, want 0", len(triggers))
	}
}

func TestDetect_Empty(t *testing.T) {
	triggers := Detect("")
	if len(triggers) != 0 {
		t.Errorf("got %d triggers for empty string, want 0", len(triggers))
	}
}

func TestDetect_AllKinds(t *testing.T) {
	cases := []struct {
		line string
		kind TriggerKind
	}{
		{"[HITL:new-dependency] adding gopkg.in/yaml.v3", TriggerNewDependency},
		{"[HITL:architectural-change] switching to event-driven model", TriggerArchitectural},
		{"[HITL:security] credential stored in plaintext", TriggerSecurity},
	}
	for _, tc := range cases {
		triggers := Detect(tc.line)
		if len(triggers) != 1 {
			t.Errorf("%q: got %d triggers, want 1", tc.line, len(triggers))
			continue
		}
		if triggers[0].Kind != tc.kind {
			t.Errorf("%q: kind = %q, want %q", tc.line, triggers[0].Kind, tc.kind)
		}
	}
}

func TestDetect_ExtractsDetail(t *testing.T) {
	triggers := Detect("[HITL:new-dependency] gopkg.in/yaml.v3 for config parsing")
	if len(triggers) != 1 {
		t.Fatalf("got %d triggers, want 1", len(triggers))
	}
	if triggers[0].Detail != "gopkg.in/yaml.v3 for config parsing" {
		t.Errorf("Detail = %q, unexpected", triggers[0].Detail)
	}
}

func TestDetect_MarkerWithNoDetail(t *testing.T) {
	triggers := Detect("[HITL:security]")
	if len(triggers) != 1 {
		t.Fatalf("got %d triggers, want 1", len(triggers))
	}
	if triggers[0].Detail != "" {
		t.Errorf("Detail = %q, want empty", triggers[0].Detail)
	}
}

func TestDetect_MultipleTriggers(t *testing.T) {
	text := `Starting task
[HITL:new-dependency] adding requests library
Some more output
[HITL:security] writing token to disk`

	triggers := Detect(text)
	if len(triggers) != 2 {
		t.Fatalf("got %d triggers, want 2", len(triggers))
	}
	if triggers[0].Kind != TriggerNewDependency {
		t.Errorf("triggers[0].Kind = %q, want new dependency", triggers[0].Kind)
	}
	if triggers[1].Kind != TriggerSecurity {
		t.Errorf("triggers[1].Kind = %q, want security", triggers[1].Kind)
	}
}

func TestDetect_InvalidMarkerIgnored(t *testing.T) {
	triggers := Detect("[HITL:unknown-kind] something")
	if len(triggers) != 0 {
		t.Errorf("got %d triggers for unknown marker kind, want 0", len(triggers))
	}
}

func TestDetect_MarkerMustBeWellFormed(t *testing.T) {
	cases := []string{
		"HITL:new-dependency",         // no brackets
		"[HITL new-dependency]",       // colon missing
		"[hitl:new-dependency]",       // lowercase prefix
		"prefix [HITL:security] text", // marker not at start — still matches (embedded OK)
	}
	// Only the last case should match (embedded markers are valid).
	triggers := Detect(cases[3])
	if len(triggers) != 1 {
		t.Errorf("embedded marker: got %d triggers, want 1", len(triggers))
	}

	for _, bad := range cases[:3] {
		if got := Detect(bad); len(got) != 0 {
			t.Errorf("%q: got %d triggers, want 0", bad, len(got))
		}
	}
}
