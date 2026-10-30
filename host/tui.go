package host

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/keytest"
)

// host is the TUI: an index of stories (itself an ochazuke page) and, while
// one is open, that story's session drawn in a box of the story's size in
// the middle of the terminal. Every story session is recorded; leaving it
// writes the recording into the history ring and asks for a name.
type host struct {
	lib   story.Library
	store story.Store
	idx   *ochazuke.Model
	field *ochazuke.Input
	want  int // index of the story the index asked to open, -1 none
	wantP int // index of the story the index asked to play, -1 none
	play  *player
	// playTick is a timer a play verb asked for (autoplay, a recording's
	// poll); Update collects it after the verb ran.
	playTick tea.Cmd
	w, h     int
	msg      string // the index's last message (saved as, kept in history)

	cur      *story.Session
	rec      *story.Recorder
	gen      int
	replay   *story.Recording
	replayAt int
	last     string // the recording just written to the ring
}

type outputMsg struct{ gen int }
type replayMsg struct{ gen, i int }
type startReplay struct{ rec *story.Recording }
type startStory struct{ s *story.Story }

func newHost(lib story.Library, title string) (*host, error) {
	h := &host{lib: lib, store: lib.Store, want: -1, wantP: -1, w: 80, h: 24}
	r := ochazuke.NewRuntime()
	h.idx = ochazuke.NewModel(r)
	h.idx.Title = title
	r.Define("entry").Columns(withoutArea(storyColumns)...).
		Verb("open", func(c ochazuke.Ctx) ochazuke.Result {
			i, ok := c.Instance.Data.(int)
			if !ok {
				return ochazuke.Failed(errors.New("not a story row"))
			}
			h.want = i
			return ochazuke.Done(nil)
		}, "Open the story in the real chrome; q leaves it").
		Verb("play", func(c ochazuke.Ctx) ochazuke.Result {
			i, ok := c.Instance.Data.(int)
			if !ok {
				return ochazuke.Failed(errors.New("not a story row"))
			}
			h.wantP = i
			return ochazuke.Done(nil)
		}, "Play the story's script step by step beside its frame; i records from any step").
		Mode("normal").
		Bind("enter", ochazuke.Do("entry.open")).
		Bind("p", ochazuke.Do("entry.play")).
		Bind("q", ochazuke.Do("app.quit"))
	h.field = ochazuke.NewInput("save-name", "", false)
	r.Define("field").Pin("insert").
		Verb("commit", func(r *ochazuke.Runtime, _ ochazuke.Context, _ ochazuke.Arguments) (any, error) {
			name := strings.TrimSpace(string(h.field.Text))
			if err := r.Pop(); err != nil {
				return nil, err
			}
			h.saveAs(name)
			return name, nil
		}, "Save the recording under the typed name (empty keeps it in the history ring)").
		Verb("cancel", func(r *ochazuke.Runtime, _ ochazuke.Context, _ ochazuke.Arguments) (any, error) {
			h.saveAs("")
			return nil, r.Pop()
		}, "Keep the recording in the history ring only").
		Mode("insert").
		Bind("enter", ochazuke.Do("field.commit")).
		Bind("esc", ochazuke.Do("field.cancel"))
	h.idx.ModalViews["save"] = ochazuke.ModalView{Title: "Recording", Render: func(_ *ochazuke.Runtime, body *ochazuke.Instance, w, _ int) ochazuke.Block {
		label := body.Payload.Prompt + " "
		field := ochazuke.InputBlock(h.field, max(1, w-len(label)), 1)
		return field.Offset(len(label), 0).WithText(label + field.Text)
	}}
	r.Instance("index", ochazuke.Kind("index"))
	var areas []string
	byArea := map[string][]ochazuke.ListItem{}
	for i, s := range lib.Stories {
		if _, ok := byArea[s.Area]; !ok {
			areas = append(areas, s.Area)
		}
		byArea[s.Area] = append(byArea[s.Area], ochazuke.ListItem{ID: fmt.Sprintf("s%d", i), Label: s.Name, Noun: "entry", Data: i, Cells: withoutArea(storyCells(s))})
	}
	for _, a := range areas {
		if _, err := r.AddList("area-"+a, byArea[a], ochazuke.Parent("index"), ochazuke.Columns(r.Noun("entry").Columns...)); err != nil {
			return nil, err
		}
	}
	render := func(r *ochazuke.Runtime, w, _ int) ochazuke.Block {
		s := ochazuke.NewStyles(*h.idx.Appearance)
		var parts []string
		if h.msg != "" {
			parts = append(parts, s.Subtitle.Render(ansi.Truncate(h.msg, w, "…")), "")
		}
		for i, a := range areas {
			if i > 0 {
				parts = append(parts, "")
			}
			l := r.List("area-" + a)
			parts = append(parts, areaHeading(s, a, l.Len(), w), indent(l.View(max(1, w-areaIndent), l.Height()+1), areaIndent))
		}
		return ochazuke.Text(strings.Join(parts, "\n"))
	}
	var opts []ochazuke.InstanceOption
	if len(lib.Stories) > 0 {
		opts = append(opts, ochazuke.Focus("s0"))
	}
	if _, err := r.AddPage("index", "Stories", render, opts...); err != nil {
		return nil, err
	}
	if err := h.definePlay(r); err != nil {
		return nil, err
	}
	if err := r.OpenPage("index"); err != nil {
		return nil, err
	}
	if _, err := r.ValidateBindings(); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *host) Init() tea.Cmd { return h.idx.Init() }

