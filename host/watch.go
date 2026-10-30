package host

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/monogatari/explore"
	"github.com/unixsuperhero/ochazuke"
)

// Watch mode (PROG explore --watch) is a view over the explorer's run,
// never a different walk: the run calls Budget.Observe after every step,
// and the pacer holds it there for the delay, while paused, or until a
// single step is asked for. The run finds the same states and findings
// either way; only its speed changes.

const (
	watchDelay   = 50 * time.Millisecond // the default --delay
	watchSlowest = 4 * time.Second
	watchPoll    = 33 * time.Millisecond // how often the screen reads the run
)

// pacer is what the run and the screen share: the screen's controls
// (pause, single steps, the delay, stop) and the run's last step.
type pacer struct {
	mu      sync.Mutex
	wake    chan struct{} // closed and replaced when a control changes
	paused  bool
	credits int // single steps asked for while paused
	delay   time.Duration
	stopped bool

	app      int // the application being explored
	name     string
	step     explore.Step // the last step, Frame cleared
	frame    string       // the last frame drawn
	drawn    time.Time
	findings []explore.Finding // the application's findings so far; its report's once done
	version  int               // bumps when findings change or an application starts or ends
	ended    int               // applications whose run has ended
	reports  []explore.Report
}

func newPacer(delay time.Duration) *pacer {
	return &pacer{wake: make(chan struct{}), delay: max(0, delay), app: -1}
}

// changed wakes an observe waiting on a control. Call with mu held.
func (p *pacer) changed() {
	close(p.wake)
	p.wake = make(chan struct{})
}

// begin starts watching application i.
func (p *pacer) begin(i int, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.app, p.name, p.findings, p.frame, p.step = i, name, nil, "", explore.Step{}
	p.version++
}

// end records application i's report: its findings replace the live
// ones (the report's paths are shrunk).
func (p *pacer) end(i int, rep explore.Report) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reports = append(p.reports, rep)
	p.ended = i + 1
	if i == p.app {
		p.findings = nil
		for _, f := range rep.Findings {
			c := *f
			c.Path = slices.Clone(f.Path)
			p.findings = append(p.findings, c)
		}
		p.version++
	}
}

// observe is Budget.Observe: it keeps the step for the screen and holds
// the run for the delay, while paused, or until a single step is asked
// for. Stopped, it returns at once.
func (p *pacer) observe(st explore.Step) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	if len(st.Findings) > 0 {
		p.findings = append(p.findings, st.Findings...)
		p.version++
	}
	// At full speed the frame is drawn only as often as the screen reads
	// it; otherwise every step's frame is shown.
	if now := time.Now(); p.delay > 0 || p.paused || p.credits > 0 || now.Sub(p.drawn) >= watchPoll {
		if f := st.Frame(); f != "" || st.Phase != explore.PhaseTraps {
			p.frame = f
		}
		p.drawn = now
	}
	st.Frame = nil
	p.step = st
	for {
		switch {
		case p.stopped:
			p.mu.Unlock()
			return
		case p.credits > 0:
			p.credits--
			p.mu.Unlock()
			return
		case p.paused:
			wake := p.wake
			p.mu.Unlock()
			<-wake
			p.mu.Lock()
			continue
		}
		d, wake := p.delay, p.wake
		p.mu.Unlock()
		if d <= 0 {
			return
		}
		t := time.NewTimer(d)
		select {
		case <-t.C:
			return
		case <-wake:
			t.Stop()
			p.mu.Lock()
		}
	}
}

func (p *pacer) control(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn()
	p.changed()
}

// toggle pauses the run, or runs it on at the set speed.
func (p *pacer) toggle() { p.control(func() { p.paused, p.credits = !p.paused, 0 }) }

// single pauses the run and lets exactly one more step through.
func (p *pacer) single() { p.control(func() { p.paused, p.credits = true, p.credits+1 }) }

// pause holds the run at its next step.
func (p *pacer) pause() { p.control(func() { p.paused, p.credits = true, 0 }) }

// faster halves the delay; under a millisecond it is full speed.
func (p *pacer) faster() {
	p.control(func() {
		p.delay /= 2
		if p.delay < time.Millisecond {
			p.delay = 0
		}
	})
}

// slower doubles the delay, from a millisecond at full speed.
func (p *pacer) slower() {
	p.control(func() { p.delay = min(watchSlowest, max(time.Millisecond, p.delay*2)) })
}

