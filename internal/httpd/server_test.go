package httpd

import "testing"

func TestPaneTargetHrefPreservesPaneIDToken(t *testing.T) {
	got := paneTargetHref("%13")
	if got != "/t/%13" {
		t.Fatalf("paneTargetHref(%%13) = %q, want %q", got, "/t/%13")
	}
}
