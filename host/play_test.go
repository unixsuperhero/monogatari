package host

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/keytest"
)

// playHost opens the index on the spill-down story and enters play mode.
func playHost(t *testing.T) (*host, story.Library) {
	t.Helper()
	tool, _ := testTool(t)
	lib, err := tool.library("")
	if err != nil {
		t.Fatal(err)
	}
	h, err := newHost(lib, "rows-story")
	if err != nil {
		t.Fatal(err)
	}
	h.idx.Width, h.idx.Height = 140, 40
	s := story.Find(lib.Stories, "motions/spill-down")
	if s == nil {
		t.Fatal("no motions/spill-down")
	}
	for i, st := range lib.Stories {
		if st == s {
			h.idx.Runtime.SetFocus("s" + itoa(i))
		}
	}
	cmd := keytest.Press(h, "p")
	if h.play == nil || h.idx.Runtime.Page != "play" {
		t.Fatalf("p opened no player (page %q, msg %q)", h.idx.Runtime.Page, h.msg)
	}
	if !h.play.auto || nextPlayTick(t, cmd).gen != h.play.gen {
		t.Fatal("p did not start the autoplay timer")
	}
	return h, lib
}

// Execute the returned command so startup tests cover scheduling as well as state.
func nextPlayTick(t *testing.T, cmd tea.Cmd) playTick {
	t.Helper()
	if cmd == nil {
		t.Fatal("no autoplay command")
	}
	switch msg := cmd().(type) {
	case playTick:
		return msg
	case tea.BatchMsg:
		for _, one := range msg {
			if one != nil {
				return nextPlayTick(t, one)
			}
		}
	default:
		t.Fatalf("autoplay command returned %T", msg)
	}
	t.Fatal("no autoplay tick in batch")
	return playTick{}
}

func itoa(i int) string { return strconv.Itoa(i) }

func screen(h *host) string { return ansi.Strip(h.View().Content) }

func TestPlayStepsAndChecksTheScript(t *testing.T) {
	h, _ := playHost(t)
	p := h.play
	if testing.Verbose() {
		t.Log("\n" + screen(h))
	}
	if p.at != 0 || len(p.script()) != 2 {
		t.Fatalf("fresh player at %d of %d", p.at, len(p.script()))
	}
	frame := screen(h)
	for _, want := range []string{"step 0/2", "auto", "j", "end", "focus=b1, focus visible", "spill-j-at-the-last-row", "a1"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("play page lacks %q:\n%s", want, frame)
		}
	}
	keytest.Press(h, "space")
	if p.auto || p.at != 0 || !strings.Contains(screen(h), "paused") {
		t.Fatal("space did not pause at the opening frame")
	}
	keytest.Press(h, "l")
	if p.at != 1 || p.d.S.R.Focus != "a2" {
		t.Fatalf("after one step at %d, focus %q", p.at, p.d.S.R.Focus)
	}
	if !strings.Contains(screen(h), "▸ j") || !strings.Contains(screen(h), "list.move") {
		t.Fatalf("the played row is not marked with what it ran:\n%s", screen(h))
	}
	keytest.Press(h, "l")
	if p.at != 2 || p.d.S.R.Focus != "b1" {
		t.Fatalf("after two steps at %d, focus %q", p.at, p.d.S.R.Focus)
	}
	rows := p.rows()
	if last := rows[len(rows)-1]; !last.Final || last.State != rowPassed {
		t.Fatalf("final row %+v", last)
	}
	if !strings.Contains(screen(h), "✓ focus=b1") {
		t.Fatalf("no pass mark:\n%s", screen(h))
	}
	keytest.Press(h, "h")
	if p.at != 1 || p.d.S.R.Focus != "a2" {
		t.Fatalf("back: at %d, focus %q", p.at, p.d.S.R.Focus)
	}
	keytest.Press(h, "r")
	if p.at != 0 || p.auto || p.d.S.R.Focus != "a1" {
		t.Fatalf("restart: at %d, focus %q", p.at, p.d.S.R.Focus)
	}
	keytest.Press(h, "f")
	if !p.done() || p.results[0] == nil {
		t.Fatalf("f did not play to the end (at %d)", p.at)
	}
	keytest.Press(h, "q")
	if h.play != nil || h.idx.Runtime.Page != "index" {
		t.Fatalf("q did not leave to the index (page %q)", h.idx.Runtime.Page)
	}
}

