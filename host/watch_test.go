package host

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/monogatari/explore"
	"github.com/unixsuperhero/ochazuke/keytest"
)

const watchStory = "motions/spill-down"

func watchApps(t *testing.T) []*story.Story {
	t.Helper()
	tool, _ := testTool(t)
	lib, err := tool.library("")
	if err != nil {
		t.Fatal(err)
	}
	apps := storyApps(lib, []string{watchStory})
	if len(apps) != 1 {
		t.Fatalf("%d applications for %s", len(apps), watchStory)
	}
	return apps
}

var elapsed = regexp.MustCompile(`, [0-9.]+(ms|µs|s|m[0-9.]+s)\n`)

// explored is the stdout of rows story explore with args, the elapsed time
// left out (it is the one part that differs between two runs).
func explored(t *testing.T, args ...string) string {
	t.Helper()
	tool, out := testTool(t)
	if err := tool.Explore(args); err != nil {
		t.Fatalf("explore %v: %v\n%s", args, err, out)
	}
	return elapsed.ReplaceAllString(out.String(), "\n")
}

// TestExploreHeadlessIsUnchanged: without --watch the same budget gives
// the same bytes, and --delay alone is refused rather than ignored.
func TestExploreHeadlessIsUnchanged(t *testing.T) {
	args := []string{"--depth", "2", "--steps", "60", watchStory}
	a, b := explored(t, args...), explored(t, args...)
	if a != b {
		t.Fatalf("two headless runs differ:\n%s\n---\n%s", a, b)
	}
	if !strings.Contains(a, "explore "+watchStory+": depth 2, steps 60/60") {
		t.Fatalf("unexpected report:\n%s", a)
	}
	tool, _ := testTool(t)
	if err := tool.Explore(append([]string{"--delay", "1s"}, args...)); err == nil || !strings.Contains(err.Error(), "--delay needs --watch") {
		t.Fatalf("--delay without --watch: %v", err)
	}
}

// TestWatchRunGivesTheHeadlessReport: a watched run, held by the pacer
// at every step, reports exactly what the headless run does.
func TestWatchRunGivesTheHeadlessReport(t *testing.T) {
	apps := watchApps(t)
	budget := explore.Budget{Depth: 2, Steps: 60}
	headless := explore.Run(story.Library{Apps: testConfig().Apps}.Subject(apps[0]), budget)
	run := startWatchRun(story.Library{Apps: testConfig().Apps}, apps, budget, time.Millisecond)
	watched := <-run.reports
	if string(headless.JSON()) != string(watched.JSON()) {
		t.Fatalf("watched report differs:\n%s\n---\n%s", headless.Text(0), watched.Text(0))
	}
	if len(watched.Findings) == 0 {
		t.Fatalf("%s has no findings to compare:\n%s", watchStory, watched.Text(0))
	}
	if v := run.pace.view(1); !v.all || len(v.findings) != len(watched.Findings) || v.step.Seq == 0 {
		t.Fatalf("pacer after the run: all %v, %d findings (report %d), last step %d", v.all, len(v.findings), len(watched.Findings), v.step.Seq)
	}
}

func watchScreen(w *watcher) string { return ansi.Strip(w.View().Content) }

// settle reads the run until cond holds (a second at most).
func settle(t *testing.T, w *watcher, what string, cond func(watchView) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.Update(watchTick{})
		if cond(w.shown) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: never happened; last step %+v\n%s", what, w.shown.step, watchScreen(w))
		}
		time.Sleep(time.Millisecond)
	}
}

