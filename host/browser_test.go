package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/keytest"
)

func browserHost(t *testing.T) *host {
	t.Helper()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, t.TempDir())
	}
	lib := story.Library{Apps: []story.App{rowsApp()}, Store: story.Store{Dir: t.TempDir()}, Stories: []*story.Story{
		{Name: "First story", Area: "lists", Fixture: "empty", Width: 40, Height: 8, Script: []story.Event{{Key: "j"}}},
		{Name: "Second story", Area: "lists", Fixture: "empty", Width: 40, Height: 8, Script: []story.Event{{Key: "k"}}},
	}}
	h, err := newHost(lib, "browser-test")
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := h.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	runBrowserCommand(t, h, cmd)
	t.Cleanup(func() {
		if h.play != nil {
			h.play.Close()
		}
	})
	return h
}

func TestBrowserSplitDefaultAndFocus(t *testing.T) {
	h := browserHost(t)
	t.Log("first focus:\n" + screen(h))
	if view := screen(h); !strings.Contains(view, "fixture: empty") || !strings.Contains(view, "First story") {
		t.Fatalf("no first preview:\n%s", view)
	}
	runBrowserCommand(t, h, keytest.Press(h, "j"))
	t.Log("second focus:\n" + screen(h))
	if h.idx.Runtime.Focus != "s1" {
		t.Fatal(h.idx.Runtime.Focus)
	}
	if view := ansi.Strip(h.renderPreview(60, 26)); !strings.HasPrefix(view, "Second story") || !strings.Contains(view, "fixture: empty") {
		t.Fatalf("no second preview:\n%s", view)
	}
}

func TestBrowserEnterAndReturn(t *testing.T) {
	h := browserHost(t)
	keytest.Press(h, "j")
	keytest.Press(h, "enter")
	if h.play == nil || h.idx.Runtime.Page != "index" {
		t.Fatalf("enter did not play in index: player=%v page=%s", h.play, h.idx.Runtime.Page)
	}
	keytest.Press(h, "space")
	if h.play.at != 1 {
		t.Fatal("did not step")
	}
	keytest.Press(h, "esc")
	if h.play != nil || h.idx.Runtime.Focus != "s1" {
		t.Fatalf("return lost focus: %s", h.idx.Runtime.Focus)
	}
}

// Window/key commands here contain only finite preview work, not Init's polls.
func runBrowserCommand(t *testing.T, h *host, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, one := range msg {
			runBrowserCommand(t, h, one)
		}
	case previewReady:
		_, next := h.Update(msg)
		runBrowserCommand(t, h, next)
	default:
		t.Fatalf("unexpected browser command message %T", msg)
	}
}

func TestBrowserPreviewCacheAndLatestFocus(t *testing.T) {
	h := browserHost(t)
	first := h.previews[0].player
	if first == nil || !strings.Contains(screen(h), "e1") {
		t.Fatal("no cached first frame")
	}
	runBrowserCommand(t, h, keytest.Press(h, "j"))
	second := h.previews[1].player
	if second == nil || first == second {
		t.Fatal("second story not built")
	}
	runBrowserCommand(t, h, keytest.Press(h, "k"))
	if h.previews[0].player != first || h.previewBusy {
		t.Fatal("cache hit rebuilt preview")
	}
	if first.d != nil || second.d != nil {
		t.Fatal("preview retained a session")
	}
	// A command is pending while focus moves. The old result fills only its
	// cache entry; the next command follows the latest focus, not an old queue.
	h.previews = map[int]storyPreview{}
	cmd := h.previewCommand()
	keytest.Press(h, "j")
	keytest.Press(h, "k")
	keytest.Press(h, "j")
	old := cmd().(previewReady)
	_, next := h.Update(old)
	if next == nil || h.focusedStory() != 1 || !strings.Contains(screen(h), "Loading preview") {
		t.Fatal("stale build replaced current preview")
	}
	runBrowserCommand(t, h, next)
	if h.previews[1].player == nil {
		t.Fatal("latest focus never built")
	}
}

