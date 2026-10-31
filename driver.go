package monogatari

import (
	"errors"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/keytest"
	"ofam.dev/ochazuke/markdown"
)

// Tagged is a message from a session's command, tagged with the session's
// generation so a message of a session that was reset or left never
// reaches its successor (job ids restart with each runtime).
type Tagged struct {
	Gen int
	Msg tea.Msg
}

// Wrap tags every message cmd produces with gen; a batch is unwrapped by
// the receiver (Unbatch) and its parts wrapped again.
func Wrap(gen int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return Tagged{Gen: gen, Msg: cmd()} }
}

// Unbatch turns a tagged batch into the batch of its wrapped parts.
func Unbatch(t Tagged) (tea.Cmd, bool) {
	batch, ok := t.Msg.(tea.BatchMsg)
	if !ok {
		return nil, false
	}
	var cmds []tea.Cmd
	for _, c := range batch {
		cmds = append(cmds, Wrap(t.Gen, c))
	}
	return tea.Batch(cmds...), true
}

// Classify names the event a message is, before it is delivered: a key, a
// paste, a current job's completion, a pane frame, a framework timer. Other
// messages (window size, colours) are not events.
func Classify(ss *Session, msg tea.Msg) *Event {
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		return &Event{Key: v.String()}
	case tea.PasteMsg:
		return &Event{Paste: v.Content}
	case ochazuke.Completion:
		if !ss.R.Accepts(v.Ticket) {
			return nil
		}
		if ss.R.Terminal(v.Owner) != nil {
			return &Event{Tick: "terminal"}
		}
		for _, j := range ss.R.Jobs() {
			if j.Job == v.Job {
				state := ochazuke.JobDone
				if v.Err != nil {
					state = ochazuke.JobFailed
				}
				return &Event{Job: ss.R.DisplayKey(j.Verb), State: state} // recorded by its short name
			}
		}
	case ochazuke.JobLingerMsg:
		return &Event{Tick: "linger"}
	case ochazuke.SegmentTickMsg, ochazuke.SegmentMsg:
		return &Event{Tick: "segment"}
	case ochazuke.BindingTimeoutMsg:
		return &Event{Tick: "binding"}
	case ochazuke.TreeDebounceMsg:
		if t := ss.R.Tree(v.Tree); t != nil && t.DebounceCurrent(v) {
			return &Event{Tick: "debounce"}
		}
	case ochazuke.RecordDebounceMsg:
		if e := ss.R.RecordEditor(v.Editor); e != nil && e.DebounceCurrent(v) {
			return &Event{Tick: "debounce"}
		}
	case markdown.DebounceMsg:
		if doc := markdown.Of(ss.R, v.Doc); doc != nil && doc.DebounceCurrent(v) {
			return &Event{Tick: "debounce"}
		}
	case ochazuke.CompletionDebounceMsg:
		if c := ss.R.Completer(); c != nil && c.DebounceCurrent(v) {
			return &Event{Tick: "debounce"}
		}
	}
	return nil
}

// Driver plays events against a session with no terminal: commands run on
// goroutines and their messages wait in a pool; a job or tick event takes
// the first matching message from the pool (waiting for it) and delivers
// it, so nothing reaches the model except through an event, in order. The
// model runs on the driver's fake clock (Model.Now), which stands still
// between events; a linger tick advances it to the timer's deadline and
// delivers the timer's message at once (Model.JobLingerTick), so a story
// never waits JobLinger.
type Driver struct {
	S    *Session
	Opt  Options
	Wait time.Duration // how long a job or tick event waits for its message
	Quit bool          // the script quit the story (q, ctrl+c)

	// Trace is one Step per event played: the mode before and after and
	// the top-level noun.verb requests the event ran.
	Trace []Step

	gen     int
	lastSeq uint64
	now     time.Time // the fake clock the session's model reads
	mu      sync.Mutex
	pool    []Tagged
	arrive  chan struct{}
}

