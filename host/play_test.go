package host

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
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
	keytest.Press(h, "p")
	if h.play == nil || h.idx.Runtime.Page != "play" {
		t.Fatalf("p opened no player (page %q, msg %q)", h.idx.Runtime.Page, h.msg)
	}
	return h, lib
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
	for _, want := range []string{"step 0/2", "paused", "j", "end", "focus=b1, focus visible", "spill-j-at-the-last-row", "a1"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("play page lacks %q:\n%s", want, frame)
		}
	}
	keytest.Press(h, "space")
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
	if p.at != 0 || p.d.S.R.Focus != "a1" {
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
	keytest.Press(h, "a")
	if !p.auto {
		t.Fatal("a did not start autoplay")
	}
	if !strings.Contains(screen(h), "auto") {
		t.Fatalf("status lacks auto:\n%s", screen(h))
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

func TestPlayRecordsLiveKeysFromAStep(t *testing.T) {
	h, lib := playHost(t)
	p := h.play
	keytest.Press(h, "space")
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
