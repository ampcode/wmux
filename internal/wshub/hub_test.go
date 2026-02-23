package wshub

import "testing"

func TestFilterStateToTargetSession(t *testing.T) {
	state := statePayload{
		Windows: []windowPayload{
			{ID: "@1", Index: 0, Name: "dev"},
			{ID: "@2", Index: 1, Name: "ops"},
		},
		Panes: []panePayload{
			{ID: "%1", SessionName: "dev", WindowID: "@1", PaneIndex: 0},
			{ID: "%2", SessionName: "ops", WindowID: "@2", PaneIndex: 0},
		},
	}

	got := filterStateToTargetSession(state, "dev")
	if len(got.Panes) != 1 || got.Panes[0].ID != "%1" {
		t.Fatalf("unexpected filtered panes: %#v", got.Panes)
	}
	if len(got.Windows) != 1 || got.Windows[0].ID != "@1" {
		t.Fatalf("unexpected filtered windows: %#v", got.Windows)
	}
}

func TestFilterStateToTargetSessionNoTargetReturnsUnchanged(t *testing.T) {
	state := statePayload{
		Windows: []windowPayload{{ID: "@1", Index: 0, Name: "dev"}},
		Panes:   []panePayload{{ID: "%1", SessionName: "dev", WindowID: "@1", PaneIndex: 0}},
	}

	got := filterStateToTargetSession(state, "")
	if len(got.Panes) != 1 || got.Panes[0].ID != "%1" {
		t.Fatalf("unexpected panes: %#v", got.Panes)
	}
	if len(got.Windows) != 1 || got.Windows[0].ID != "@1" {
		t.Fatalf("unexpected windows: %#v", got.Windows)
	}
}

func TestEncodeArgvCommand(t *testing.T) {
	line, err := encodeArgvCommand([]string{"send-keys", "-t", "%1", "-l", "hello world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "send-keys -t %1 -l 'hello world'"
	if line != want {
		t.Fatalf("encoded line mismatch: got=%q want=%q", line, want)
	}
}

func TestEncodeArgvCommandEscapesSingleQuotes(t *testing.T) {
	line, err := encodeArgvCommand([]string{"send-keys", "-t", "%1", "-l", "a'b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "send-keys -t %1 -l 'a'\\''b'"
	if line != want {
		t.Fatalf("encoded line mismatch: got=%q want=%q", line, want)
	}
}

func TestEncodeArgvCommandRejectsEmpty(t *testing.T) {
	if _, err := encodeArgvCommand(nil); err == nil {
		t.Fatalf("expected error for empty argv")
	}
}

func TestParsePaneCursorOutput(t *testing.T) {
	c, ok := parsePaneCursorOutput([]string{"__WMUX_CURSOR\t12\t7"})
	if !ok {
		t.Fatalf("expected cursor parse success")
	}
	if c.X != 12 || c.Y != 7 {
		t.Fatalf("unexpected cursor values: %#v", c)
	}
}
