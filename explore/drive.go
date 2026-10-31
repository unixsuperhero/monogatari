package explore

import (
	"fmt"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/keytest"
)

// Action names. A key action is the key's binding name ("j", "ctrl+a",
// "esc"); the others are spelled in angle brackets so no key collides.
const (
	Wait  = "<wait>"  // every framework timer due now: sequence timeout, debounces, the linger tick; and commands that were still running
	Paste = "[paste]" // a bracketed paste of the text "pasted"
)

// PasteText is the text a Paste action pastes.
const PasteText = "pasted"

// Resize is the action that resizes the terminal to w x h.
func Resize(w, h int) string { return fmt.Sprintf("<resize %dx%d>", w, h) }

// commandWindow is how long the driver waits for the commands one message
// produced before it records the ones still running as blocked. A fake
// answers at once or never (a stream waiting on output, a job waiting on a
// key), so the window only separates those two; blocked commands are
// waited on again at the next Wait, never between keys, so a late answer
// cannot change what a key sees.
const commandWindow = 150 * time.Millisecond

// maxMessages bounds the messages one action may deliver; a chain of
// commands longer than this is reported, not followed.
const maxMessages = 256

// timerNames are the closures that only sleep before producing a message
// (tea.Tick, tea.Every, the sequence timer). The driver never runs them:
// Wait delivers the framework's timers through their Tick accessors, so no
// action waits on the wall clock. An application's own timers are counted
// and otherwise dropped (Report.Timers).
var timerNames = []string{"bubbletea/v2.Tick.", "bubbletea/v2.Every.", "(*Runtime).BindingTimeoutCommand."}

// session is one replay of a subject: a fresh model driven action by
// action, with its commands run inline.
type session struct {
	s       *Subject
	m       *ochazuke.Model
	p       tea.Model // Subject.Program over m; nil drives m
	now     time.Time
	width   int
	height  int
	blocked []*pendingCmd
	quit    bool

	// set by the last action
	panic      *panicInfo
	overrun    bool
	timers     []string
	newBlocked []string // commands the last action left running past the window
	lastSeq    uint64
	ran        []ochazuke.HistoryEntry
	status     string
}

type pendingCmd struct {
	name string
	done chan tea.Msg
	gid  chan uint64 // the goroutine running it, sent once it starts
	id   uint64
}

type panicInfo struct {
	Where string // "update", "view", "new", "check"
	Value string
	Stack string
}

func cmdName(cmd tea.Cmd) string {
	if f := runtime.FuncForPC(reflect.ValueOf(cmd).Pointer()); f != nil {
		return f.Name()
	}
	return "?"
}

func isTimer(name string) bool {
	for _, t := range timerNames {
		if strings.Contains(name, t) {
			return true
		}
	}
	return false
}

// start builds a fresh session at the subject's size (80x24) on the fake
// clock and runs Init.
func start(s *Subject) (ss *session, err error) {
	ss = &session{s: s, now: time.Unix(1_000_000, 0).UTC(), width: 80, height: 24}
	if s.Size[0] > 0 && s.Size[1] > 0 {
		ss.width, ss.height = s.Size[0], s.Size[1]
	}
	defer func() {
		if v := recover(); v != nil {
			ss.panic = &panicInfo{Where: "new", Value: fmt.Sprint(v), Stack: trimStack(debug.Stack())}
			err = nil
		}
	}()
	m, err := s.New()
	if err != nil {
		return nil, err
	}
	if m == nil || m.Runtime == nil {
		return nil, fmt.Errorf("explore: %s: New returned no model", s.Name)
	}
	ss.m = m
	ss.wrap()
	m.Now = func() time.Time { return ss.now }
	// A long sequence timeout: a sequence ends by its next key, or by Wait
	// (BindingTimeoutTick), never by the wall clock between two keys.
	m.Runtime.BindingTimeout = time.Hour
	ss.markHistory()
	ss.deliver(ss.program().Init())
	ss.update(tea.WindowSizeMsg{Width: ss.width, Height: ss.height})
	ss.markHistory()
	ss.timers = nil
	return ss, nil
}

// wrap puts the subject's program over the session's model.
func (ss *session) wrap() {
	ss.p = nil
	if ss.s.Program != nil {
		ss.p = ss.s.Program(ss.m)
	}
}

// program is what the session drives: the subject's program, else the
// model.
func (ss *session) program() tea.Model {
	if ss.p != nil {
		return ss.p
	}
	return ss.m
}

