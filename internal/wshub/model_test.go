package wshub

import "testing"

func TestModelStateApplyOutputLines(t *testing.T) {
	m := newModelState()
	changed := m.applyOutputLines([]string{
		"__WMUX___win\t@1\t0\teditor",
		"__WMUX___pane\t%1\t@1\t0\t1\t0\t0\t120\t40\tbash",
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
}
