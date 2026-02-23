package tmuxparse

import (
	"reflect"
	"testing"
)

func TestParserCommandBlockAndNotification(t *testing.T) {
	var (
		begins []BlockHeader
		lines  []string
		ends   []struct {
			begin   BlockHeader
			end     BlockHeader
			success bool
		}
		notes []Notification
		errs  []ParseError
	)

	p := NewParser(Callbacks{
		OnCommandBegin: func(h BlockHeader) { begins = append(begins, h) },
		OnCommandLine: func(_ BlockHeader, line string) {
			lines = append(lines, line)
		},
		OnCommandEnd: func(begin BlockHeader, end BlockHeader, success bool) {
			ends = append(ends, struct {
				begin   BlockHeader
				end     BlockHeader
				success bool
			}{begin: begin, end: end, success: success})
		},
		OnNotification: func(n Notification) { notes = append(notes, n) },
		OnError:        func(err ParseError) { errs = append(errs, err) },
	})

	p.FeedLine("%begin 1363006971 2 1")
	p.FeedLine("0: zsh* (1 panes)")
	p.FeedLine("%end 1363006971 2 1")
	p.FeedLine("%window-renamed @7 dev shell")

	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %+v", errs)
	}
	if len(begins) != 1 {
		t.Fatalf("expected 1 begin, got %d", len(begins))
	}
	if got, want := lines, []string{"0: zsh* (1 panes)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("output lines mismatch: got=%v want=%v", got, want)
	}
	if len(ends) != 1 || !ends[0].success {
		t.Fatalf("expected successful end, got %#v", ends)
	}
	if len(notes) != 1 || notes[0].Name != "window-renamed" || notes[0].Args[0] != "@7" || notes[0].Text != "dev shell" {
		t.Fatalf("unexpected notification: %#v", notes)
	}
}

func TestParserExtendedOutputAndSubscription(t *testing.T) {
	var notes []Notification
	p := NewParser(Callbacks{OnNotification: func(n Notification) { notes = append(notes, n) }})

	p.FeedLine("%extended-output %1 12 foo bar : hello world")
	p.FeedLine("%subscription-changed sub $1 @2 0 %3 extra : #{pane_current_command}")

	if len(notes) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(notes))
	}
	if got, want := notes[0].Name, "extended-output"; got != want {
		t.Fatalf("unexpected first notification name: %s", got)
	}
	if got, want := notes[0].Args[0], "%1"; got != want {
		t.Fatalf("unexpected pane id in extended-output: %s", got)
	}
	if got, want := notes[0].Value, "hello world"; got != want {
		t.Fatalf("unexpected extended-output value: %q", got)
	}
	if got, want := notes[1].Name, "subscription-changed"; got != want {
		t.Fatalf("unexpected second notification name: %s", got)
	}
	if got, want := notes[1].Value, "#{pane_current_command}"; got != want {
		t.Fatalf("unexpected subscription value: %q", got)
	}
}

func TestParserOutputPreservesLeadingSpaces(t *testing.T) {
	var notes []Notification
	p := NewParser(Callbacks{OnNotification: func(n Notification) { notes = append(notes, n) }})

	p.FeedLine("%output %11  hello")

	if len(notes) != 1 {
		t.Fatalf("expected one notification, got %d", len(notes))
	}
	if got, want := notes[0].Args[0], "%11"; got != want {
		t.Fatalf("unexpected pane id: got=%q want=%q", got, want)
	}
	if got, want := notes[0].Value, " hello"; got != want {
		t.Fatalf("output value mismatch: got=%q want=%q", got, want)
	}
}

func TestParserEndWithoutBeginIsError(t *testing.T) {
	var errs []ParseError
	p := NewParser(Callbacks{OnError: func(err ParseError) { errs = append(errs, err) }})
	p.FeedLine("%end 1 2 3")

	if len(errs) != 1 {
		t.Fatalf("expected one error, got %d", len(errs))
	}
}
