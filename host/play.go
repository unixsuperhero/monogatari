package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
)

// player plays a story's script one event at a time, headless, and shows
// each event beside its assertion: the script on the left, the story's
// frame on the right. Nothing is typed into the story; the script is the
// only source of events. Stepping back rebuilds the session and replays
// up to the step before, so any position is reachable.
type player struct {
	lib    story.Library
	story  *story.Story  // the resolved story (a trimmed one merged with its base)
	prefix []story.Event // the base's events played before the window
	d      *story.Driver
	at     int // events of the script played so far
	err    error
	action int // -1: setup (or an ordinary script); otherwise Actions index

	steps   map[int][]story.Expect // expects keyed by After; 0 is the final set
	results map[int][]string       // After -> failures (an empty slice is a pass); unset while unchecked
	golden  *string                // the golden row's outcome when the story is pinned by a golden ("" passes)

	auto  bool
	speed time.Duration
	gen   int

	// recording routes the host's keys into the story; every event (a key,
	// a paste, a job that delivers, a pane frame) is appended to the script
	// as it happens, so the column fills in while you type.
	recording bool
	rec       *story.Recorder
	polls     int
}

const (
	playFastest = 40 * time.Millisecond
	playSlowest = 4 * time.Second
	playDefault = 400 * time.Millisecond
	playWait    = 3 * time.Second // how long a job or tick event may hold the loop
)

type playTick struct{ gen int }

// recordTick polls a recording session for the messages its jobs and
// panes produced and emits a fake pane's output line once a second.
type recordTick struct{ gen int }

const recordPoll = 250 * time.Millisecond

// recordStop is the one key the host keeps while recording.
const recordStop = "ctrl+g"

// playRow is one line of the script column: an event (or the end marker),
// what it asserts, and how the assertion went.
type playRow struct {
	Event  string
	Assert string
	Ran    string // the top-level requests the event ran, once played
	State  rowState
	Final  bool
	Detail string
	Depth  int
	Parent bool
	Reset  bool
}

type rowState int

const (
	rowUnplayed rowState = iota
	rowPlayed            // played, nothing to assert
	rowPassed
	rowFailed
)

