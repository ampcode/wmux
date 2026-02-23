package tmuxparse

import "testing"

func TestDecodeEscapedValue(t *testing.T) {
	input := "abc\\040def\\012ghi\\\\jkl"
	got := DecodeEscapedValue(input)
	want := "abc def\nghi\\jkl"
	if got != want {
		t.Fatalf("decoded mismatch: got=%q want=%q", got, want)
	}
}