func TestPlayAutoAdvancesOnTicks(t *testing.T) {
	h, _ := playHost(t)
	p := h.play
	keytest.Press(h, "+")
	if !p.auto {
		t.Fatal("player did not start autoplay")
	}
	if !strings.Contains(screen(h), "auto") {
		t.Fatalf("status lacks auto:\n%s", screen(h))
	}
	stale := p.gen
	keytest.Press(h, "space")
	h.Update(playTick{gen: stale})
	if p.auto || p.at != 0 {
		t.Fatal("paused player advanced")
	}
	keytest.Press(h, "space")
	if !p.auto {
		t.Fatal("space did not resume autoplay")
	}
	h.Update(playTick{gen: p.gen})
	if p.at != 1 {
		t.Fatalf("one tick played %d events", p.at)
	}
	h.Update(playTick{gen: p.gen - 1}) // a stale tick
	if p.at != 1 {
		t.Fatalf("a stale tick played an event (at %d)", p.at)
	}
	h.Update(playTick{gen: p.gen})
	if p.at != 2 || p.auto {
		t.Fatalf("at the end at %d, auto %v", p.at, p.auto)
	}
}

func TestPlayNewPlayerIgnoresPreviousTimer(t *testing.T) {
	h, _ := playHost(t)
	t.Cleanup(func() { h.play.Close() })
	keytest.Press(h, "space")
	keytest.Press(h, "space")
	old := playTick{h.play.gen}
	keytest.Press(h, "q")
	keytest.Press(h, "p")
	h.Update(old)
	if h.play.at != 0 || !h.play.auto {
		t.Fatal("previous player's timer advanced the new player")
	}
}

func TestPlayRecordsLiveKeysFromAStep(t *testing.T) {
	h, lib := playHost(t)
	p := h.play
	keytest.Press(h, "l")
	keytest.Press(h, "i")
	if !p.recording || p.at != 1 || len(p.script()) != 1 {
		t.Fatalf("i did not start recording from step 1 (recording %v, at %d, script %d)", p.recording, p.at, len(p.script()))
	}
	if _, final := p.steps[0]; !final {
		t.Fatal("recording dropped the final expect")
	}
	if !strings.Contains(screen(h), "rec") {
		t.Fatalf("status lacks the recording mark:\n%s", screen(h))
	}
	// Keys go to the story and fill the script.
	keytest.Press(h, "j")
	keytest.Press(h, "j")
	if p.at != 3 || len(p.script()) != 3 || p.script()[2].Key != "j" || p.d.S.R.Focus != "b2" {
		t.Fatalf("recorded at %d, script %d, focus %q", p.at, len(p.script()), p.d.S.R.Focus)
	}
	if p.script()[1].T == 0 {
		t.Fatal("a recorded event carries no time")
	}
	if !strings.Contains(screen(h), "step 3/3") {
		t.Fatalf("the column did not fill in:\n%s", screen(h))
	}
	// The play keys are not play keys while recording: space is the story's.
	keytest.Press(h, recordStop)
	if p.recording {
		t.Fatal("ctrl+g did not stop the recording")
	}
	keytest.Press(h, "h")
	if p.at != 2 || p.d.S.R.Focus != "b1" {
		t.Fatalf("back after recording: at %d, focus %q", p.at, p.d.S.R.Focus)
	}
	keytest.Press(h, "s")
	if h.idx.Runtime.TopModal() == "" {
		t.Fatalf("s opened no save prompt (msg %q)", h.msg)
	}
	keytest.Type(h, "walk")
	keytest.Press(h, "enter")
	e, err := lib.Store.Load("walk")
	if err != nil {
		t.Fatal(err, h.msg)
	}
	if len(e.Rec.Events) != 2 || e.Rec.Events[0].Key != "j" || e.Rec.Events[1].Key != "j" {
		t.Fatalf("saved events %+v", e.Rec.Events)
	}
	if res := (story.Library{Apps: testConfig().Apps}).Replay(e.Rec, nil); !res.OK() {
		t.Fatalf("the saved recording does not replay: %v %v", res.Err, res.Failed)
	}
}

