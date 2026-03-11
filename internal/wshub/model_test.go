package wshub

import "testing"

func TestModelStateApplyOutputLines(t *testing.T) {
	m := newModelState()
	changed := m.applyOutputLines([]string{
		"__WMUX___win\t@1\t0\teditor",
		"__WMUX___pane\t%1\t@1\t0\t1\t0\t0\t120\t40\tbash\tbash\t0\teditor",
	})
	if !changed {
		t.Fatalf("expected model change")
	}
	s := m.snapshot()
	if len(s.Windows) != 1 || s.Windows[0].ID != "@1" {
		t.Fatalf("unexpected windows snapshot: %#v", s.Windows)
	}
	if len(s.Panes) != 1 || s.Panes[0].ID != "%1" {
		t.Fatalf("unexpected panes snapshot: %#v", s.Panes)
	}
	if s.Panes[0].Name != "bash" || s.Panes[0].Title != "bash" {
		t.Fatalf("unexpected pane name/title: %#v", s.Panes[0])
	}
	if s.Panes[0].WindowIndex != 0 || s.Panes[0].WindowName != "editor" {
		t.Fatalf("unexpected pane window metadata: %#v", s.Panes[0])
	}
}

func TestModelStateApplyOutputLinesUsesCurrentCommandAsPaneName(t *testing.T) {
	m := newModelState()
	changed := m.applyOutputLines([]string{
		"__WMUX___pane\tdev\t%3\t@1\t1\t1\t0\t0\t200\t60\tzsh\tmy-title\t2\tapi",
	})
	if !changed {
		t.Fatalf("expected model change")
	}

	s := m.snapshot()
	if len(s.Panes) != 1 {
		t.Fatalf("unexpected panes snapshot: %#v", s.Panes)
	}
	p := s.Panes[0]
	if p.Name != "zsh" {
		t.Fatalf("unexpected pane name: got=%q want=%q", p.Name, "zsh")
	}
	if p.Title != "my-title" {
		t.Fatalf("unexpected pane title: got=%q want=%q", p.Title, "my-title")
	}
	if p.SessionName != "dev" {
		t.Fatalf("unexpected session name: got=%q want=%q", p.SessionName, "dev")
	}
	if p.WindowIndex != 2 || p.WindowName != "api" {
		t.Fatalf("unexpected window metadata: got=%d/%q", p.WindowIndex, p.WindowName)
	}
}

func TestModelStateApplyOutputLinesReplacesPaneSnapshot(t *testing.T) {
	m := newModelState()
	if changed := m.applyOutputLines([]string{
		"__WMUX___pane\tdev\t%1\t@1\t0\t1\t0\t0\t120\t40\tbash\tbash\t0\tweb",
		"__WMUX___pane\tdev\t%2\t@1\t1\t0\t60\t0\t120\t40\tbash\tbash\t0\tweb",
	}); !changed {
		t.Fatalf("expected initial snapshot change")
	}

	if changed := m.applyOutputLines([]string{
		"__WMUX___pane\tdev\t%1\t@1\t0\t1\t0\t0\t120\t40\tbash\tbash\t0\tweb",
	}); !changed {
		t.Fatalf("expected snapshot replacement change")
	}

	s := m.snapshot()
	if len(s.Panes) != 1 || s.Panes[0].ID != "%1" {
		t.Fatalf("expected stale pane removal, got %#v", s.Panes)
	}
}