// open starts a story (or a recording's story, replayed live).
func (h *host) open(s *story.Story, replay *story.Recording) tea.Cmd {
	if len(s.Actions) > 0 && replay == nil {
		h.openPlay(s)
		return nil
	}
	resolved, prefix, err := h.lib.Resolve(s)
	if err != nil {
		h.msg = "open " + s.Name + ": " + err.Error()
		return nil
	}
	ss, err := h.lib.Build(resolved, story.Options{})
	if err != nil {
		h.msg = "open " + s.Name + ": " + err.Error()
		return nil
	}
	h.rec = story.NewRecorder(resolved, nil)
	if len(prefix) > 0 {
		// A trimmed story opens at its before-state: the base's events
		// played headless, kept at the head of the recording.
		d := story.NewDriver(ss, story.Options{})
		if err := d.Play(prefix, nil); err != nil {
			h.msg = "open " + s.Name + ": " + err.Error()
			ss.Close()
			return nil
		}
		ss = d.S
		h.rec.Rec.Events = append(h.rec.Rec.Events, prefix...)
	}
	h.cur, h.gen, h.replay, h.replayAt = ss, h.gen+1, replay, 0
	cmds := []tea.Cmd{story.Wrap(h.gen, ss.R.PendingCommands()), ss.M.Init()}
	if replay != nil && len(replay.Events) > 0 {
		cmds = append(cmds, h.replayNext(replay.Events[0].T))
	} else {
		cmds = append(cmds, h.outputTick())
	}
	return tea.Batch(cmds...)
}

// outputTick makes a live fake pane print a line a second.
func (h *host) outputTick() tea.Cmd {
	gen := h.gen
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return outputMsg{gen} })
}

func (h *host) replayNext(after int64) tea.Cmd {
	gen, i := h.gen, h.replayAt
	return tea.Tick(time.Duration(max(0, after))*time.Millisecond, func(time.Time) tea.Msg { return replayMsg{gen, i} })
}

// leave writes the recording into the ring and asks for a name.
func (h *host) leave() tea.Cmd {
	h.cur.Close()
	h.cur, h.replay = nil, nil
	h.gen++
	path, err := h.store.Push(&h.rec.Rec)
	if err != nil {
		h.msg = "recording not written: " + err.Error()
		return nil
	}
	h.askName(path)
	return nil
}

// askName opens the save-as prompt for the recording just written to path.
func (h *host) askName(path string) error {
	h.last = path
	h.msg = ""
	h.field.Text = nil
	h.field.Cursor = 0
	if _, err := h.idx.Runtime.OpenModal("save", ochazuke.Payload{Prompt: "save as:"}, ochazuke.WithInput(h.field, ochazuke.Kind("field"))); err != nil {
		h.msg = "kept in history: " + path
		return err
	}
	return nil
}

// saveAs moves the recording just written out of the ring under name, or
// leaves it in the ring when name is empty.
func (h *host) saveAs(name string) {
	if name == "" {
		h.msg = "kept in history (last " + fmt.Sprint(story.HistorySize) + "): " + h.last
		return
	}
	dest, err := h.store.Move(h.last, name)
	if err != nil {
		h.msg = "not saved (" + err.Error() + "); kept in history: " + h.last
		return
	}
	h.msg = "saved as " + dest
}

// deliver hands one message to the open story, recording it when it is an
// event, and follows quit (leave) and reset (rebuild).
func (h *host) deliver(msg tea.Msg) tea.Cmd {
	if ev := story.Classify(h.cur, msg); ev != nil {
		h.rec.Add(*ev)
	}
	cmd := h.cur.Update(msg)
	if h.cur.R.Quit {
		return h.leave()
	}
	if h.cur.Reset {
		ss, err := h.cur.Rebuild(false) // closes the old session (Close is idempotent)
		if err != nil {
			h.msg = err.Error()
			return h.leave()
		}
		h.cur, h.gen = ss, h.gen+1
		return tea.Batch(story.Wrap(h.gen, ss.Start()), h.outputTick())
	}
	return story.Wrap(h.gen, cmd)
}