// stop lets the run go on unwatched: every observe returns at once.
func (p *pacer) stop() { p.control(func() { p.stopped, p.paused, p.credits = true, false, 0 }) }

// watchView is what the screen draws, read under the pacer's lock.
type watchView struct {
	app, ended   int
	name         string
	step         explore.Step
	frame        string
	findings     []explore.Finding
	version      int
	paused       bool
	delay        time.Duration
	stopped, all bool
}

func (p *pacer) view(apps int) watchView {
	p.mu.Lock()
	defer p.mu.Unlock()
	return watchView{app: p.app, ended: p.ended, name: p.name, step: p.step, frame: p.frame, findings: p.findings, version: p.version,
		paused: p.paused, delay: p.delay, stopped: p.stopped, all: p.ended >= apps}
}

// watchRun explores the applications in order, observed by the pacer
// until it stops, and hands each report on in order; like the headless
// loop it stops after a report with explorer errors.
type watchRun struct {
	lib     story.Library
	pace    *pacer
	apps    []*story.Story
	budget  explore.Budget
	reports chan explore.Report
}

func startWatchRun(lib story.Library, apps []*story.Story, budget explore.Budget, delay time.Duration) *watchRun {
	w := &watchRun{lib: lib, pace: newPacer(delay), apps: apps, budget: budget, reports: make(chan explore.Report, len(apps))}
	go w.run()
	return w
}

func (w *watchRun) run() {
	defer close(w.reports)
	for i, app := range w.apps {
		s := w.lib.Subject(app)
		b := w.budget
		w.pace.mu.Lock()
		stopped := w.pace.stopped
		w.pace.mu.Unlock()
		if !stopped {
			w.pace.begin(i, s.Name)
			b.Observe = w.pace.observe
		}
		rep := explore.Run(s, b)
		w.pace.end(i, rep)
		w.reports <- rep
		if len(rep.Errors) > 0 {
			return
		}
	}
}

// watchReplay is a finding's path played frame by frame.
type watchReplay struct {
	f      explore.Finding
	app    string
	frames []string
	at     int // frames[at] is shown: the state after at keys
	auto   bool
	speed  time.Duration
	gen    int
}

type watchTick struct{}
type watchReplayTick struct{ gen int }

// watcher is the watch TUI: an ochazuke model of its own with a watch
// page (the run's frame beside its counters, key path and findings) and
// a replay page (a finding's path stepped beside its frame).
type watcher struct {
	run      *watchRun
	m        *ochazuke.Model
	list     *ochazuke.List
	findings []explore.Finding // the rows of the list
	app      int
	version  int
	shown    watchView
	replay   *watchReplay
	want     int     // a finding row asked to be replayed, -1 none
	pending  tea.Cmd // a timer a verb asked for
	msg      string
}