func TestPlayTutorialResetsNavigatesAndShowsCaptions(t *testing.T) {
	h, lib := playHost(t)
	t.Cleanup(func() {
		if h.play != nil {
			h.play.Close()
		}
	})
	s := story.Find(lib.Stories, "tutorials/independent-motions")
	h.Update(startStory{s}) // story open uses the player for tutorials
	p := h.play
	if p.action != -1 || !strings.Contains(screen(h), "reset point") {
		t.Fatal("setup not shown")
	}
	if !p.auto {
		t.Fatal("tutorial did not start autoplay")
	}
	h.Update(playTick{p.gen}) // setup j
	if p.d.S.R.Focus != "a2" {
		t.Fatal(p.d.S.R.Focus)
	}
	h.Update(playTick{p.gen}) // reset, show action 1 before its key
	if p.action != 0 || p.at != 0 || !strings.Contains(screen(h), "Press j") {
		t.Fatal("action caption missing")
	}
	h.Update(playTick{p.gen}) // action j
	if p.d.S.R.Focus != "a3" {
		t.Fatal(p.d.S.R.Focus)
	}
	h.Update(playTick{p.gen}) // reset for action 2
	if p.action != 1 || p.at != 0 || p.d.S.R.Focus != "a2" {
		t.Fatalf("no reset: %+v", p)
	}
	h.Update(playTick{p.gen}) // k, from reset a2
	if p.d.S.R.Focus != "a1" || !p.done() || p.auto || len(p.failures()) > 0 {
		t.Fatal("second action failed")
	}
	keytest.Press(h, "[")
	if p.action != 0 || p.auto || p.at != 0 || p.d.S.R.Focus != "a2" {
		t.Fatal("previous action did not reset")
	}
	keytest.Press(h, "]")
	if p.action != 1 || p.auto || p.at != 0 || p.d.S.R.Focus != "a2" {
		t.Fatal("next action did not reset")
	}
	if _, err := h.idx.Runtime.Settings().SetSession("play.captions", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(screen(h), "Press k") {
		t.Fatal("captions cannot be disabled")
	}
	keytest.Press(h, "r")
	if p.action != -1 || p.at != 0 || p.d.S.R.Focus != "a1" {
		t.Fatal("restart did not return to opening")
	}
	keytest.Press(h, "f")
	if !p.done() || p.d.S.R.Focus != "a1" {
		t.Fatal("end did not play all independent actions")
	}
}

func TestPlayTutorialRecordingKeepsSetupAndRelativeChecks(t *testing.T) {
	h, lib := playHost(t)
	s := *story.Find(lib.Stories, "tutorials/independent-motions")
	s.Actions = append([]story.Action(nil), s.Actions...)
	s.Actions[0].Expect = story.Expects{{After: 1, Focus: "a3"}}
	p, err := newPlayer(lib, &s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	t.Cleanup(h.play.Close)
	if err := p.chooseAction(0); err != nil {
		t.Fatal(err)
	}
	p.step()
	rec := p.asRecording()
	if len(rec.Events) != 2 || rec.Expect[0].After != 2 || len(rec.Initial.Actions) != 0 {
		t.Fatalf("bad recording: %+v", rec)
	}
	if res := lib.Replay(rec, nil); !res.OK() {
		t.Fatalf("replay: %v %v", res.Err, res.Failed)
	}
	p.startRecord()
	if err := p.back(); err != nil {
		t.Fatal(err)
	}
	if p.d.S.R.Focus != "a2" {
		t.Fatal("recording lost its setup")
	}
}

func TestPlayTutorialStopsOnBadResetPoint(t *testing.T) {
	h, lib := playHost(t)
	t.Cleanup(h.play.Close)
	s := *story.Find(lib.Stories, "tutorials/independent-motions")
	s.Expect = story.Expects{{Focus: "missing"}}
	p, err := newPlayer(lib, &s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err := p.chooseAction(0); err == nil {
		t.Fatal("invalid reset point was accepted")
	}
	p.step()
	if p.at != 0 || len(p.failures()) == 0 {
		t.Fatal("played action from a bad reset point")
	}
}

func TestPlayTutorialTreeKeepsSetupParentsAndResetCursor(t *testing.T) {
	h, lib := playHost(t)
	t.Cleanup(func() { h.play.Close() })
	h.Update(startStory{story.Find(lib.Stories, "tutorials/tree-resets")})
	p := h.play
	rows, cur := p.tutorialRows()
	if cur != -1 || rows[0].Event != "j" || rows[1].Event != "j" || !rows[2].Reset {
		t.Fatalf("opening tree: cursor %d, rows %+v", cur, rows)
	}
	var parents []string
	for i, row := range rows {
		if row.Parent {
			parents = append(parents, row.Event)
			if row.Detail == "" || rows[i+1].Depth != 1 || rows[i+2].Depth != 1 {
				t.Fatalf("missing caption or child steps: %+v", rows[i:])
			}
		}
	}
	if strings.Join(parents, ",") != "Move up twice,Move down twice" {
		t.Fatal(parents)
	}
	for range 2 {
		h.Update(playTick{p.gen})
	}
	if _, cur = p.tutorialRows(); cur != 2 || p.d.S.R.Focus != "a3" {
		t.Fatal("setup did not end at the reset row")
	}
	h.Update(playTick{p.gen})
	if _, cur = p.tutorialRows(); cur != 2 || p.action != 0 {
		t.Fatal("first action did not visibly reset")
	}
	for want := 4; want <= 5; want++ {
		h.Update(playTick{p.gen})
		if _, cur = p.tutorialRows(); cur != want {
			t.Fatalf("key child cursor %d, want %d", cur, want)
		}
	}
	h.Update(playTick{p.gen})
	if _, cur = p.tutorialRows(); cur != 2 || p.action != 1 || p.d.S.R.Focus != "a3" {
		t.Fatal("second action did not jump back to the same reset row")
	}
	keytest.Press(h, "l")
	if _, cur = p.tutorialRows(); cur != 7 || p.d.S.R.Focus != "a4" {
		t.Fatalf("second action did not walk its children: cursor %d", cur)
	}
	keytest.Press(h, "[")
	if _, cur = p.tutorialRows(); cur != 2 || p.action != 0 {
		t.Fatal("previous action lost reset row")
	}
	keytest.Press(h, "]")
	if _, cur = p.tutorialRows(); cur != 2 || p.action != 1 {
		t.Fatal("next action lost reset row")
	}
	frame := screen(h)
	if !strings.Contains(frame, "▸ ◆ reset point") || !strings.Contains(frame, "▾ Move up twice") || !strings.Contains(frame, "▾ Move down twice") {
		t.Fatalf("not a single visible tutorial tree:\n%s", frame)
	}
	if _, err := h.idx.Runtime.Settings().SetSession("play.captions", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(screen(h), "k k moves") || !strings.Contains(screen(h), "▾ Move up twice") {
		t.Fatal("caption opt-out must keep tree titles")
	}
	if _, err := h.idx.Runtime.Settings().SetSession("play.tree", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(screen(h), "▾ Move up twice") {
		t.Fatal("tree layout cannot be disabled")
	}
	keytest.Press(h, "r")
	if _, cur = p.tutorialRows(); cur != -1 || p.action != -1 {
		t.Fatal("restart did not return to opening")
	}
}

func TestPlayTutorialTreeKeepsParentVisibleForLongActions(t *testing.T) {
	h, lib := playHost(t)
	t.Cleanup(func() { h.play.Close() })
	s := *story.Find(lib.Stories, "tutorials/tree-resets")
	s.Actions = append([]story.Action(nil), s.Actions...)
	s.Actions[0].Keys = make([]story.Event, 30)
	for i := range s.Actions[0].Keys {
		s.Actions[0].Keys[i] = story.Event{Key: "j"}
	}
	s.Actions[0].Expect = nil
	h.Update(startStory{&s})
	p := h.play
	if err := p.chooseAction(0); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		p.step()
	}
	styles := ochazuke.NewStyles(*h.idx.Appearance)
	frame := ansi.Strip(strings.Join(p.treeSidebar(styles, 45, 12, true), "\n"))
	if strings.Count(frame, "▾ Move up twice") != 1 || !strings.Contains(frame, "k k moves") || !strings.Contains(frame, "▸   ├ j") {
		t.Fatalf("long action lost parent, caption or cursor:\n%s", frame)
	}
	if n := len(p.treeSidebar(styles, 45, 12, true)); n > 12 {
		t.Fatalf("sidebar overflowed: %d lines", n)
	}
	if err := p.chooseAction(1); err != nil {
		t.Fatal(err)
	}
	frame = ansi.Strip(strings.Join(p.treeSidebar(styles, 45, 12, true), "\n"))
	if !strings.Contains(frame, "▸ ◆ reset point") {
		t.Fatalf("reset cursor was not brought back into view:\n%s", frame)
	}
}
