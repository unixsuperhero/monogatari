package host

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	story "ofam.dev/monogatari"
)

var updateIndex = flag.Bool("update-index", false, "rewrite testdata/index.golden")

// The index's areas are tables of storyColumns without area: a header per
// section, steps right-aligned, size then steps dropping as the terminal
// narrows, the gap reason cut with "…", the focused row the one highlight.
func TestIndexIsATablePerArea(t *testing.T) {
	keys := []story.Event{{Key: "j"}, {Key: "k"}, {Key: "enter"}}
	lib := story.Library{Apps: testConfig().Apps, Store: story.Store{Dir: t.TempDir()}, Stories: []*story.Story{
		{Name: "j moves down a row", Area: "lists", Width: 40, Height: 6, Script: keys},
		{Name: "a focused empty list stays visible", Area: "lists", Script: keys[:1]},
		{Name: "count spills once at the edge", Area: "motions", Width: 60, Height: 10, Script: keys, Skip: "gap: the count is spent on the first stop instead of the spill"},
	}}
	h, err := newHost(lib, "rows-story")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.idx.Runtime.Settings().Set("index.preview", false); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, width := range []int{100, 64, 40} {
		h.Update(tea.WindowSizeMsg{Width: width, Height: 16})
		fmt.Fprintf(&out, "-- width %d --\n", width)
		for _, line := range strings.Split(ansi.Strip(h.View().Content), "\n") {
			out.WriteString(strings.TrimRight(line, " ") + "\n")
		}
	}
	got := out.String()
	golden := filepath.Join("testdata", "index.golden")
	if *updateIndex {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("index differs from %s (go test ./host -run TestIndexIsATablePerArea -update-index):\n%s", golden, got)
	}
	// The focused area's row is the one full-strength highlight; the other
	// area's table keeps its selected row, dimmed (R5.1).
	full, dimmed := 0, 0
	for _, line := range strings.Split(story.Golden(h.View().Content), "\n") {
		switch {
		case strings.HasPrefix(line, "R|"):
			full++
		case strings.HasPrefix(line, "r|"):
			dimmed++
		}
	}
	if full != 1 || dimmed != 1 {
		t.Fatalf("%d highlighted and %d dimmed rows, want the focused one and the other area's", full, dimmed)
	}
}

// PROG open NAME starts the host with that story open (startStory),
// a base story resolved like any other; q goes back to the index.
func TestOpenStartsOnTheStory(t *testing.T) {
	stories, err := story.LoadDir(filepath.Join("testdata", "stories"))
	if err != nil {
		t.Fatal(err)
	}
	lib := story.Library{Apps: testConfig().Apps, Store: story.Store{Dir: t.TempDir()}, Stories: stories}
	h, err := newHost(lib, "rows-story")
	if err != nil {
		t.Fatal(err)
	}
	s := story.Find(stories, "motions/climb-on")
	if s == nil || s.Base == "" {
		t.Fatal("motions/climb-on (a story with a base) not found")
	}
	h.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	_, cmd := h.Update(startStory{s})
	cmd = settlePlayerCommand(t, h, cmd)
	if h.play == nil || !h.play.auto || cmd == nil {
		t.Fatalf("no story open: %s", h.idx.Status)
	}
	t.Cleanup(h.play.Close)
	if view := ansi.Strip(h.View().Content); !strings.Contains(view, "b3") || h.play.d.S.R.Focus != "a2" {
		t.Fatalf("the story is not open after its base (focus %q):\n%s", h.play.d.S.R.Focus, view)
	}
	h.Update(nextPlayTick(t, cmd))
	if h.play.at != 1 {
		t.Fatal("story open did not advance automatically")
	}
}