// NewDriver starts driving a built session.
func NewDriver(ss *Session, opt Options) *Driver {
	d := &Driver{S: ss, Opt: opt, Wait: 6 * time.Second, arrive: make(chan struct{}, 1), gen: 1, now: time.Unix(0, 0).UTC()}
	d.useClock()
	d.skipHistory()
	d.run(ss.Start())
	return d
}

// Close ends the session's fake goroutines.
func (d *Driver) Close() { d.S.Close() }

func (d *Driver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	gen := d.gen
	go func() { d.push(Tagged{Gen: gen, Msg: cmd()}) }()
}

func (d *Driver) push(t Tagged) {
	if batch, ok := t.Msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				gen := t.Gen
				go func() { d.push(Tagged{Gen: gen, Msg: c()}) }()
			}
		}
		return
	}
	if t.Msg == nil {
		return
	}
	d.mu.Lock()
	d.pool = append(d.pool, t)
	d.mu.Unlock()
	select {
	case d.arrive <- struct{}{}:
	default:
	}
}

func (d *Driver) update(msg tea.Msg) {
	cmd := d.S.Update(msg)
	if d.S.R.Quit {
		d.Quit = true
		return
	}
	d.run(cmd)
}

// take delivers the first pooled message of this generation that match
// accepts, waiting up to Wait for one.
func (d *Driver) take(what string, match func(tea.Msg) bool) error {
	deadline := time.After(d.Wait)
	for {
		d.mu.Lock()
		// A foreground program's request is the host's to answer (the
		// Model runs it through its Foreground hook): answered while
		// waiting, so its completion can arrive.
		for i, t := range d.pool {
			if _, ok := t.Msg.(ochazuke.ForegroundMsg); ok && t.Gen == d.gen {
				d.pool = append(d.pool[:i:i], d.pool[i+1:]...)
				d.mu.Unlock()
				d.update(t.Msg)
				d.mu.Lock()
				break
			}
		}
		for i, t := range d.pool {
			if t.Gen == d.gen && match(t.Msg) {
				d.pool = append(d.pool[:i:i], d.pool[i+1:]...)
				d.mu.Unlock()
				d.update(t.Msg)
				return nil
			}
		}
		d.mu.Unlock()
		select {
		case <-d.arrive:
		case <-deadline:
			return fmt.Errorf("no %s arrived within %s", what, d.Wait)
		}
	}
}

// Step is what one event did: the mode it was read in, the mode after it
// and the top-level requests it ran (noun.verb in the mode each ran in).
type Step struct {
	Event  Event
	Before string
	After  string
	Ran    []Ran
}

// Ran is one request a step ran.
type Ran struct {
	Mode string
	Verb string // noun.verb
	OK   bool
}

// Step plays one event and appends its Step to Trace.
func (d *Driver) Step(e Event) error {
	if d.Quit {
		return nil
	}
	return d.traced(e, func() error { return d.step(e) })
}

// Arrived delivers every pooled message of this generation that is an
// event (a job's completion, a pane frame, a framework timer) in arrival
// order, each as a traced step, and returns the events it played. A live
// recorder calls it between keys so the session follows its jobs without
// a script naming them; a scripted run never needs it.
func (d *Driver) Arrived() []Event {
	d.mu.Lock()
	var msgs []tea.Msg
	kept := d.pool[:0]
	for _, t := range d.pool {
		if t.Gen == d.gen && Classify(d.S, t.Msg) != nil {
			msgs = append(msgs, t.Msg)
		} else {
			kept = append(kept, t)
		}
	}
	d.pool = kept
	d.mu.Unlock()
	var events []Event
	for _, msg := range msgs {
		if d.Quit {
			break
		}
		ev := Classify(d.S, msg)
		if ev == nil {
			continue
		}
		d.traced(*ev, func() error { d.update(msg); return nil })
		events = append(events, *ev)
	}
	return events
}

