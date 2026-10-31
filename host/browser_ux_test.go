package host

import (
	"context"
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

func reviewBrowserApp() story.App {
	a := browserApp()
	a.Fixtures = append(a.Fixtures, story.Fixture{Name: "review", Build: func(env story.Env) (story.Built, error) {
		var seed struct {
			Focus string `json:"focus"`
			Cold  bool   `json:"cold"`
		}
		if err := json.Unmarshal(env.Seed, &seed); err != nil {
			return story.Built{}, err
		}
		st, err := story.LoadDir(filepath.Join("testdata", "browser-fixtures"))
		if err != nil {
			return story.Built{}, err
		}
		app := rowsApp()
		// The reviewer's slow and panicking fixtures, with the same delays.
		app.Fixtures = append(app.Fixtures,
			story.Fixture{Name: "slow", Build: func(env story.Env) (story.Built, error) { time.Sleep(1500 * time.Millisecond); return buildRows(env) }},
			story.Fixture{Name: "boom", Build: func(story.Env) (story.Built, error) { panic("fixture exploded") }},
		)
		h, err := newHost(story.Library{Apps: []story.App{app}, Stories: st}, "review-browser")
		if err != nil {
			return story.Built{}, err
		}
		h.idx.Runtime.Config().Disable()
		focused := story.Find(st, seed.Focus)
		if focused == nil {
			return story.Built{}, fmt.Errorf("no review story %q", seed.Focus)
		}
		for i, s := range st {
			if s != focused {
				continue
			}
			h.idx.Runtime.SetFocus(fmt.Sprint("s", i))
			if !seed.Cold {
				p, err := protectedPlayer(h.lib, s)
				pv := storyPreview{player: p, err: err}
				if p != nil {
					pv.frame = p.d.S.Frame()
					p.Close()
					p.d = nil
				}
				h.previews[i] = pv
			}
		}
		return story.Built{Model: h.idx, Program: h, Close: h.close}, nil
	}})
	return a
}

func TestBrowserUXCheckDefaultBindings(t *testing.T) {
	h := browserHost(t)
	if problems := h.idx.Runtime.CheckDefaultBindings("monogatari/host/bindings.json", hostBindings); len(problems) > 0 {
		t.Fatal(problems)
	}
}

// Review fixtures are copied from the reviewer's archive, including the
// invalid app/fixture/seed, 120-step script, forty rows and slow fixtures.
func reviewLibrary(t *testing.T) story.Library {
	t.Helper()
	lib := browserHost(t).lib
	st, err := story.LoadDir(filepath.Join("testdata", "browser-fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	lib.Stories = st
	return lib
}

func TestBrowserUXAreaBoundaryScroll(t *testing.T) {
	lib := reviewLibrary(t)
	h, err := newHost(lib, "review")
	if err != nil {
		t.Fatal(err)
	}
	h.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	// A small preceding area is already visible above a much taller area.
	h.idx.Runtime.SetFocus("s3")
	screen(h)
	before := h.indexTop
	keytest.Press(h, "j")
	screen(h)
	if delta := h.indexTop - before; delta > 4 || delta < 0 {
		t.Fatalf("one area spill scrolled %d rows", delta)
	}
	if !strings.Contains(ansi.Strip(h.renderStoryList(h.idx.Runtime, 45, 35, true)), lib.Stories[0].Name[:8]) {
		t.Fatal("visible preceding area was unnecessarily scrolled away")
	}
}

func TestBrowserUXEnterBrokenStory(t *testing.T) {
	h := browserHost(t)
	h.lib.Stories[0].Fixture = "nope"
	before := ansi.Strip(h.renderStoryList(h.idx.Runtime, 45, 26, true))
	runBrowserCommand(t, h, keytest.Press(h, "enter"))
	if !strings.Contains(screen(h), "Error:") || !strings.Contains(h.idx.Status, "nope") {
		t.Fatalf("no error status: %s", screen(h))
	}
	if got := ansi.Strip(h.renderStoryList(h.idx.Runtime, 45, 26, true)); got != before {
		t.Fatalf("error moved list:\n%s", got)
	}
	keytest.Press(h, "j")
	if h.idx.Status != "" {
		t.Fatal("error survived next key")
	}
	h.last = strings.Repeat("/recordings", 8)
	h.saveAs("")
	if !strings.Contains(h.idx.Info, "kept in history") {
		t.Fatal("recording result is not on status line")
	}
}

func TestBrowserUXPreviewOffKeepsFocusVisible(t *testing.T) {
	h, err := newHost(reviewLibrary(t), "review")
	if err != nil {
		t.Fatal(err)
	}
	h.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	h.idx.Runtime.SetFocus("s43")
	screen(h)
	h.idx.Runtime.Settings().Set("index.preview", false)
	view := screen(h)
	if !strings.Contains(view, "many story 39") {
		t.Fatalf("first single-list frame hid focus:\n%s", view)
	}
}

func TestBrowserUXEnterSlowFixture(t *testing.T) {
	h := browserHost(t)
	release, started, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	h.lib.Apps[0].Fixtures[1].Build = func(env story.Env) (story.Built, error) {
		close(started)
		<-release
		built, err := buildRows(env)
		built.Close = func() { close(closed) }
		return built, err
	}
	done := make(chan tea.Cmd, 1)
	go func() { done <- keytest.Press(h, "enter") }()
	var cmd tea.Cmd
	select {
	case cmd = <-done:
	case <-time.After(time.Second):
		close(release)
		released = true
		<-done
		t.Fatal("fixture blocked the update loop")
	}
	result := make(chan ochazuke.Completion, 1)
	go func() { completion, _ := browserCompletion(cmd); result <- completion }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fixture command did not start")
	}
	if !strings.Contains(screen(h), "Starting…") || h.play != nil {
		t.Fatal("no Starting indicator")
	}
	keytest.Press(h, "j")
	if h.focusedStory() != 1 {
		t.Fatal("motion did not work during startup")
	}
	keytest.Press(h, "esc")
	if h.starting != nil || strings.Contains(screen(h), "Starting…") {
		t.Fatal("escape did not cancel startup")
	}
	select {
	case completion := <-result:
		h.Update(completion)
	case <-time.After(time.Second):
		t.Fatal("cancelled player command still waits on the fixture")
	}
	close(release)
	released = true
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancelled startup leaked its late session")
	}
}

// Collect a finite startup command's completion without updating the host.
// This lets tests hold delivery until after cancellation or further motion.
func browserCompletion(cmd tea.Cmd) (ochazuke.Completion, bool) {
	if cmd == nil {
		return ochazuke.Completion{}, false
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, one := range msg {
			if done, ok := browserCompletion(one); ok {
				return done, true
			}
		}
	case ochazuke.Completion:
		return msg, true
	}
	return ochazuke.Completion{}, false
}

func TestBrowserUXFixturePanicPreview(t *testing.T) {
	h := browserHost(t)
	h.previews = map[int]storyPreview{}
	h.lib.Stories[0].Files = []string{"panic-fixture.txt"}
	var filesDir string
	h.lib.Apps[0].Fixtures[1].Build = func(env story.Env) (story.Built, error) { filesDir = env.FilesDir; panic("fixture exploded") }
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("fixture panic escaped: %v", p)
		}
	}()
	runBrowserCommand(t, h, h.previewCommand())
	if !strings.Contains(ansi.Strip(h.renderPreview(50, 26)), "fixture exploded") {
		t.Fatal("panic was not shown as preview error")
	}
	if _, err := os.Stat(filesDir); !os.IsNotExist(err) {
		t.Fatal("panic leaked fixture files", filesDir, err)
	}
}