// updateProgram delivers one message to the program, keeping the model it
// returns.
func (ss *session) updateProgram(msg tea.Msg) tea.Cmd {
	if ss.p == nil {
		_, cmd := ss.m.Update(msg)
		return cmd
	}
	p, cmd := ss.p.Update(msg)
	if p != nil {
		ss.p = p
	}
	return cmd
}

// close cancels what the session still runs: the subject's Close, else
// every instance under app is removed, which cancels its jobs (and so the
// blocked reads of fake streams).
func (ss *session) close() {
	if ss == nil || ss.m == nil {
		return
	}
	defer func() { _ = recover() }()
	if ss.s.Close != nil {
		ss.s.Close(ss.m)
		return
	}
	r := ss.m.Runtime
	for _, id := range sortedKeys(r.Instances) {
		if inst := r.Instances[id]; inst != nil && inst.Parent == "app" {
			_ = r.RemoveInstance(id)
		}
	}
}

func (ss *session) markHistory() {
	for _, h := range ss.m.Runtime.History(0) {
		ss.lastSeq = max(ss.lastSeq, h.Seq)
	}
}

// trimStack keeps a stack's frames without what changes between runs (the
// goroutine number, argument words, pc offsets), at most 40 lines.
func trimStack(b []byte) string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "goroutine ") || line == "" {
			continue
		}
		if i := strings.LastIndex(line, " +0x"); i >= 0 {
			line = line[:i]
		}
		if !strings.HasPrefix(line, "\t") {
			if i := strings.LastIndex(line, "("); i > 0 && strings.HasSuffix(line, ")") {
				line = line[:i] + "(…)"
			}
		}
		if strings.HasPrefix(line, "created by ") {
			if i := strings.Index(line, " in goroutine "); i >= 0 {
				line = line[:i]
			}
		}
		out = append(out, line)
		if len(out) >= 40 {
			break
		}
	}
	return strings.Join(out, "\n")
}

// update delivers one message and renders a frame, as Bubble Tea does
// after every message (motions read the last frame's drawing order). A
// panic in either is recorded and ends the session's usefulness.
func (ss *session) update(msg tea.Msg) {
	if ss.panic != nil || ss.quit {
		return
	}
	var cmd tea.Cmd
	func() {
		defer func() {
			if v := recover(); v != nil {
				ss.panic = &panicInfo{Where: "update", Value: fmt.Sprint(v), Stack: trimStack(debug.Stack())}
			}
		}()
		cmd = ss.updateProgram(msg)
	}()
	if ss.panic != nil {
		return
	}
	if ss.m.Runtime.Quit {
		ss.quit = true
		return
	}
	if _, err := ss.render(ss.width, ss.height); err != nil {
		return
	}
	ss.deliver(cmd)
}

// render draws one frame at w x h, recovering a panic.
func (ss *session) render(w, h int) (view tea.View, err error) {
	defer func() {
		if v := recover(); v != nil {
			ss.panic = &panicInfo{Where: "view", Value: fmt.Sprint(v), Stack: trimStack(debug.Stack())}
			err = fmt.Errorf("panic in View: %v", v)
		}
	}()
	ow, oh := ss.m.Width, ss.m.Height
	ss.m.Width, ss.m.Height = w, h
	// A frame is an explicit snapshot: timers are dropped here, so the frame
	// tick a coalescing model (ochazuke's render.max-fps) schedules never
	// arrives. Older ochazuke has no InvalidateRender and needs none.
	if f, ok := any(ss.m).(interface{ InvalidateRender() }); ok {
		f.InvalidateRender()
	}
	view = ss.program().View()
	ss.m.Width, ss.m.Height = ow, oh
	return view, nil
}

// deliver runs a command and every command its messages produce, in the
// order they were issued: the parts of a batch run concurrently, the
// driver waits up to commandWindow for all of them, then delivers the
// messages of the ones that finished in issue order. Timers are dropped
// (see timerNames); the commands still running are kept for Wait.
func (ss *session) deliver(cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	delivered := 0
	for len(queue) > 0 && ss.panic == nil && !ss.quit {
		batch := queue
		queue = nil
		msgs := ss.runAll(flatten(batch))
		for _, msg := range msgs {
			if delivered >= maxMessages {
				ss.overrun = true
				return
			}
			delivered++
			if _, ok := msg.(tea.QuitMsg); ok {
				ss.quit = true
				return
			}
			ss.updateCollect(msg, &queue)
		}
	}
}

