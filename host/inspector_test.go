package host

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/keytest"
)

var updateInspector = flag.Bool("update-inspector", false, "rewrite testdata/inspector.golden")

func TestPlayInspectorShowsYankedPathOutsideFrame(t *testing.T) {
	h, lib := playHost(t)
	t.Cleanup(func() {
		if h.play != nil {
			h.play.Close()
		}
	})
	h.idx.Width, h.idx.Height = 110, 24
	s := story.Find(lib.Stories, "tutorials/copy-path")
	if s == nil {
		t.Fatal("no copy-path tutorial")
	}
	_, start := h.Update(startStory{s})
	settlePlayerCommand(t, h, start)
	keytest.Press(h, "l") // reach the action's reset point, pausing autoplay
	keytest.Press(h, "l") // y
	p := h.play
	if p.at != 1 || p.action != 0 || len(p.failures()) != 0 {
		t.Fatalf("step failed: %v", p.failures())
	}
	previous := p.before.Compact()
	p.poll()
	if p.before.Compact() != previous {
		t.Fatal("idle poll erased step highlighting")
	}
	appFrame := p.d.S.Frame()
	frame := screen(h)
	if !strings.Contains(frame, `Δ register " = ".items[3].owner.id"`) || !strings.Contains(frame, `info: "copied path"`) {
		t.Fatal(frame)
	}
	if strings.Contains(appFrame, "register") || appFrame != p.d.S.Frame() {
		t.Fatal("inspector changed the story frame")
	}
	pathRow, stripRow := -1, -1
	for i, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "│") && strings.Contains(line, ".items[3]") {
			pathRow = i
		}
		if strings.Contains(line, "Δ register") {
			stripRow = i
		}
	}
	if pathRow < 0 || stripRow <= pathRow {
		t.Fatal("strip is not below preview")
	}
	golden := filepath.Join("testdata", "inspector.golden")
	if *updateInspector {
		if err := os.WriteFile(golden, []byte(frame+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != frame+"\n" {
		t.Fatalf("frame differs from %s:\n%s", golden, frame)
	}
	if err := h.idx.Runtime.Settings().Set("play.inspector", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(screen(h), "Δ register") || appFrame != p.d.S.Frame() {
		t.Fatal("opt-out changed the frame or kept the strip")
	}
	if err := h.idx.Runtime.Settings().Set("play.inspector", true); err != nil {
		t.Fatal(err)
	}
	keytest.Press(h, "h")
	if strings.Contains(screen(h), `register " = ".items[3].owner.id"`) {
		t.Fatal("back kept the future register")
	}
	keytest.Press(h, "l")
	if !strings.Contains(screen(h), "Δ register") {
		t.Fatal("replay did not highlight the yank")
	}
	if err := h.idx.Runtime.Settings().Set("play.tree", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ansi.Strip(h.renderPlay(h.idx.Runtime, 106, 12).Text), "Δ register") {
		t.Fatal("captions clipped the state strip")
	}
}

func TestPlayInspectorWritesAndNarrowFrames(t *testing.T) {
	h, _ := playHost(t)
	defer h.play.Close()
	p := h.play
	p.d.S.R.RecordWrittenFile("reshaped.json")
	p.d.S.R.AddInspectField("json.path", func() any { return ".items[3].owner.id" })
	style := ochazuke.NewStyles(*h.idx.Appearance)
	for _, width := range []int{1, 12, 50} {
		lines := p.inspectorStrip(style, width)
		if len(lines) > 3 {
			t.Fatal(lines)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d: %q", width, line)
			}
		}
	}
	line := ansi.Strip(strings.Join(p.inspectorStrip(style, 100), " "))
	if !strings.Contains(line, `wrote "reshaped.json"`) || !strings.Contains(line, `json.path = ".items[3].owner.id"`) {
		t.Fatal(line)
	}
	p.before = p.inspection()
	p.d.S.R.AddInspectField("json.path", nil)
	if !strings.Contains(ansi.Strip(strings.Join(p.inspectorStrip(style, 100), " ")), `Δ cleared "field:json.path"`) {
		t.Fatal("removed field not highlighted")
	}
}

func TestBrowserPreviewInspectorAndToggle(t *testing.T) {
	h := browserHost(t)
	preview := h.previews[h.focusedStory()]
	if preview.player == nil || preview.player.d != nil {
		t.Fatal("expected a cached preview with its session closed")
	}
	want := "mode normal"
	if !strings.Contains(screen(h), want) {
		t.Fatal("cached preview lost its inspector", screen(h))
	}
	keytest.Press(h, "alt+?")
	if h.idx.Runtime.Settings().Bool("play.inspector") || strings.Contains(screen(h), want) {
		t.Fatal("index inspector toggle failed", screen(h))
	}
	settlePlayerCommand(t, h, keytest.Press(h, "enter"))
	keytest.Press(h, "alt+?")
	if !h.idx.Runtime.Settings().Bool("play.inspector") || !strings.Contains(screen(h), want) {
		t.Fatal("pane player inspector toggle failed", screen(h))
	}
	keytest.Press(h, "p")
	keytest.Press(h, "alt+?")
	if h.idx.Runtime.Settings().Bool("play.inspector") || strings.Contains(screen(h), want) {
		t.Fatal("full-size inspector toggle failed", screen(h))
	}
}