// TestWatchTUIPauseStepQuit drives the watch TUI: it starts running, a
// pauses it, l (right, space) plays exactly one more step at a time, q stops
// watching, and the run then finishes unwatched with the headless report.
func TestWatchTUIPauseStepQuit(t *testing.T) {
	apps := watchApps(t)
	budget := explore.Budget{Depth: 2, Steps: 60}
	headless := explore.Run(story.Library{Apps: testConfig().Apps}.Subject(apps[0]), budget)
	run := startWatchRun(story.Library{Apps: testConfig().Apps}, apps, budget, time.Hour) // the first step holds for an hour
	w, err := newWatcher(run, "rows-story explore")
	if err != nil {
		t.Fatal(err)
	}
	w.m.Width, w.m.Height = 160, 40
	settle(t, w, "the first step", func(v watchView) bool { return v.step.Seq == 1 })
	screen := watchScreen(w)
	for _, want := range []string{watchStory + " (1/1)", "▶ running · every 1h0m0s", "states 1 · steps 0/60 · 60 left · depth 2", "keys: (start)", "◉ step 1", "findings"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("watch page lacks %q:\n%s", want, screen)
		}
	}
	keytest.Press(w, "a")
	w.Update(watchTick{})
	if !strings.Contains(watchScreen(w), "❚❚ paused") {
		t.Fatalf("a did not pause:\n%s", watchScreen(w))
	}
	keytest.Press(w, "l")
	settle(t, w, "one step", func(v watchView) bool { return v.step.Seq == 2 })
	time.Sleep(20 * time.Millisecond)
	w.Update(watchTick{})
	if w.shown.step.Seq != 2 || w.shown.step.Phase != explore.PhaseSearch || w.shown.step.Steps != 1 {
		t.Fatalf("l played more or less than one step: %+v", w.shown.step)
	}
	if !strings.Contains(watchScreen(w), "keys: "+w.shown.step.Action) || !strings.Contains(watchScreen(w), "steps 1/60 · 59 left") {
		t.Fatalf("the stepped state is not shown:\n%s", watchScreen(w))
	}
	keytest.Press(w, "right")
	settle(t, w, "a second step", func(v watchView) bool { return v.step.Seq == 3 })
	keytest.Press(w, "space")
	settle(t, w, "a third step", func(v watchView) bool { return v.step.Seq == 4 })
	keytest.Press(w, "q")
	if !w.m.Runtime.Quit {
		t.Fatal("q did not quit the TUI")
	}
	watched := <-run.reports
	if string(headless.JSON()) != string(watched.JSON()) {
		t.Fatalf("the report after q differs from the headless one:\n%s\n---\n%s", headless.Text(0), watched.Text(0))
	}
}

// TestWatchTUIReplaysAFinding: once the run ends, the findings list holds
// the report's findings; enter replays the selected one's path frame by
// frame (h and l step it), q goes back to the run.
func TestWatchTUIReplaysAFinding(t *testing.T) {
	apps := watchApps(t)
	run := startWatchRun(story.Library{Apps: testConfig().Apps}, apps, explore.Budget{Depth: 2, Steps: 60}, 0)
	w, err := newWatcher(run, "rows-story explore")
	if err != nil {
		t.Fatal(err)
	}
	w.m.Width, w.m.Height = 160, 40
	rep := <-run.reports
	settle(t, w, "the end of the run", func(v watchView) bool { return v.all })
	if !strings.Contains(watchScreen(w), "■ done") || w.list.Len() != len(rep.Findings) {
		t.Fatalf("done page: %d rows for %d findings:\n%s", w.list.Len(), len(rep.Findings), watchScreen(w))
	}
	if f := w.m.Runtime.Focus; f != "f0" {
		t.Fatalf("focus %q, want the first finding", f)
	}
	keytest.Press(w, "enter")
	rp := w.replay
	if rp == nil || w.m.Runtime.Page != "replay" {
		t.Fatalf("enter opened no replay (page %q, msg %q)", w.m.Runtime.Page, w.msg)
	}
	f := rep.Findings[0]
	if len(rp.frames) != len(f.Path)+1 || rp.f.Summary != f.Summary {
		t.Fatalf("replay of %v has %d frames", f.Path, len(rp.frames))
	}
	if !strings.Contains(watchScreen(w), f.Check+": "+f.Summary[:min(20, len(f.Summary))]) || !strings.Contains(watchScreen(w), "(start)") {
		t.Fatalf("replay page:\n%s", watchScreen(w))
	}
	keytest.Press(w, "r")
	keytest.Press(w, "l")
	if rp.at != min(1, len(rp.frames)-1) || rp.auto {
		t.Fatalf("l: at %d auto %v", rp.at, rp.auto)
	}
	keytest.Press(w, "h")
	if rp.at != 0 {
		t.Fatalf("h: at %d", rp.at)
	}
	keytest.Press(w, "q")
	if w.m.Runtime.Page != "watch" || w.replay != nil || w.m.Runtime.Quit {
		t.Fatalf("q in the replay: page %q, quit %v", w.m.Runtime.Page, w.m.Runtime.Quit)
	}
	keytest.Press(w, "q")
	if !w.m.Runtime.Quit {
		t.Fatal("q on the watch page did not quit")
	}
}