// traced plays e through play and appends what it did to Trace.
func (d *Driver) traced(e Event, play func() error) error {
	before := d.S.R.Mode.Name
	err := play()
	step := Step{Event: e, Before: before, After: d.S.R.Mode.Name}
	for _, h := range d.S.R.History(0) {
		if h.Seq <= d.lastSeq {
			continue
		}
		d.lastSeq = h.Seq
		if h.Depth == 0 {
			step.Ran = append(step.Ran, Ran{Mode: h.Context.Mode, Verb: d.S.R.DisplayKey(h.Request.Key()), OK: h.Result.OK}) // by its short name
		}
	}
	d.Trace = append(d.Trace, step)
	return err
}

func (d *Driver) step(e Event) error {
	switch {
	case e.Key != "":
		d.update(keytest.Msg(e.Key, false, false))
	case e.Type != "":
		for _, r := range e.Type {
			if d.Quit {
				break
			}
			d.update(keytest.Msg(string(r), true, false))
		}
	case e.Paste != "":
		d.update(keytest.Msg(e.Paste, false, true))
	case e.Job != "":
		err := d.take("completion of "+e.Job, func(msg tea.Msg) bool {
			ev := Classify(d.S, msg)
			return ev != nil && d.S.R.CanonicalKey(ev.Job) == d.S.R.CanonicalKey(e.Job)
		})
		if err != nil {
			return err
		}
	case e.Restart:
		return d.rebuild(true)
	case e.Tick == "output":
		if !d.S.Emit() {
			return errors.New("tick output: no fake pane is live")
		}
	case e.Tick == "linger":
		tick, ok := d.S.M.JobLingerTick()
		if !ok {
			return errors.New("tick linger: no linger timer is scheduled")
		}
		if tick.At.After(d.now) {
			d.now = tick.At
		}
		d.update(tick)
	case e.Tick == "segment":
		// The next refresh: the clock moves to the timer's deadline (the
		// first refresh needs none), the due segments start, and every
		// answer is delivered before the next event.
		d.S.R.EnsureSegments()
		if tick, ok := d.S.M.SegmentTick(); ok && tick.At.After(d.now) {
			d.now = tick.At
		}
		tick, _ := d.S.M.SegmentTick()
		d.update(tick)
		for d.S.M.SegmentsInFlight() > 0 {
			if err := d.take("segment answer", func(msg tea.Msg) bool { _, ok := msg.(ochazuke.SegmentMsg); return ok }); err != nil {
				return err
			}
		}
	case e.Tick == "terminal" || e.Tick == "binding" || e.Tick == "debounce":
		if err := d.take(e.Tick+" tick", func(msg tea.Msg) bool {
			ev := Classify(d.S, msg)
			return ev != nil && ev.Tick == e.Tick
		}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown event %s", e)
	}
	if d.S.Reset {
		return d.reset()
	}
	return nil
}

// reset rebuilds the session from its story (Env.Reset, a reset verb).
func (d *Driver) reset() error { return d.rebuild(false) }

// rebuild builds the session again from its story through its app's
// fixture; a restart keeps the state directory, a reset starts with a new
// one.
func (d *Driver) rebuild(restart bool) error {
	ss, err := d.S.Rebuild(restart)
	if err != nil {
		return err
	}
	d.S = ss
	d.useClock()
	d.gen++
	d.lastSeq = 0
	d.skipHistory()
	d.run(ss.Start())
	return nil
}

// useClock points the session's model at the driver's fake clock.
func (d *Driver) useClock() { d.S.M.Now = func() time.Time { return d.now } }

// skipHistory marks the requests made while building (job starts) as seen.
func (d *Driver) skipHistory() {
	for _, h := range d.S.R.History(0) {
		d.lastSeq = max(d.lastSeq, h.Seq)
	}
}

// Play runs events in order, calling after (when set) with the count of
// events played so far after each one.
func (d *Driver) Play(events []Event, after func(n int) error) error {
	for i, e := range events {
		if err := d.Step(e); err != nil {
			return fmt.Errorf("event %d (%s): %w", i+1, e, err)
		}
		if after != nil {
			if err := after(i + 1); err != nil {
				return err
			}
		}
	}
	return nil
}