// updateCollect is update that queues the produced command instead of
// running it at once, so one generation of commands runs together.
func (ss *session) updateCollect(msg tea.Msg, queue *[]tea.Cmd) {
	if ss.panic != nil || ss.quit {
		return
	}
	var cmd tea.Cmd
	func() {
		defer func() {
			if v := recover(); v != nil {
				ss.panic = &panicInfo{Where: "update", Value: fmt.Sprint(v), Stack: trimStack(debug.Stack())}
			}
		}()
		cmd = ss.updateProgram(msg)
	}()
	if ss.panic != nil {
		return
	}
	if ss.m.Runtime.Quit {
		ss.quit = true
		return
	}
	if _, err := ss.render(ss.width, ss.height); err != nil {
		return
	}
	if cmd != nil {
		*queue = append(*queue, cmd)
	}
}

// flatten expands batches without running anything but the batch
// closures themselves (which only return their parts).
func flatten(cmds []tea.Cmd) []tea.Cmd {
	var out []tea.Cmd
	for _, c := range cmds {
		if c == nil {
			continue
		}
		if strings.Contains(cmdName(c), "bubbletea/v2.Batch.") {
			if batch, ok := c().(tea.BatchMsg); ok {
				out = append(out, flatten(batch)...)
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// runAll runs the commands concurrently and returns the messages of those
// that finished within commandWindow, in issue order; batches they return
// are flattened in place.
func (ss *session) runAll(cmds []tea.Cmd) []tea.Msg {
	var started []*pendingCmd
	for _, c := range cmds {
		name := cmdName(c)
		if isTimer(name) {
			if !strings.Contains(name, "BindingTimeoutCommand") {
				ss.timers = append(ss.timers, name)
			}
			continue
		}
		p := &pendingCmd{name: name, done: make(chan tea.Msg, 1), gid: make(chan uint64, 1)}
		go func(c tea.Cmd) {
			p.gid <- goid()
			defer func() {
				if v := recover(); v != nil {
					p.done <- cmdPanic{fmt.Sprint(v), trimStack(debug.Stack())}
				}
			}()
			p.done <- c()
		}(c)
		started = append(started, p)
	}
	return ss.collect(started, commandWindow)
}

type cmdPanic struct{ value, stack string }

// collect waits for every pending command until it finishes or parks, at
// most window, and returns, in order, the messages of the finished ones;
// the rest are kept blocked. A command parks when its goroutine sits in a
// blocking state (a channel, a select, a condition, a sleep) on three polls
// in a row: a fake stream waiting for output, a fake job waiting for a
// key. A command in a syscall or still running is waited on, up to window.
func (ss *session) collect(pending []*pendingCmd, window time.Duration) []tea.Msg {
	if len(pending) == 0 {
		return nil
	}
	results := make([]tea.Msg, len(pending))
	finished := make([]bool, len(pending))
	parked := make([]int, len(pending))
	take := func() int {
		left := 0
		for i, p := range pending {
			if finished[i] {
				continue
			}
			select {
			case msg := <-p.done:
				results[i], finished[i] = msg, true
			default:
				left++
			}
		}
		return left
	}
	deadline := time.Now().Add(window)
	for wait := 50 * time.Microsecond; take() > 0 && time.Now().Before(deadline); {
		for _, p := range pending {
			if p.id == 0 {
				select {
				case p.id = <-p.gid:
				default:
				}
			}
		}
		states := goroutineStates()
		settled := true
		for i, p := range pending {
			if finished[i] {
				continue
			}
			if p.id != 0 && blockingState(states[p.id]) {
				parked[i]++
			} else {
				parked[i] = 0
			}
			if parked[i] < 3 {
				settled = false
			}
		}
		if settled {
			break
		}
		time.Sleep(wait)
		wait = min(wait*2, time.Millisecond)
	}
	take()
	var out []tea.Msg
	for i, p := range pending {
		if !finished[i] {
			ss.blocked = append(ss.blocked, p)
			ss.newBlocked = append(ss.newBlocked, p.name)
			continue
		}
		msg := results[i]
		if cp, ok := msg.(cmdPanic); ok {
			ss.panic = &panicInfo{Where: "command " + p.name, Value: cp.value, Stack: cp.stack}
			return out
		}
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			out = append(out, ss.runAll(flatten(batch))...)
			continue
		}
		out = append(out, msg)
	}
	return out
}

// act plays one action and records what it ran: the history entries it
// added (Runtime.History) and the status line it left.
func (ss *session) act(action string) {
	ss.ran, ss.overrun, ss.timers, ss.newBlocked = nil, false, nil, nil
	if ss.panic != nil || ss.quit {
		return
	}
	switch {
	case action == Wait:
		ss.wait()
	case action == Paste:
		ss.update(keytest.Msg(PasteText, false, true))
	case strings.HasPrefix(action, "<resize "):
		w, h := parseSize(action)
		ss.width, ss.height = w, h
		ss.update(tea.WindowSizeMsg{Width: w, Height: h})
	default:
		ss.update(keytest.Msg(action, false, false))
	}
	if ss.m == nil {
		return
	}
	for _, h := range ss.m.Runtime.History(0) {
		if h.Seq > ss.lastSeq {
			ss.ran = append(ss.ran, h)
			ss.lastSeq = h.Seq
		}
	}
	ss.status = ss.m.Status
}

func parseSize(action string) (int, int) {
	spec := strings.TrimSuffix(strings.TrimPrefix(action, "<resize "), ">")
	ws, hs, _ := strings.Cut(spec, "x")
	w, _ := strconv.Atoi(ws)
	h, _ := strconv.Atoi(hs)
	return max(1, w), max(1, h)
}

// wait is time passing with no key: the commands still running get one
// more window, then every framework timer that is due fires, in a fixed
// order, until none is (bounded).
func (ss *session) wait() {
	for round := 0; round < 8 && ss.panic == nil && !ss.quit; round++ {
		progressed := false
		if blocked := ss.blocked; len(blocked) > 0 {
			ss.blocked = nil
			msgs := ss.collect(blocked, commandWindow)
			for _, msg := range msgs {
				progressed = true
				var queue []tea.Cmd
				ss.updateCollect(msg, &queue)
				for _, c := range queue {
					ss.deliver(c)
				}
			}
		}
		r := ss.m.Runtime
		if tick, ok := r.BindingTimeoutTick(); ok {
			ss.update(tick)
			progressed = true
		}
		if c := r.Completer(); c != nil {
			if tick, ok := c.DebounceTick(); ok {
				ss.update(tick)
				progressed = true
			}
		}
		for _, id := range sortedKeys(r.Instances) {
			if ss.panic != nil || ss.quit {
				return
			}
			if t := r.Tree(id); t != nil {
				if tick, ok := t.DebounceTick(); ok {
					ss.update(tick)
					progressed = true
				}
			}
			if e := r.RecordEditor(id); e != nil {
				if tick, ok := e.DebounceTick(); ok {
					ss.update(tick)
					progressed = true
				}
			}
		}
		if tick, ok := ss.m.JobLingerTick(); ok {
			if tick.At.After(ss.now) {
				ss.now = tick.At
			}
			ss.update(tick)
			progressed = true
		}
		if !progressed {
			return
		}
	}
}

// screen is the frame at the session's size as plain text.
func (ss *session) screen() string {
	view, err := ss.render(ss.width, ss.height)
	if err != nil {
		return ""
	}
	return ansi.Strip(view.Content)
}

// goid is the calling goroutine's id, read from its stack header.
func goid() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	f := strings.Fields(string(buf[:n]))
	if len(f) < 2 {
		return 0
	}
	id, _ := strconv.ParseUint(f[1], 10, 64)
	return id
}

// goroutineStates reads every goroutine's state from a full stack dump.
func goroutineStates() map[uint64]string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	out := map[uint64]string{}
	for _, line := range strings.Split(string(buf), "\n") {
		rest, ok := strings.CutPrefix(line, "goroutine ")
		if !ok {
			continue
		}
		idText, state, ok := strings.Cut(rest, " [")
		if !ok {
			continue
		}
		id, err := strconv.ParseUint(idText, 10, 64)
		if err != nil {
			continue
		}
		state, _, _ = strings.Cut(state, "]")
		state, _, _ = strings.Cut(state, ",")
		out[id] = state
	}
	return out
}

// blockingState reports a goroutine state that waits on another goroutine
// or a timer, not on the machine.
func blockingState(state string) bool {
	for _, s := range []string{"chan receive", "chan send", "select", "sleep", "sync.Cond.Wait", "sync.WaitGroup.Wait"} {
		if strings.HasPrefix(state, s) {
			return true
		}
	}
	return false
}