func TestBrowserUXPreviewTimeoutAndLateCleanup(t *testing.T) {
	h := browserHost(t)
	h.previews = map[int]storyPreview{}
	started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h.lib.Apps[0].Fixtures[1].Build = func(env story.Env) (story.Built, error) {
		close(started)
		<-release
		built, err := buildRows(env)
		built.Close = func() { close(closed) }
		return built, err
	}
	h.lib.Stories[1].Fixture = "plain"
	h.idx.Runtime.Settings().Set("story.build-timeout", "50ms")
	done := make(chan tea.Msg, 1)
	cmd := h.previewCommand()
	go func() { done <- cmd() }()
	<-started
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("preview never timed out")
	}
	h.Update(msg)
	if h.previewBusy || h.previews[0].err == nil || !strings.Contains(h.previews[0].err.Error(), "longer than 50ms") {
		close(release)
		t.Fatal("timeout did not leave a usable preview slot")
	}
	h.idx.Runtime.Settings().Set("story.build-timeout", "5s")
	runBrowserCommand(t, h, keytest.Press(h, "j"))
	if h.previews[1].player == nil {
		close(release)
		t.Fatal("timed out build blocked the next story")
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("late session was not closed")
	}
}

func TestBrowserUXStalePlayerCompletionClosesSession(t *testing.T) {
	h := browserHost(t)
	closed := make(chan struct{})
	h.lib.Apps[0].Fixtures[1].Build = func(env story.Env) (story.Built, error) {
		built, err := buildRows(env)
		built.Close = func() { close(closed) }
		return built, err
	}
	cmd := keytest.Press(h, "enter")
	// Finish construction, but hold the Completion until after cancellation.
	completion, ok := browserCompletion(cmd)
	if !ok {
		t.Fatal("startup returned no completion")
	}
	// Cancellation uses the runtime's ticket, and a late result is disposed
	// by the host before the framework drops the stale completion.
	keytest.Press(h, "esc")
	h.Update(completion)
	if h.play != nil || h.starting != nil || h.idx.Runtime.Accepts(completion.Ticket) {
		t.Fatal("cancelled result opened a player")
	}
	select {
	case <-closed:
	default:
		t.Fatal("late player session was not closed")
	}
}

func TestBrowserUXCancelledBuildNeverStarts(t *testing.T) {
	h := browserHost(t)
	started := false
	h.lib.Apps[0].Fixtures[1].Build = func(env story.Env) (story.Built, error) { started = true; return buildRows(env) }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := buildPlayer(ctx, h.lib, h.lib.Stories[0], time.Second)
	if err != context.Canceled || started {
		t.Fatal(err, started)
	}
}