func newWatcher(run *watchRun, title string) (*watcher, error) {
	w := &watcher{run: run, app: -1, version: -1, want: -1}
	r := ochazuke.NewRuntime()
	w.m = ochazuke.NewModel(r)
	w.m.Title = title
	iv := func(fn func() error) any {
		return func(_ *ochazuke.Runtime, _ ochazuke.Context, _ ochazuke.Arguments) (any, error) { return nil, fn() }
	}
	p := run.pace
	r.Define("watch").
		Verb("pause", iv(func() error { p.toggle(); return nil }), "Pause the run, or run on at the set speed").
		Verb("step", iv(func() error { p.single(); return nil }), "Pause and play one more step").
		Verb("faster", iv(func() error { p.faster(); return nil }), "Halve the delay between steps (under 1ms is full speed)").
		Verb("slower", iv(func() error { p.slower(); return nil }), "Double the delay between steps").
		Verb("stop", func(r *ochazuke.Runtime, _ ochazuke.Context, _ ochazuke.Arguments) (any, error) {
			p.stop()
			r.Quit = true
			return nil, nil
		}, "Stop watching: the run finishes unwatched and the report prints as without --watch").
		Mode("normal").
		Bind("a", ochazuke.Do("watch.pause")).
		Bind("space", ochazuke.Do("watch.step")).
		Bind("l", ochazuke.Do("watch.step")).
		Bind("right", ochazuke.Do("watch.step")).
		Bind("+", ochazuke.Do("watch.faster")).
		Bind("=", ochazuke.Do("watch.faster")).
		Bind("-", ochazuke.Do("watch.slower")).
		Bind("q", ochazuke.Do("watch.stop"))
	r.Define("finding").Columns(ochazuke.Column{Title: "check", Max: 16, Priority: 1}, ochazuke.Column{Title: "summary", Min: 16}, ochazuke.Column{Title: "keys", Max: 20, Priority: 2}).
		Verb("replay", func(c ochazuke.Ctx) ochazuke.Result {
			i, ok := c.Instance.Data.(int)
			if !ok {
				return ochazuke.Failed(errors.New("not a finding row"))
			}
			w.want = i
			return ochazuke.Done(nil)
		}, "Replay the finding's path frame by frame (the run pauses)").
		Mode("normal").
		Bind("enter", ochazuke.Do("finding.replay"))
	iv2 := func(fn func(*watchReplay)) any {
		return iv(func() error {
			if w.replay != nil {
				fn(w.replay)
			}
			return nil
		})
	}
	r.Define("replay").
		Verb("step", iv2(func(rp *watchReplay) { rp.auto = false; rp.at = min(rp.at+1, len(rp.frames)-1) }), "Show the state after the next key").
		Verb("back", iv2(func(rp *watchReplay) { rp.auto = false; rp.at = max(0, rp.at-1) }), "Show the state before the last key").
		Verb("restart", iv2(func(rp *watchReplay) { rp.auto = false; rp.at = 0 }), "Back to the state before the first key").
		Verb("end", iv2(func(rp *watchReplay) { rp.auto = false; rp.at = len(rp.frames) - 1 }), "Show the state after the last key").
		Verb("auto", iv2(func(rp *watchReplay) {
			rp.auto = !rp.auto && rp.at < len(rp.frames)-1
			if rp.auto {
				rp.gen++
				w.pending = w.replayTimer()
			}
		}), "Play on at the set speed; again pauses").
		Verb("faster", iv2(func(rp *watchReplay) { rp.speed = max(playFastest, rp.speed/2) }), "Halve the delay between keys").
		Verb("slower", iv2(func(rp *watchReplay) { rp.speed = min(playSlowest, rp.speed*2) }), "Double the delay between keys").
		Verb("leave", func(r *ochazuke.Runtime, _ ochazuke.Context, _ ochazuke.Arguments) (any, error) {
			w.replay = nil
			return nil, r.OpenPage("watch")
		}, "Back to the run (still paused; a runs it on)").
		Mode("normal").
		Bind("space", ochazuke.Do("replay.step")).
		Bind("l", ochazuke.Do("replay.step")).
		Bind("right", ochazuke.Do("replay.step")).
		Bind("h", ochazuke.Do("replay.back")).
		Bind("left", ochazuke.Do("replay.back")).
		Bind("r", ochazuke.Do("replay.restart")).
		Bind("f", ochazuke.Do("replay.end")).
		Bind("a", ochazuke.Do("replay.auto")).
		Bind("+", ochazuke.Do("replay.faster")).
		Bind("=", ochazuke.Do("replay.faster")).
		Bind("-", ochazuke.Do("replay.slower")).
		Bind("q", ochazuke.Do("replay.leave")).
		Bind("esc", ochazuke.Do("replay.leave"))
	if _, err := r.AddPage("watch", "Watch", w.renderWatch, ochazuke.Kind("watch")); err != nil {
		return nil, err
	}
	l, err := r.AddList("findings", nil, ochazuke.Parent("watch"), ochazuke.NoMarks(), ochazuke.Columns(r.Noun("finding").Columns...))
	if err != nil {
		return nil, err
	}
	w.list = l
	if _, err := r.AddPage("replay", "Replay", w.renderReplay, ochazuke.Kind("replay")); err != nil {
		return nil, err
	}
	if err := r.OpenPage("watch"); err != nil {
		return nil, err
	}
	if err := r.SetFocus("findings"); err != nil {
		return nil, err
	}
	if _, err := r.ValidateBindings(); err != nil {
		return nil, err
	}
	w.refresh()
	return w, nil
}

func (w *watcher) Init() tea.Cmd { return tea.Batch(w.m.Init(), w.pollTimer()) }

func (w *watcher) pollTimer() tea.Cmd {
	return tea.Tick(watchPoll, func(time.Time) tea.Msg { return watchTick{} })
}

func (w *watcher) replayTimer() tea.Cmd {
	rp := w.replay
	if rp == nil {
		return nil
	}
	gen := rp.gen
	return tea.Tick(rp.speed, func(time.Time) tea.Msg { return watchReplayTick{gen} })
}