// newPlayer resolves and builds the story and plays the base's prefix.
func newPlayer(lib story.Library, s *story.Story) (*player, error) {
	resolved, prefix, err := lib.Resolve(s)
	if err != nil {
		return nil, err
	}
	owned := *resolved
	owned.Script = slices.Clone(resolved.Script)
	p := &player{lib: lib, story: &owned, prefix: prefix, speed: playDefault, action: -1}
	if err := p.rebuild(0); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// rebuild starts the session over and plays the first n script events,
// checking each step's expects as the runner would.
func (p *player) rebuild(n int) error {
	if p.d != nil {
		p.d.Close()
		p.d = nil
	}
	p.at, p.err, p.results, p.golden = 0, nil, map[int][]string{}, nil
	p.steps = map[int][]story.Expect{}
	expects := p.story.Expect
	if p.action >= 0 {
		expects = p.story.Actions[p.action].Expect
	}
	for _, e := range expects {
		p.steps[e.After] = append(p.steps[e.After], e)
	}
	ss, err := p.lib.Build(p.story, story.Options{})
	if err != nil {
		return err
	}
	p.d = story.NewDriver(ss, story.Options{})
	p.d.Wait = playWait
	if err := p.d.Play(p.prefix, nil); err != nil {
		p.Close()
		return fmt.Errorf("base: %w", err)
	}
	if p.action >= 0 {
		if err := p.d.Play(p.story.Setup, func(n int) error {
			for _, e := range p.story.Expect {
				if e.After == n {
					if bad := story.Check(p.d.S, e); len(bad) > 0 {
						return fmt.Errorf("%s", strings.Join(bad, "; "))
					}
				}
			}
			return nil
		}); err != nil {
			p.Close()
			return fmt.Errorf("setup: %w", err)
		}
		for _, e := range p.story.Expect {
			if e.After == 0 {
				if bad := story.Check(p.d.S, e); len(bad) > 0 {
					return fmt.Errorf("setup: %s", strings.Join(bad, "; "))
				}
			}
		}
	}
	p.d.Trace = nil
	for i := 0; i < n && p.at < len(p.script()); i++ {
		p.step()
	}
	return nil
}

// Close ends the session.
func (p *player) Close() {
	if p.d != nil {
		p.d.Close()
	}
}

func (p *player) script() []story.Event {
	if p.action >= 0 {
		return p.story.Actions[p.action].Keys
	}
	if len(p.story.Actions) > 0 {
		return p.story.Setup
	}
	return p.story.Script
}

// done reports that every event has played (or a step failed to play).
func (p *player) done() bool {
	return p.err != nil || p.at >= len(p.script()) && p.action >= len(p.story.Actions)-1
}

// chooseAction reuses rebuild to reach the reset point without replaying
// earlier lessons. Changing actions pauses autoplay and invalidates old ticks.
func (p *player) chooseAction(index int) error {
	if len(p.story.Actions) == 0 {
		return nil
	}
	p.auto = false
	p.gen++
	p.action = max(0, min(index, len(p.story.Actions)-1))
	p.err = p.rebuild(0)
	return p.err
}

func (p *player) restart() error {
	p.action, p.auto = -1, false
	p.gen++
	p.err = p.rebuild(0)
	return p.err
}

// step plays the next event and checks what the script expects after it;
// after the last event the final expects (else the golden) are checked.
func (p *player) step() {
	if p.done() {
		return
	}
	if p.at >= len(p.script()) {
		p.action++
		p.err = p.rebuild(0)
		return // leave the reset frame and caption visible for one tick
	}
	e := p.script()[p.at]
	if err := p.d.Step(e); err != nil {
		p.err = fmt.Errorf("event %d (%s): %w", p.at+1, e, err)
		return
	}
	p.at++
	if ex, ok := p.steps[p.at]; ok {
		var bad []string
		for _, x := range ex {
			bad = append(bad, story.Check(p.d.S, x)...)
		}
		p.results[p.at] = nonNil(bad)
	}
	if p.at < len(p.script()) {
		return
	}
	if final, ok := p.steps[0]; ok {
		var bad []string
		for _, x := range final {
			bad = append(bad, story.Check(p.d.S, x)...)
		}
		p.results[0] = nonNil(bad)
		return
	}
	if len(p.story.Actions) > 0 {
		return // tutorial assertions are optional; never require a golden
	}
	// No final expect: the golden frame pins the story, as the runner's
	// default does.
	got := story.Golden(p.d.S.Frame())
	want, err := p.story.ReadGolden()
	var outcome string
	switch {
	case errors.Is(err, fs.ErrNotExist):
		outcome = "no golden (" + p.story.GoldenPath() + ")"
	case err != nil:
		outcome = err.Error()
	case string(want) != got:
		outcome = "frame differs from the golden"
	}
	p.golden = &outcome
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// back replays to the step before the current one.
func (p *player) back() error {
	if p.at == 0 && p.action >= 0 {
		p.action--
		return p.rebuild(len(p.script()))
	}
	return p.rebuild(max(0, p.at-1))
}

// startRecord makes the current position the starting state: the unplayed
// tail of the script and its expects are dropped, and what is typed from
// here on becomes the script.
func (p *player) startRecord() {
	// Turn the current lesson into an ordinary script so saved recordings
	// replay exactly what was on screen, with its reset prefix included.
	if len(p.story.Actions) > 0 {
		copy := *p.story
		copy.Script = append([]story.Event{}, p.script()...)
		if p.action >= 0 {
			p.prefix = append(p.prefix, p.story.Setup...)
			copy.Expect = p.story.Actions[p.action].Expect
		}
		copy.Setup, copy.Actions, copy.Notes = nil, nil, nil
		p.story, p.action = &copy, -1
	}
	p.auto = false
	p.story.Script = p.story.Script[:p.at]
	for after := range p.steps {
		if after > p.at {
			delete(p.steps, after)
		}
	}
	p.golden, p.err = nil, nil
	p.recording = true
	p.polls = 0
	p.rec = story.NewRecorder(p.story, nil)
	p.gen++
}

// stopRecord ends the recording; the events stay in the script.
func (p *player) stopRecord() { p.recording = false }

// recorded appends an event the live session just played.
func (p *player) recorded(e story.Event) {
	p.rec.Add(e)
	e.T = p.rec.Rec.Events[len(p.rec.Rec.Events)-1].T
	p.story.Script = append(p.story.Script, e)
	p.at++
}

// record delivers one of the host's messages to the story as an event.
func (p *player) record(msg tea.Msg) {
	ev := story.Classify(p.d.S, msg)
	if ev == nil {
		return
	}
	if err := p.d.Step(*ev); err != nil {
		p.err = err
		return
	}
	p.recorded(*ev)
}

// poll plays what the session's jobs and panes produced since the last
// poll and, once a second, a fake pane's output line.
func (p *player) poll() {
	for _, ev := range p.d.Arrived() {
		p.recorded(ev)
	}
	p.polls++
	if p.polls%int(time.Second/recordPoll) == 0 && p.d.S.HasPane() {
		ev := story.Event{Tick: "output"}
		if err := p.d.Step(ev); err == nil {
			p.recorded(ev)
		}
	}
}

// recording is the script so far as a recording: the base's prefix and
// every event played, Initial the story it started from.
func (p *player) asRecording() *story.Recording {
	started := time.Now()
	if p.rec != nil {
		started = p.rec.Rec.Started
	}
	initial := *p.story
	initial.Script, initial.Expect, initial.Setup, initial.Actions = nil, nil, nil, nil
	events := append([]story.Event{}, p.prefix...)
	if p.action >= 0 {
		events = append(events, p.story.Setup...)
	}
	events = append(events, p.script()[:p.at]...)
	expects := p.story.Expect
	if p.action >= 0 {
		expects = p.story.Actions[p.action].Expect
	}
	expects = append(story.Expects(nil), expects...)
	prefixLength := len(p.prefix)
	if p.action >= 0 {
		prefixLength += len(p.story.Setup)
	}
	for i := range expects {
		if expects[i].After > 0 {
			expects[i].After += prefixLength
		}
	}
	return &story.Recording{Story: p.story.Name, Started: started, Initial: &initial, Events: events, Expect: expects}
}

// rows is the script column: one row per event, plus an end row when the
// story asserts something at the end (an expect or a golden).
func (p *player) rows() []playRow {
	script := p.script()
	rows := make([]playRow, 0, len(script)+1)
	for i, e := range script {
		row := playRow{Event: e.String()}
		if ex, ok := p.steps[i+1]; ok {
			row.Assert = summarize(ex)
		}
		if i < p.at {
			row.State = rowPlayed
			if i < len(p.d.Trace) {
				var ran []string
				for _, r := range p.d.Trace[i].Ran {
					ran = append(ran, r.Verb)
				}
				row.Ran = strings.Join(ran, " ")
			}
			if bad, ok := p.results[i+1]; ok {
				row.State = rowPassed
				if len(bad) > 0 {
					row.State = rowFailed
				}
			}
		}
		rows = append(rows, row)
	}
	if final, ok := p.steps[0]; ok {
		row := playRow{Event: "end", Assert: summarize(final), Final: true}
		if bad, ok := p.results[0]; ok {
			row.State = rowPassed
			if len(bad) > 0 {
				row.State = rowFailed
			}
		}
		rows = append(rows, row)
	} else if len(p.story.Actions) == 0 {
		row := playRow{Event: "end", Assert: "frame = golden", Final: true}
		if p.golden != nil {
			row.State = rowPassed
			if *p.golden != "" {
				row.State = rowFailed
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// current is the index of the row the player stands on: the last event
// played, the end row once everything has, -1 before the first step.
func (p *player) current() int {
	if p.at == 0 {
		return -1
	}
	if p.at >= len(p.script()) {
		return len(p.script())
	}
	return p.at - 1
}

// tutorialRows is one tree, independent of the segment currently playing.
// The reset row remains the cursor position until an action's first key runs.
func (p *player) tutorialRows() ([]playRow, int) {
	steps := func(events []story.Event, expects story.Expects) []playRow {
		rows := make([]playRow, len(events))
		for i, e := range events {
			rows[i] = playRow{Event: e.String(), Detail: e.Note}
		}
		for _, e := range expects {
			if e.After > 0 && e.After <= len(rows) {
				rows[e.After-1].Assert = summarize([]story.Expect{e})
			}
		}
		return rows
	}
	setup := steps(p.story.Setup, p.story.Expect)
	reset := playRow{Event: "reset point", Reset: true}
	for _, e := range p.story.Expect {
		if e.After == 0 {
			reset.Assert = summarize([]story.Expect{e})
		}
	}
	if p.action < 0 {
		played := p.rows()
		copy(setup, played)
		for i := range setup {
			setup[i].Detail = p.story.Setup[i].Note
		}
		if len(played) > len(setup) {
			reset.State = played[len(setup)].State
		}
	} else {
		for i := range setup {
			setup[i].State = rowPlayed
		}
		reset.State = rowPassed
	}
	rows := append(setup, reset)
	addNotes := func(notes []story.Note, start, end, opening int) {
		for _, note := range notes {
			at := opening
			if note.After > 0 {
				at = min(start+note.After-1, end)
			}
			rows[at].Detail = strings.TrimSpace(rows[at].Detail + "\n" + note.Note)
		}
	}
	addNotes(p.story.Notes, 0, len(setup), 0)
	cur := p.current()
	if p.action >= 0 || p.at >= len(setup) {
		cur = len(setup)
	}
	for i, a := range p.story.Actions {
		parent := len(rows)
		parentRow := playRow{Event: a.Label, Detail: a.Caption, Parent: true}
		var final story.Expects
		for _, e := range a.Expect {
			if e.After == 0 {
				final = append(final, e)
			}
		}
		parentRow.Assert = summarize(final)
		rows = append(rows, parentRow)
		children := steps(a.Keys, a.Expect)
		if i == p.action {
			children = p.rows()
			if len(children) > len(a.Keys) {
				final := children[len(a.Keys)]
				rows[parent].Assert, rows[parent].State = final.Assert, final.State
				children = children[:len(a.Keys)]
			}
			for j := range a.Keys {
				children[j].Detail = a.Keys[j].Note
			}
			if p.at > 0 {
				cur = parent + p.at
			}
		}
		for j := range children {
			children[j].Depth = 1
		}
		rows = append(rows, children...)
		addNotes(a.Notes, parent+1, len(rows)-1, parent)
	}
	return rows, cur
}

// failures is what went wrong at the current position, if anything.
func (p *player) failures() []string {
	if p.err != nil {
		return []string{p.err.Error()}
	}
	if p.at >= len(p.script()) {
		if bad, ok := p.results[0]; ok {
			return bad
		}
		if p.golden != nil && *p.golden != "" {
			return []string{*p.golden}
		}
		if p.at > 0 {
			return p.results[p.at]
		}
		return nil
	}
	return p.results[p.at]
}

// summarize writes a set of expects as one short line.
func summarize(ex []story.Expect) string {
	var parts []string
	q := func(s string) string { return fmt.Sprintf("%q", s) }
	for _, e := range ex {
		if e.Focus != "" {
			parts = append(parts, "focus="+e.Focus)
		}
		if e.Mode != "" {
			parts = append(parts, "mode="+e.Mode)
		}
		if e.Modal != nil {
			if *e.Modal == "" {
				parts = append(parts, "no modal")
			} else {
				parts = append(parts, "modal="+*e.Modal)
			}
		}
		if e.Page != "" {
			parts = append(parts, "page="+e.Page)
		}
		if e.Visible != nil {
			if *e.Visible {
				parts = append(parts, "focus visible")
			} else {
				parts = append(parts, "focus off screen")
			}
		}
		if e.Highlighted != nil {
			parts = append(parts, fmt.Sprintf("highlighted=%d", *e.Highlighted))
		}
		for _, s := range e.Contains {
			parts = append(parts, "has "+q(s))
		}
		for _, s := range e.Lacks {
			parts = append(parts, "lacks "+q(s))
		}
		if len(e.Order) > 0 {
			var quoted []string
			for _, s := range e.Order {
				quoted = append(quoted, q(s))
			}
			parts = append(parts, "order "+strings.Join(quoted, " < "))
		}
		if e.NoTicker {
			parts = append(parts, "no ticker")
		} else if e.Ticker != nil {
			parts = append(parts, fmt.Sprintf("ticker=%q", e.Ticker))
		}
		if e.NoNotices {
			parts = append(parts, "no notices")
		} else if e.Notices != nil {
			parts = append(parts, fmt.Sprintf("notices=%q", e.Notices))
		}
	}
	return strings.Join(parts, ", ")
}

// definePlay registers the play page and the player's verbs on the index
// runtime. The page's instance is of the play noun, so its rows answer
// while the page is open and the footer's hints derive from them.
func (h *host) definePlay(r *ochazuke.Runtime) error {
	if err := r.DeclareSetting(ochazuke.Setting{Name: "play.tree", Type: ochazuke.SettingBoolean, Default: true, Description: "Show tutorials in one tree: setup, reset point, and action titles with their key steps; off shows only the active segment"}); err != nil {
		return err
	}
	if err := r.DeclareSetting(ochazuke.Setting{Name: "play.captions", Type: ochazuke.SettingBoolean, Default: true, Description: "Show tutorial captions and step notes in the player"}); err != nil {
		return err
	}
	if err := r.DeclareSetting(ochazuke.Setting{Name: "play.action-pause", Type: ochazuke.SettingDuration, Default: "3s", Description: "Pause before and after tutorial actions; 0s uses the key playback speed"}); err != nil {
		return err
	}
	iv := func(fn func() error) any {
		return func(_ *ochazuke.Runtime, _ ochazuke.Context, _ ochazuke.Arguments) (any, error) {
			return nil, fn()
		}
	}
	r.Define("play").
		Verb("step", iv(func() error { h.play.auto = false; h.play.step(); return nil }), "Play the next event").
		Verb("back", iv(func() error { h.play.auto = false; return h.play.back() }), "Replay to the step before").
		Verb("restart", iv(func() error { return h.play.restart() }), "Start the story over").
		Verb("next-action", iv(func() error { return h.play.chooseAction(h.play.action + 1) }), "Reset and show the next tutorial action").
		Verb("previous-action", iv(func() error { return h.play.chooseAction(h.play.action - 1) }), "Reset and show the previous tutorial action").
		Verb("end", iv(func() error {
			h.play.auto = false
			for !h.play.done() {
				h.play.step()
			}
			return nil
		}), "Play every remaining event now").
		Verb("auto", iv(func() error {
			h.play.auto = !h.play.auto && !h.play.done()
			if h.play.auto {
				h.play.gen++
				h.playTick = h.playTimer()
			}
			return nil
		}), "Play on at the set speed; again pauses").
		Verb("faster", iv(func() error { h.play.speed = max(playFastest, h.play.speed/2); return nil }), "Halve the delay between events").
		Verb("slower", iv(func() error { h.play.speed = min(playSlowest, h.play.speed*2); return nil }), "Double the delay between events").
		Verb("record", iv(func() error {
			h.play.startRecord()
			h.playTick = h.recordTimer()
			return nil
		}), "Record from here: keys go to the story and fill the script; ctrl+g stops").
		Verb("save", iv(func() error { return h.savePlay() }), "Write the script so far as a recording and ask for a name").
		Verb("fullsize", iv(func() error {
			if h.play == nil {
				return errors.New("no story is playing")
			}
			if !h.embedded {
				h.paneReturn = true
				return h.returnToPane()
			}
			h.paneReturn = true
			h.embedded = false
			return r.OpenPage("play")
		}), "Toggle full size, keeping the pane player's position").
		Verb("leave", iv(func() error { return h.leavePlay() }), "Return to the enclosing pane or selected story")
	r.Instance("index-player", ochazuke.Parent("index"), ochazuke.Kind("play"))
	_, err := r.AddPage("play", "Play", h.renderPlay, ochazuke.Kind("play"))
	return err
}

// openPlay starts a cancellable job. Fixture construction never runs on the
// update loop; a Completion installs the owned session and starts its timer.
func (h *host) openPlay(s *story.Story) tea.Cmd {
	r := h.idx.Runtime
	if h.previewCancel != nil {
		h.previewCancel()
		h.previewCancel = nil
		h.previewBusy = false
	}
	here, focus := h.wantHere, r.Focus
	h.starting, h.startFocus = s, focus
	if !r.Settings().Bool("index.preview") && r.Settings().Bool("play.start-indicator") {
		h.idx.Info = "Starting…"
	}
	lib, wait, slot := h.lib, r.Settings().Duration("story.build-timeout"), h.playerSlot
	res := ochazuke.Pending(func(ctx context.Context) (any, error) {
		p, err := buildPlayerInSlot(ctx, lib, s, wait, slot)
		return playerBuilt{p}, err
	}).ReplacingAt("index", "player").Label("Build story").Then(func(_ ochazuke.Ctx, value any, err error) {
		h.starting = nil
		if h.idx.Info == "Starting…" {
			h.idx.Info = ""
		}
		if err != nil {
			return
		}
		p := value.(playerBuilt).player
		if h.play != nil {
			h.gen = max(h.gen, h.play.gen)
			h.play.Close()
		}
		if inst := r.Instances[focus]; inst != nil && inst.Noun == "entry" {
			h.returnFocus = focus
		}
		h.play, h.embedded, h.paneReturn = p, here, here
		h.gen++
		p.gen = h.gen
		page, target := "play", ""
		if here {
			page, target = "index", "index-player"
		}
		if err := r.OpenPage(page); err != nil {
			p.Close()
			h.play = nil
			h.idx.Status = err.Error()
			return
		}
		if target != "" {
			r.SetFocus(target)
		}
		p.auto = !p.done()
		if p.auto {
			h.playTick = h.playTimer()
		}
	})
	ticket, err := r.Launch("index", "entry.play", res, ochazuke.Ctx{Runtime: r})
	if err != nil {
		h.starting = nil
		h.idx.Status = err.Error()
		return nil
	}
	h.startTicket = ticket
	return r.PendingCommands()
}

func (h *host) cancelStart() {
	h.idx.Runtime.Settle(h.startTicket, context.Canceled)
	h.starting = nil
	if h.idx.Info == "Starting…" {
		h.idx.Info = ""
	}
}

func (h *host) returnToPane() error {
	h.embedded = true
	if err := h.idx.Runtime.OpenPage("index"); err != nil {
		return err
	}
	return h.idx.Runtime.SetFocus("index-player")
}

// leavePlay closes the player and returns to the index.
func (h *host) leavePlay() error {
	if !h.embedded && h.paneReturn {
		return h.returnToPane()
	}
	h.paneReturn = false
	if h.play != nil {
		h.gen = max(h.gen, h.play.gen)
		h.play.Close()
		h.play = nil
	}
	h.embedded = false
	if err := h.idx.Runtime.OpenPage("index"); err != nil {
		return err
	}
	if h.returnFocus != "" {
		return h.idx.Runtime.SetFocus(h.returnFocus)
	}
	return nil
}

// savePlay writes the script so far into the history ring and asks for a
// name, as leaving a live story does.
func (h *host) savePlay() error {
	p := h.play
	if p == nil || p.at == 0 {
		return errors.New("nothing to save: play or record some events first")
	}
	p.stopRecord()
	path, err := h.store.Push(p.asRecording())
	if err != nil {
		return fmt.Errorf("recording not written: %w", err)
	}
	return h.askName(path)
}

// recordTimer schedules the next poll of a recording session.
func (h *host) recordTimer() tea.Cmd {
	gen := h.play.gen
	return tea.Tick(recordPoll, func(time.Time) tea.Msg { return recordTick{gen} })
}

// onRecordTick polls the recording session and schedules the next poll.
func (h *host) onRecordTick(t recordTick) tea.Cmd {
	p := h.play
	if p == nil || !p.recording || t.gen != p.gen {
		return nil
	}
	p.poll()
	return h.recordTimer()
}

// recordKey routes a key or paste into the recording story; the stop key
// ends the recording instead.
func (h *host) recordKey(msg tea.Msg) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == recordStop {
		h.play.stopRecord()
		return
	}
	h.play.record(msg)
}

// playTimer schedules the next automatic step.
func (h *host) playTimer() tea.Cmd {
	gen := h.play.gen
	delay := h.play.speed
	if len(h.play.story.Actions) > 0 && (h.play.at == 0 || h.play.at == len(h.play.script())) {
		delay = max(delay, h.idx.Runtime.Settings().Duration("play.action-pause"))
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return playTick{gen} })
}

// onPlayTick plays one event when autoplay is on and schedules the next.
func (h *host) onPlayTick(t playTick) tea.Cmd {
	p := h.play
	if p == nil || !p.auto || t.gen != p.gen {
		return nil
	}
	p.step()
	if p.done() {
		p.auto = false
		return nil
	}
	return h.playTimer()
}

// renderPlay draws the play page: the script column on the left (event,
// what ran or what was asserted, coloured by outcome) and the story's
// frame boxed on the right, the sidebar taking what the frame leaves.
func (h *host) renderPlay(_ *ochazuke.Runtime, w, ht int) ochazuke.Block {
	s := ochazuke.NewStyles(*h.idx.Appearance)
	p := h.play
	if h.starting != nil && h.idx.Runtime.Settings().Bool("play.start-indicator") {
		return ochazuke.Text("Starting…")
	}
	if p == nil {
		return ochazuke.Text(s.Muted.Render("no story is playing"))
	}
	frame := ""
	if p.d != nil {
		frame = p.d.S.Frame()
	}
	sw, sh := p.story.Size()
	title := " ▶ " + p.story.Area + "/" + story.Slug(p.story.Name) + " "
	box := boxFrame(frame, title, fmt.Sprintf(" %dx%d ", sw, sh), sw)

	side := sideWidth(w, sw)
	tree := len(p.story.Actions) > 0 && h.idx.Runtime.Settings().Bool("play.tree")
	showCaptions := h.idx.Runtime.Settings().Bool("play.captions")
	var caption []string
	if showCaptions && !tree {
		if text := p.caption(); text != "" {
			caption = strings.Split(ansi.Wrap(text, max(1, w), ""), "\n")
			caption = append(caption, "")
		}
	}
	sidebar := p.sidebar(s, side, max(0, ht-len(caption)))
	if tree {
		sidebar = p.treeSidebar(s, side, ht, showCaptions)
	}
	body := beside(sidebar, box, side, w, max(0, ht-len(caption)))
	if len(caption) > 0 {
		body = strings.Join(caption, "\n") + "\n" + body
	}
	return ochazuke.Text(body)
}

func (p *player) caption() string {
	var parts []string
	notes := p.story.Notes
	if len(p.story.Actions) > 0 {
		if p.action < 0 {
			parts = append(parts, "Setup · reach the reset point")
		} else {
			a := p.story.Actions[p.action]
			parts = append(parts, fmt.Sprintf("Action %d/%d · %s", p.action+1, len(p.story.Actions), a.Label), a.Caption)
			notes = a.Notes
		}
	}
	for _, n := range notes {
		if n.After == p.at {
			parts = append(parts, n.Note)
		}
	}
	if p.at < len(p.script()) && p.script()[p.at].Note != "" {
		parts = append(parts, p.script()[p.at].Note)
	}
	return strings.Join(parts, "\n")
}

// sideWidth is the width a sidebar gets beside a boxed frame frameW wide:
// what the box leaves, at least 24 cells.
func sideWidth(w, frameW int) int {
	side := w - (frameW + 2) - 1
	if side < 24 {
		side = min(w, 24)
	}
	return side
}

// beside lays a sidebar side cells wide on the left of a boxed frame, ht
// rows of w cells; the box is clipped to what the sidebar leaves.
func beside(sidebar, box []string, side, w, ht int) string {
	right := w - side - 1
	rows := make([]string, ht)
	for i := range rows {
		line := layoutRow(sidebar, i, side)
		if right > 0 {
			line += " "
			if i < len(box) {
				line += ansi.Truncate(box[i], right, "")
			}
		}
		rows[i] = line
	}
	return strings.Join(rows, "\n")
}

// layoutRow is row i of rows padded or clipped to width.
func layoutRow(rows []string, i, width int) string {
	row := ""
	if i < len(rows) {
		row = rows[i]
	}
	row = ansi.Truncate(row, width, "")
	return row + strings.Repeat(" ", max(0, width-ansi.StringWidth(row)))
}

// boxFrame draws a frame of width w in a box with a title in the top rule
// and a footer in the bottom one.
func boxFrame(frame, title, foot string, w int) []string {
	title = ansi.Truncate(title, w, "…")
	box := []string{"┌" + title + strings.Repeat("─", max(0, w-ansi.StringWidth(title))) + "┐"}
	for _, row := range strings.Split(frame, "\n") {
		box = append(box, "│"+row+"│")
	}
	foot = ansi.Truncate(foot, w, "")
	return append(box, "└"+strings.Repeat("─", max(0, w-ansi.StringWidth(foot)))+foot+"┘")
}

// sidebar is the script column, height rows tall: a status line, the
// script with the current row marked and kept in view, and the current
// position's failures beneath.
func (p *player) sidebar(s ochazuke.Styles, width, height int) []string {
	rows := p.rows()
	cur := p.current()
	var status string
	switch {
	case p.recording:
		status = s.Error.Render("● rec") + s.Muted.Render(" · "+recordStop+" stops")
	case p.err != nil:
		status = s.Error.Render("stopped")
	case p.auto:
		status = s.Accent.Render("▶ auto") + s.Muted.Render(fmt.Sprintf(" every %s", p.speed))
	case p.done():
		status = s.Muted.Render("end")
	default:
		status = s.Muted.Render("paused")
	}
	head := s.Title.Render(ansi.Truncate(fmt.Sprintf("step %d/%d", p.at, len(p.script())), width, "")) + "  " + status
	out := []string{ansi.Truncate(head, width, "…"), ""}

	fails := p.failures()
	detail := min(len(fails), max(0, height-len(out)-4))
	avail := height - len(out) - detail
	if detail > 0 {
		avail-- // the blank line before the failures
	}
	if avail < 1 {
		return out
	}

	// The event column is as wide as the longest event, within reason.
	ew := 4
	for _, r := range rows {
		ew = max(ew, ansi.StringWidth(r.Event))
	}
	ew = min(ew, max(4, width/3))
	start := 0
	if cur >= avail {
		start = cur - avail + 1
	}
	start = min(start, max(0, len(rows)-avail))
	for i := start; i < len(rows) && i < start+avail; i++ {
		r := rows[i]
		marker := "  "
		if i == cur {
			marker = s.Accent.Render("▸ ")
		}
		event := ansi.Truncate(r.Event, ew, "…")
		event += strings.Repeat(" ", ew-ansi.StringWidth(event))
		if r.State == rowUnplayed {
			event = s.Muted.Render(event)
		}
		rest := max(0, width-2-ew-1)
		var note string
		switch {
		case r.Assert != "" && r.State == rowPassed:
			note = s.Accent.Render(ansi.Truncate("✓ "+r.Assert, rest, "…"))
		case r.Assert != "" && r.State == rowFailed:
			note = s.Error.Render(ansi.Truncate("✗ "+r.Assert, rest, "…"))
		case r.Assert != "":
			note = s.Muted.Render(ansi.Truncate("· "+r.Assert, rest, "…"))
		case r.Ran != "":
			note = s.Muted.Render(ansi.Truncate(r.Ran, rest, "…"))
		}
		out = append(out, marker+event+" "+note)
	}
	if detail > 0 {
		out = append(out, "")
		for _, f := range fails[:detail] {
			first, _, _ := strings.Cut(f, "\n")
			out = append(out, s.Error.Render(ansi.Truncate(first, width, "…")))
		}
	}
	return out
}

// treeSidebar keeps the same rows on screen across resets. A long action
// keeps its parent and caption above the scrolling children as context.
func (p *player) treeSidebar(s ochazuke.Styles, width, height int, captions bool) []string {
	rows, cur := p.tutorialRows()
	out := p.sidebar(s, width, 2)
	var lines []string
	starts := make([]int, len(rows)+1)
	parent := -1
	for i, row := range rows {
		starts[i] = len(lines)
		if row.Parent && i < cur {
			parent = i
		}
		label := row.Event
		switch {
		case row.Parent:
			label = "▾ " + label
		case row.Reset:
			label = "◆ " + label
		case row.Depth > 0:
			branch := "  ├ "
			if i+1 == len(rows) || rows[i+1].Parent {
				branch = "  └ "
			}
			label = branch + label
		}
		marker := "  "
		if i == cur {
			marker = "▸ "
		}
		line := ansi.Truncate(marker+label, width, "…")
		if i == cur {
			line = s.Accent.Reverse(true).Render(line)
		} else if row.Parent || row.Reset {
			line = s.Title.Render(line)
		} else if row.State == rowUnplayed {
			line = s.Muted.Render(line)
		}
		lines = append(lines, line)
		if row.Assert != "" {
			mark := "· "
			if row.State == rowPassed {
				mark = "✓ "
			} else if row.State == rowFailed {
				mark = "✗ "
			}
			lines = append(lines, s.Muted.Render(ansi.Truncate("    "+mark+row.Assert, width, "…")))
		}
		if captions && row.Detail != "" {
			indent := strings.Repeat(" ", 4+row.Depth*2)
			for _, detail := range strings.Split(ansi.Wrap(row.Detail, max(1, width-len(indent)), ""), "\n") {
				lines = append(lines, s.Muted.Render(indent+detail))
			}
		}
	}
	starts[len(rows)] = len(lines)
	fails := p.failures()
	detail := min(len(fails), max(0, height-len(out)-4))
	avail := max(0, height-len(out)-detail)
	if detail > 0 {
		avail = max(0, avail-1)
	}
	position := 0
	if cur >= 0 {
		position = starts[cur]
	}
	start := max(0, position-avail+1)
	if parent >= 0 && cur >= 0 && rows[cur].Depth > 0 && starts[parent] < start {
		// These are the same tree's ancestor lines, pinned only when they
		// would otherwise scroll out of view. Always leave room for the cursor.
		n := min(starts[parent+1]-starts[parent], max(0, avail/2))
		out = append(out, lines[starts[parent]:starts[parent]+n]...)
		avail -= n
		start = max(start, position-avail+1)
	}
	end := min(len(lines), start+avail)
	out = append(out, lines[start:end]...)
	if detail > 0 {
		out = append(out, "")
		for _, f := range fails[:detail] {
			first, _, _ := strings.Cut(f, "\n")
			out = append(out, s.Error.Render(ansi.Truncate(first, width, "…")))
		}
	}
	return out
}