func TestBrowserUXPreviewFrameDimmed(t *testing.T) {
	h := browserHost(t)
	frame := h.renderPreview(60, 28)
	for _, row := range strings.Split(story.Golden(frame), "\n") {
		if strings.Contains(row, "e1") && strings.HasPrefix(row, "R|") {
			t.Fatal("preview cursor is full strength")
		}
	}
	if !strings.Contains(frame, "│") {
		t.Fatal("no preview frame")
	}
}

func TestBrowserUXPreviewOffEnterHint(t *testing.T) {
	h := browserHost(t)
	h.idx.Runtime.Settings().Set("index.preview", false)
	hints := ochazuke.BindingHints(h.idx.Runtime, h.idx.Runtime.Focus)
	if !strings.Contains(hints, "enter entry.open") || strings.Contains(hints, "play-here") {
		t.Fatal(hints)
	}
	keytest.Press(h, "?")
	if view := screen(h); strings.Contains(view, "play-here") || !strings.Contains(view, "entry.open") {
		t.Fatal(view)
	}
}

func TestBrowserUXFullsizeEscReturnsToPane(t *testing.T) {
	h := browserHost(t)
	runBrowserCommand(t, h, keytest.Press(h, "enter"))
	keytest.Press(h, "space")
	keytest.Press(h, "l")
	p := h.play
	keytest.Press(h, "p")
	keytest.Press(h, "esc")
	if h.play != p || !h.embedded || p.at != 1 {
		t.Fatal("escape lost pane position")
	}
	keytest.Press(h, "p")
	keytest.Press(h, "p")
	if h.play != p || !h.embedded {
		t.Fatal("p does not toggle full size")
	}
	keytest.Press(h, "esc")
	if h.play != nil || h.idx.Runtime.Focus != "s0" {
		t.Fatal("second escape did not return to list")
	}
}

func TestBrowserUXFullsizeDirectToggle(t *testing.T) {
	h := browserHost(t)
	settlePlayerCommand(t, h, keytest.Press(h, "p"))
	p := h.play
	keytest.Press(h, "p")
	if h.play != p || !h.embedded {
		t.Fatal("full-size p advertised a toggle but did nothing")
	}
	keytest.Press(h, "p")
	if h.embedded || h.play != p {
		t.Fatal("pane p lost the player")
	}
	h.idx.Runtime.Settings().Set("index.preview", false)
	if hints := ochazuke.BindingHints(h.idx.Runtime, h.idx.Runtime.Focus); strings.Contains(hints, "play.fullsize") {
		t.Fatal("zoom hint shown with no pane", hints)
	}
}

func TestBrowserUXPreviewBrokenFixture(t *testing.T) {
	for _, ref := range []string{"broken/bad-fixture", "broken/bad-app", "broken/bad-seed"} {
		t.Run(ref, func(t *testing.T) {
			lib := reviewLibrary(t)
			s := story.Find(lib.Stories, ref)
			h, err := newHost(lib, "review")
			if err != nil {
				t.Fatal(err)
			}
			for i, st := range lib.Stories {
				if st == s {
					h.idx.Runtime.SetFocus(fmt.Sprint("s", i))
				}
			}
			runBrowserCommand(t, h, h.previewCommand())
			view := ansi.Strip(h.renderPreview(32, 35))
			want := s.Fixture
			if s.App != "" {
				want = s.App
			}
			if !strings.Contains(view, want) || !strings.Contains(view, "step 0/1") || !strings.Contains(view, "j") {
				t.Fatal(view)
			}
			if ref == "broken/bad-seed" && (!strings.Contains(view, "seed.lists") || strings.Contains(view, "Go struct")) {
				t.Fatal(view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > 32 {
					t.Fatalf("error is clipped instead of wrapped: %s", view)
				}
			}
		})
	}
}

func TestBrowserUXRapidMotionSlowPreview(t *testing.T) {
	h := browserHost(t)
	h.previews = map[int]storyPreview{}
	started := 0
	h.lib.Apps[0].Fixtures[1].Build = func(env story.Env) (story.Built, error) { started++; return buildRows(env) }
	old := h.previewCommand()
	next := keytest.Press(h, "j")
	if next == nil {
		t.Fatal("new focus waits for stale preview")
	}
	runBrowserCommand(t, h, next)
	runBrowserCommand(t, h, old)
	if started != 1 {
		t.Fatalf("built %d fixtures, want only latest", started)
	}
}

func TestBrowserUXDefaultBindingSources(t *testing.T) {
	h := browserHost(t)
	for _, row := range h.idx.Runtime.DefaultBindings() {
		if (row.Noun == "entry" || row.Noun == "play") && row.Source == ochazuke.SourceGo {
			t.Fatalf("Go default: %+v", row)
		}
	}
}

func TestBrowserUXPaneGutter(t *testing.T) {
	h := browserHost(t)
	if !strings.Contains(screen(h), "│ First story") {
		t.Fatal("no divider between area count and preview title")
	}
}
