package tmuxparse

import "testing"

func TestStreamParserEvents(t *testing.T) {
	sp := NewStreamParser(8)

	sp.FeedLine("%begin 10 20 0")
	sp.FeedLine("line one")
	sp.FeedLine("line two")
	sp.FeedLine("%end 10 20 0")
	sp.FeedLine("%sessions-changed")
	sp.FeedLine("plain text outside")
	sp.Close()

	var (
		commands      []Command
		notifications []Notification
		parseErrors   []ParseError
	)

	for ev := range sp.Events() {
		switch x := ev.(type) {
		case Command:
			commands = append(commands, x)
		case Notification:
			notifications = append(notifications, x)
		case ParseError:
			parseErrors = append(parseErrors, x)
		default:
			t.Fatalf("unexpected event type %T", ev)
		}
	}

	if len(commands) != 1 {
		t.Fatalf("expected one command event, got %d", len(commands))
	}
	if !commands[0].Success || len(commands[0].Output) != 2 {
		t.Fatalf("unexpected command event: %#v", commands[0])
	}
	if len(notifications) != 1 || notifications[0].Name != "sessions-changed" {
		t.Fatalf("unexpected notifications: %#v", notifications)
	}
	if len(parseErrors) != 1 {
		t.Fatalf("expected one parse error, got %d (%#v)", len(parseErrors), parseErrors)
	}
}