// refresh reads the run: the counters and frame, and the findings list
// when it changed.
func (w *watcher) refresh() {
	v := w.run.pace.view(len(w.run.apps))
	w.shown = v
	if v.version == w.version && v.app == w.app {
		return
	}
	w.version, w.app = v.version, v.app
	w.findings = slices.Clone(v.findings)
	items := make([]ochazuke.ListItem, len(w.findings))
	for i, f := range w.findings {
		items[i] = ochazuke.ListItem{ID: fmt.Sprintf("f%d", i), Label: f.Summary, Noun: "finding", Data: i,
			Cells: []string{f.Check, f.Summary, pathText(f.Path)}}
	}
	if err := w.list.SetItems(items); err != nil {
		w.msg = err.Error()
	}
	r := w.m.Runtime
	if r.Page == "watch" && (r.Focus == "" || r.Instances[r.Focus] == nil || r.Focus == "watch") {
		_ = r.SetFocus("findings")
	}
	if r.Focus == "findings" && len(items) > 0 {
		_ = r.SetFocus(items[0].ID)
	}
}

// openReplay pauses the run and replays finding i of the list.
func (w *watcher) openReplay(i int) {
	if i < 0 || i >= len(w.findings) || w.app < 0 {
		return
	}
	w.run.pace.pause()
	f := w.findings[i]
	s := w.run.lib.Subject(w.run.apps[w.app])
	frames, err := explore.Frames(s, f.Path)
	if err != nil {
		w.msg = "replay: " + err.Error()
		return
	}
	w.replay = &watchReplay{f: f, app: s.Name, frames: frames, speed: playDefault, auto: len(frames) > 1}
	if err := w.m.Runtime.OpenPage("replay"); err != nil {
		w.msg = "replay: " + err.Error()
		w.replay = nil
		return
	}
	if w.replay.auto {
		w.replay.gen++
		w.pending = w.replayTimer()
	}
}

func (w *watcher) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case watchTick:
		w.refresh()
		return w, w.pollTimer()
	case watchReplayTick:
		rp := w.replay
		if rp == nil || !rp.auto || v.gen != rp.gen {
			return w, nil
		}
		rp.at++
		if rp.at >= len(rp.frames)-1 {
			rp.at, rp.auto = len(rp.frames)-1, false
			return w, nil
		}
		return w, w.replayTimer()
	}
	_, cmd := w.m.Update(msg)
	if w.m.Runtime.Quit {
		w.run.pace.stop()
		return w, tea.Quit
	}
	if w.want >= 0 {
		i := w.want
		w.want = -1
		w.openReplay(i)
	}
	if w.pending != nil {
		cmd, w.pending = tea.Batch(cmd, w.pending), nil
	}
	return w, cmd
}

func (w *watcher) View() tea.View { return w.m.View() }

func (w *watcher) styles() ochazuke.Styles { return ochazuke.NewStyles(*w.m.Appearance) }

// frameSize is the size of the application being shown.
func (w *watcher) frameSize() (int, int) {
	i := max(0, w.app)
	if i >= len(w.run.apps) {
		return 80, 24
	}
	return w.run.apps[i].Size()
}