func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		h.w, h.h = v.Width, v.Height
		_, cmd := h.idx.Update(v)
		return h, cmd
	case story.Tagged:
		if h.cur == nil || v.Gen != h.gen {
			return h, nil
		}
		if cmd, ok := story.Unbatch(v); ok {
			return h, cmd
		}
		if _, quit := v.Msg.(tea.QuitMsg); quit {
			return h, nil
		}
		return h, h.deliver(v.Msg)
	case startReplay:
		return h, h.open(v.rec.Initial, v.rec)
	case startStory:
		return h, h.open(v.s, nil)
	case outputMsg:
		if h.cur == nil || v.gen != h.gen || h.replay != nil {
			return h, nil
		}
		if h.cur.HasPane() {
			h.rec.Add(story.Event{Tick: "output"})
			h.cur.Emit()
		}
		return h, h.outputTick()
	case replayMsg:
		if h.cur == nil || v.gen != h.gen || h.replay == nil {
			return h, nil
		}
		return h, h.replayStep()
	case playTick:
		return h, h.onPlayTick(v)
	case recordTick:
		return h, h.onRecordTick(v)
	case tea.KeyPressMsg, tea.PasteMsg:
		if h.cur != nil {
			return h, h.deliver(msg)
		}
		if h.play != nil && h.play.recording {
			h.recordKey(msg)
			return h, nil
		}
	case tea.BackgroundColorMsg:
		if h.cur != nil {
			h.cur.M.Update(v)
		}
	}
	_, cmd := h.idx.Update(msg)
	if h.idx.Runtime.Quit {
		return h, tea.Quit
	}
	if h.want >= 0 {
		s := h.lib.Stories[h.want]
		h.want = -1
		return h, tea.Batch(cmd, h.open(s, nil))
	}
	if h.wantP >= 0 {
		s := h.lib.Stories[h.wantP]
		h.wantP = -1
		h.openPlay(s)
	}
	if h.playTick != nil {
		cmd, h.playTick = tea.Batch(cmd, h.playTick), nil
	}
	return h, cmd
}

// replayStep plays the next recorded event: keys, text and output lines
// are sent; job deliveries and timer ticks happen by themselves.
func (h *host) replayStep() tea.Cmd {
	events := h.replay.Events
	ev := events[h.replayAt]
	gen := h.gen
	var cmds []tea.Cmd
	switch {
	case ev.Key != "":
		cmds = append(cmds, h.deliver(keytest.Msg(ev.Key, false, false)))
	case ev.Type != "":
		for _, r := range ev.Type {
			cmds = append(cmds, h.deliver(keytest.Msg(string(r), true, false)))
		}
	case ev.Paste != "":
		cmds = append(cmds, h.deliver(keytest.Msg(ev.Paste, false, true)))
	case ev.Tick == "output":
		h.rec.Add(story.Event{Tick: "output"})
		h.cur.Emit()
	}
	if h.cur == nil || h.gen != gen {
		return tea.Batch(cmds...)
	}
	h.replayAt++
	if h.replayAt >= len(events) {
		h.replay = nil
		return tea.Batch(append(cmds, h.outputTick())...)
	}
	return tea.Batch(append(cmds, h.replayNext(events[h.replayAt].T-ev.T))...)
}

// View draws the index, or the open story boxed at its own size in the
// middle of the terminal.
func (h *host) View() tea.View {
	if h.cur == nil {
		return h.idx.View()
	}
	frame := h.cur.Frame()
	w, ht := h.cur.Story.Size()
	title := " ● rec " + h.cur.Story.Area + "/" + story.Slug(h.cur.Story.Name) + " "
	if h.replay != nil {
		title = fmt.Sprintf(" ▶ replay %d/%d ", h.replayAt, len(h.replay.Events))
	}
	box := boxFrame(frame, title, fmt.Sprintf(" %dx%d ", w, ht), w)
	left, top := max(0, (h.w-w-2)/2), max(0, (h.h-ht-2)/2)
	lines := make([]string, top, top+len(box))
	pad := strings.Repeat(" ", left)
	for _, l := range box {
		lines = append(lines, pad+l)
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	if c := h.cur.Cursor(); c != nil {
		moved := *c
		moved.X, moved.Y = c.X+left+1, c.Y+top+1
		v.Cursor = &moved
	}
	return v
}

// areaIndent is how far a section's rows sit in from its heading.
const areaIndent = 2

// areaHeading is one section's title row: the area in the accent, a muted
// rule filling the line, and the story count at the right edge.
func areaHeading(s ochazuke.Styles, area string, n, width int) string {
	title := s.Accent.Render(strings.ToUpper(area))
	count := s.Muted.Render(fmt.Sprintf("%d", n))
	rule := width - ansi.StringWidth(title) - ansi.StringWidth(count) - 2
	if rule < 1 {
		return ansi.Truncate(title+" "+count, width, "…")
	}
	return title + " " + s.Muted.Render(strings.Repeat("─", rule)) + " " + count
}

// indent shifts every line of text right by n cells.
func indent(text string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}
