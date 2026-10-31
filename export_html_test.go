package monogatari

import (
	"strings"
	"testing"
)

func TestFrameHTML(t *testing.T) {
	frame := "\x1b[38;2;12;34;56;1m<&界é\x1b[0m\n\x1b[7;4mhi\x1b[0m"
	got := FrameHTML(frame)
	for _, want := range []string{"color:#0c2238", "font-weight:bold", "&lt;&amp;界é", "text-decoration-line:underline", "background:var(--frame-fg,#cdd6f4)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Count(got, "<span") != 2 || strings.ContainsRune(got, '\x1b') {
		t.Fatalf("unmerged runs or ANSI: %s", got)
	}
	if got := FrameHTML("plain  text"); got != "plain  text" {
		t.Fatal(got)
	}
}
