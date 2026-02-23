package wshub

import "testing"

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