func TestBrowserPreviewBuildDoesNotBlockMotion(t *testing.T) {
	h := browserHost(t)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan tea.Msg, 1)
	h.previews = map[int]storyPreview{}
	h.lib.Apps[0].Fixtures[1].Build = func(env story.Env) (story.Built, error) { close(started); <-release; return buildRows(env) }
	cmd := h.previewCommand()
	go func() { done <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	keytest.Press(h, "j")
	if h.focusedStory() != 1 || !strings.Contains(screen(h), "Loading preview") {
		t.Fatal("focus motion blocked")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestBrowserOptOutAndFullSize(t *testing.T) {
	h := browserHost(t)
	if err := h.idx.Runtime.Settings().Set("index.preview", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(screen(h), "fixture:") || h.previewCommand() != nil {
		t.Fatal("opt-out kept split")
	}
	keytest.Press(h, "enter")
	if h.cur == nil {
		t.Fatal("legacy enter did not open live")
	}
	h.cur.Close()
	h.cur = nil
	if err := h.idx.Runtime.Settings().Set("index.preview", true); err != nil {
		t.Fatal(err)
	}
	keytest.Press(h, "j")
	keytest.Press(h, "enter")
	p := h.play
	keytest.Press(h, "space")
	keytest.Press(h, "z")
	if h.embedded || h.idx.Runtime.Page != "play" || h.play != p || p.at != 1 {
		t.Fatal("zoom lost player position")
	}
	keytest.Press(h, "q")
	if h.play != nil || h.idx.Runtime.Focus != "s1" {
		t.Fatal("full-size return lost selection")
	}
	keytest.Press(h, "z")
	if h.play == nil || h.idx.Runtime.Page != "play" {
		t.Fatal("index full-size binding did not play")
	}
}

func TestBrowserSettingIsExposedByHostCLI(t *testing.T) {
	tool, out := testTool(t)
	if err := tool.Main([]string{"settings", "set", "index.preview", "false", "--yes"}); err != nil {
		t.Fatal(err, out.String())
	}
	r := ochazuke.NewRuntime()
	r.Config().Dir = tool.configDir()
	if err := declareIndexSettings(r); err != nil {
		t.Fatal(err)
	}
	if r.Settings().Bool("index.preview") {
		t.Fatal("TUI did not read CLI setting")
	}
}

func browserApp() story.App {
	return story.App{Name: "browser", Fixtures: []story.Fixture{{Name: "index", Build: func(env story.Env) (story.Built, error) {
		st := []*story.Story{
			{Name: "First story", Area: "lists", Fixture: "empty", Width: 40, Height: 8, Script: []story.Event{{Key: "j"}}},
			{Name: "Second story", Area: "lists", Fixture: "empty", Width: 40, Height: 8, Script: []story.Event{{Key: "k"}}},
		}
		h, err := newHost(story.Library{Apps: []story.App{rowsApp()}, Stories: st}, "story-browser")
		if err != nil {
			return story.Built{}, err
		}
		h.idx.Runtime.Config().Disable()
		// Warm both snapshots at fixture construction so story assertions do not
		// depend on asynchronous scheduling. Unit tests cover command delivery.
		for i, s := range st {
			p, err := newPlayer(h.lib, s)
			if err != nil {
				return story.Built{}, err
			}
			h.previews[i] = storyPreview{frame: p.d.S.Frame(), player: p}
			p.Close()
			p.d = nil
		}
		return story.Built{Model: h.idx, Program: h, Close: func() {
			if h.play != nil {
				h.play.Close()
			}
		}}, nil
	}}}}
}

func TestBrowserStories(t *testing.T) {
	Test(t, Config{Name: "browser-story", Apps: []story.App{browserApp()}, Stories: os.DirFS(filepath.Join("testdata", "browser-stories"))})
}

func TestBrowserTutorialPreview(t *testing.T) {
	h := browserHost(t)
	data, err := os.ReadFile("testdata/stories/tutorials/independent-motions.json")
	if err != nil {
		t.Fatal(err)
	}
	var s story.Story
	if err = json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	h.lib.Stories[0] = &s
	delete(h.previews, 0)
	runBrowserCommand(t, h, h.previewCommand())
	h.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	view := screen(h)
	for _, want := range []string{"reset point", "Move down", "Move up", "focus=a3", "focus=a1", "a1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("preview lacks %q:\n%s", want, view)
		}
	}
}

func TestBrowserMotionsKeepFocusVisible(t *testing.T) {
	h := browserHost(t)
	// Enough rows and areas to overflow the pane in both directions.
	lib := h.lib
	lib.Stories = nil
	for area := 0; area < 3; area++ {
		for row := 0; row < 25; row++ {
			lib.Stories = append(lib.Stories, &story.Story{Name: fmt.Sprintf("story %d-%d", area, row), Area: fmt.Sprintf("area%d", area), Fixture: "empty"})
		}
	}
	var err error
	h, err = newHost(lib, "long-browser")
	if err != nil {
		t.Fatal(err)
	}
	h.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	for _, keys := range []string{"G", "j", "ctrl+d", "ctrl+u", "gg", "k"} {
		keytest.Press(h, keys)
		if h.focusedStory() < 0 {
			t.Fatalf("%s lost story focus: %s", keys, h.idx.Runtime.Focus)
		}
		name := lib.Stories[h.focusedStory()].Name
		view := screen(h)
		if !strings.Contains(view, name) {
			t.Fatalf("%s hid focused %s:\n%s", keys, name, view)
		}
	}
	keytest.Press(h, "/")
	keytest.Type(h, "story 0-2")
	keytest.Press(h, "enter")
	if !strings.Contains(screen(h), "story 0-2") {
		t.Fatal("filter lost row")
	}
	if _, err = h.idx.Runtime.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}
