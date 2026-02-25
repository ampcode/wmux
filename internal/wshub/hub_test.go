package wshub

import (
	"bytes"
	"testing"
)

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

func TestSplitUTF8AtSafeBoundaryKeepsTrailingPartialRune(t *testing.T) {
	// U+2500 BOX DRAWINGS LIGHT HORIZONTAL (e2 94 80), split after first byte.
	partial := []byte{0xe2}
	out, carry := splitUTF8AtSafeBoundary(partial)
	if len(out) != 0 {
		t.Fatalf("expected no decoded bytes, got %q", string(out))
	}
	if !bytes.Equal(carry, partial) {
		t.Fatalf("carry mismatch: got=%v want=%v", carry, partial)
	}

	completed, rem := splitUTF8AtSafeBoundary(append(carry, []byte{0x94, 0x80}...))
	if rem != nil {
		t.Fatalf("expected empty carry, got %v", rem)
	}
	if got := string(completed); got != "─" {
		t.Fatalf("decoded rune mismatch: got=%q want=%q", got, "─")
	}
}

func TestDecodePaneOutputDataCarriesAcrossChunks(t *testing.T) {
	h := &Hub{outputUTF8Carry: map[string][]byte{}}

	part1 := h.decodePaneOutputData("%1", "\\342")
	if part1 != "" {
		t.Fatalf("expected first chunk to be buffered, got %q", part1)
	}
	part2 := h.decodePaneOutputData("%1", "\\224\\200")
	if part2 != "─" {
		t.Fatalf("decoded chunk mismatch: got=%q want=%q", part2, "─")
	}
}