// renderWatch draws the run: the status, counters and key path with the
// findings beneath on the left, the explored state's frame boxed on the
// right.
func (w *watcher) renderWatch(r *ochazuke.Runtime, width, ht int) ochazuke.Block {
	s := w.styles()
	v := w.shown
	fw, fh := w.frameSize()
	side := sideWidth(width, fw)

	var status string
	switch {
	case v.all:
		status = s.Muted.Render("■ done")
	case v.paused:
		status = s.Accent.Render("❚❚ paused")
	case v.delay == 0:
		status = s.Accent.Render("▶ running") + s.Muted.Render(" · full speed")
	default:
		status = s.Accent.Render("▶ running") + s.Muted.Render(" · every "+v.delay.String())
	}
	name := v.name
	if name == "" {
		name = "starting"
	}
	st := v.step
	head := s.Title.Render(fmt.Sprintf("%s (%d/%d)", name, max(1, v.app+1), len(w.run.apps))) + "  " + status
	counts := fmt.Sprintf("states %d · steps %d/%d · %d left · depth %d", st.Visited, st.Steps, st.Budget, max(0, st.Budget-st.Steps), st.Depth)
	where := "keys: " + pathText(st.Path)
	switch {
	case v.ended > v.app && v.app >= 0:
		where = "run done; the findings are the report's (paths shrunk)"
	case st.Phase == explore.PhaseTraps:
		where = "trap search done; shrinking"
	case st.Phase == explore.PhaseSearch && st.New:
		where += s.Accent.Render("  new")
	}
	side1 := []string{ansi.Truncate(head, side, "…"), s.Muted.Render(ansi.Truncate(counts, side, "…")), ansi.Truncate(where, side, "…"), ""}
	if w.msg != "" {
		side1 = append(side1, s.Error.Render(ansi.Truncate(w.msg, side, "…")), "")
	}
	side1 = append(side1, s.Title.Render(fmt.Sprintf("findings %d", len(w.findings))))
	if len(w.findings) == 0 {
		side1 = append(side1, s.Muted.Render("none yet"))
	} else if left := ht - len(side1); left > 0 {
		side1 = append(side1, strings.Split(w.list.View(side, left), "\n")...)
	}

	frame := v.frame
	if frame == "" {
		frame = strings.Repeat("\n", fh-1)
	}
	title := fmt.Sprintf(" ◉ step %d ", st.Seq)
	box := boxFrame(frame, title, fmt.Sprintf(" %dx%d ", fw, fh), fw)
	return ochazuke.Text(beside(side1, box, side, width, ht))
}

// renderReplay draws a finding's path with the shown position marked
// beside that position's frame.
func (w *watcher) renderReplay(_ *ochazuke.Runtime, width, ht int) ochazuke.Block {
	s := w.styles()
	rp := w.replay
	if rp == nil {
		return ochazuke.Text(s.Muted.Render("no finding is replaying"))
	}
	fw, fh := w.frameSize()
	side := sideWidth(width, fw)
	status := s.Muted.Render("paused")
	switch {
	case rp.auto:
		status = s.Accent.Render("▶ auto") + s.Muted.Render(" every "+rp.speed.String())
	case rp.at >= len(rp.frames)-1:
		status = s.Muted.Render("end")
	}
	head := s.Title.Render(fmt.Sprintf("replay %d/%d", rp.at, len(rp.frames)-1)) + "  " + status
	out := []string{
		ansi.Truncate(head, side, "…"),
		s.Error.Render(ansi.Truncate(rp.f.Check+": "+rp.f.Summary, side, "…")),
		"",
	}
	start := "(start)"
	marker := "  "
	if rp.at == 0 {
		marker = s.Accent.Render("▸ ")
	}
	out = append(out, marker+start)
	for i, k := range rp.f.Path {
		marker := "  "
		if i+1 == rp.at {
			marker = s.Accent.Render("▸ ")
		}
		key := ansi.Truncate(k, side-2, "…")
		if i+1 > rp.at {
			key = s.Muted.Render(key)
		}
		out = append(out, marker+key)
	}
	if d := firstLine(rp.f.Detail); d != "" && d != rp.f.Summary {
		out = append(out, "", s.Muted.Render(ansi.Truncate(d, side, "…")))
	}
	frame := rp.frames[rp.at]
	if frame == "" {
		frame = strings.Repeat("\n", fh-1)
	}
	box := boxFrame(frame, " ↺ "+rp.app+" ", fmt.Sprintf(" %dx%d ", fw, fh), fw)
	return ochazuke.Text(beside(out, box, side, width, ht))
}

func pathText(path []string) string {
	if len(path) == 0 {
		return "(start)"
	}
	return strings.Join(path, " ")
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}

// watchTUI runs the watch TUI until q (or the last run ends and q is
// pressed), then says on stderr when runs are still finishing unwatched.
func (t *Tool) watchTUI(run *watchRun) error {
	w, err := newWatcher(run, t.cfg.name()+" explore")
	if err != nil {
		run.pace.stop()
		return err
	}
	// The TUI draws on the terminal itself, so stdout can take the
	// report (PROG explore --watch > report.txt).
	var opts []tea.ProgramOption
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		opts = append(opts, tea.WithInput(tty), tea.WithOutput(tty))
	}
	_, err = tea.NewProgram(w, opts...).Run()
	run.pace.stop()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	if v := run.pace.view(len(run.apps)); !v.all {
		fmt.Fprintf(t.Stderr, "%s explore: watch stopped; finishing %d of %d runs unwatched\n", t.Prog, len(run.apps)-v.ended, len(run.apps))
	}
	return nil
}
