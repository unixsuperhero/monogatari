package monogatari

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// App is an application stories can build; every host registers its own
// (Library.Apps). monogatari has no built-in app.
type App struct {
	Name    string
	Summary string
	// Fixtures are the app's named starting states; the first is the
	// default for a story that names none.
	Fixtures []Fixture
	// Fields are the top-level story fields the app reads beyond the
	// story format's own (the demo's page and pages, Story.Fields). A
	// story with a field its app does not declare fails to build.
	Fields []string
	// Expected reports an operation error the app means (a refusal of its
	// fakes), which the explorer must not report; nil is none.
	Expected func(op, err string) bool
}

// Fixture is one named starting state of an app.
type Fixture struct {
	Name, Summary string
	// Build makes the app's runtime for a story. It reads only what Env
	// gives it: nothing else on the machine.
	Build func(Env) (Built, error)
}

// Env is everything a fixture may use: the story, its app data and the
// temp directories the host made for it.
type Env struct {
	Story *Story
	// Raw is the whole story document (Story's own fields and Fields).
	Raw  json.RawMessage
	Seed json.RawMessage
	// StateDir is the session's state directory (Story.State; kept across
	// a restart), ConfigDir the temp directory the story's config files
	// are written to (Story.Config), FilesDir its file tree (Story.Files);
	// each "" when the story has none.
	StateDir, ConfigDir, FilesDir string
	// Now is the session's clock: the driver's fake clock once a driver
	// runs the session, the epoch before.
	Now           func() time.Time
	Width, Height int
	Options       Options
	// Reset asks the host to rebuild the session from its story after the
	// event being played (a reset verb).
	Reset func()

	ss *Session
}

// Built is what a fixture made.
type Built struct {
	Model *ochazuke.Model
	// Program is the app's own tea.Model when it wraps the ochazuke
	// model's Update and View; nil drives Model.
	Program tea.Model
	// Init runs Program's (or Model's) Init before the first event; false
	// runs only Runtime.PendingCommands.
	Init bool
	// Start runs once the host has loaded the story's config, hung its
	// checks, sized the session and drawn the first frame (jobs a story
	// starts, a scroll offset).
	Start func() error
	// Close releases the fixture's fakes; the host removes the temp dirs.
	Close func()
	// Emit makes every live fake pane print a line (the output tick),
	// reporting whether one was live; HasPane reports one is live.
	Emit    func() bool
	HasPane func() bool
	// Writes is what the app wrote, for the writes expect and dirty
	// checks; nil is none.
	Writes func() []string
	// Snapshot bakes the session's current state into a story's app data,
	// for trim --snapshot; nil cannot.
	Snapshot func() (Snapshot, error)
}

// Snapshot is a session's current state as story data: the fields and
// the seed of a story that starts there, and what it cannot carry.
type Snapshot struct {
	Fields map[string]json.RawMessage
	Seed   json.RawMessage
	Warn   []string
}

// Options change how a session renders, never what it does.
type Options struct {
	Mono bool // the mono theme: no colour, as goldens compare
}

// Session is one built story: the runtime, the model, the app's own
// program, and what the host made for it.
type Session struct {
	Story *Story
	R     *ochazuke.Runtime
	M     *ochazuke.Model
	// Program is the app's tea.Model when it wraps M; nil drives M.
	Program tea.Model
	// Reset is set by Env.Reset; the host (TUI or Driver) rebuilds.
	Reset bool
	// StateDir is the state directory of a story with State, "" without;
	// Close removes it unless the session is restarting into it.
	StateDir string
	App      *App
	Fixture  *Fixture

	apps      []App
	opt       Options
	built     Built
	configDir string
	filesDir  string
	sandboxed bool
	cursor    *tea.Cursor
}

var epoch = time.Unix(0, 0).UTC()

// Build makes a session of a story through its app's fixture. A trimmed
// story's base is resolved by the caller (Resolve) before Build.
func (lib Library) Build(s *Story, opt Options) (*Session, error) {
	return build(lib.Apps, s, opt, "")
}

// Lookup resolves a story's app and fixture in apps: "" is the first.
func Lookup(apps []App, s *Story) (*App, *Fixture, error) {
	if len(apps) == 0 {
		return nil, nil, errors.New("this host registers no app")
	}
	app := &apps[0]
	if s.App != "" {
		i := slices.IndexFunc(apps, func(a App) bool { return a.Name == s.App })
		if i < 0 {
			return nil, nil, fmt.Errorf("app %q is not in this host (run it with %s story)", s.App, s.App)
		}
		app = &apps[i]
	}
	if len(app.Fixtures) == 0 {
		return nil, nil, fmt.Errorf("app %q has no fixture", app.Name)
	}
	fx := &app.Fixtures[0]
	if s.Fixture != "" {
		i := slices.IndexFunc(app.Fixtures, func(f Fixture) bool { return f.Name == s.Fixture })
		if i < 0 {
			var names []string
			for _, f := range app.Fixtures {
				names = append(names, f.Name)
			}
			return nil, nil, fmt.Errorf("fixture %q is not in app %q (it has %s)", s.Fixture, app.Name, strings.Join(names, ", "))
		}
		fx = &app.Fixtures[i]
	}
	for _, k := range slices.Sorted(maps.Keys(s.Fields)) {
		if !slices.Contains(app.Fields, k) {
			return nil, nil, fmt.Errorf("story field %q is not one app %q reads", k, app.Name)
		}
	}
	return app, fx, nil
}

// build is Build with the state directory a restart keeps ("" makes one
// for a story with State).
func build(apps []App, s *Story, opt Options, stateDir string) (ss *Session, err error) {
	app, fx, err := Lookup(apps, s)
	if err != nil {
		return nil, err
	}
	ss = &Session{Story: s, App: app, Fixture: fx, apps: apps, opt: opt}
	keep := stateDir != ""
	defer func() {
		if err != nil {
			ss.close(keep)
		}
	}()
	if s.State && stateDir == "" {
		if stateDir, err = os.MkdirTemp("", "monogatari-state-"); err != nil {
			return nil, err
		}
	}
	ss.StateDir = stateDir
	if err := ss.makeFiles(); err != nil {
		return nil, err
	}
	if err := ss.writeConfig(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	w, h := s.Size()
	env := Env{Story: s, Raw: raw, Seed: s.Seed, StateDir: ss.StateDir, ConfigDir: ss.configDir, FilesDir: ss.filesDir, Now: ss.now, Width: w, Height: h, Options: opt, Reset: func() { ss.Reset = true }, ss: ss}
	built, err := fx.Build(env)
	if err != nil {
		return nil, err
	}
	ss.built = built
	if built.Model == nil || built.Model.Runtime == nil {
		return nil, fmt.Errorf("fixture %s/%s built no model", app.Name, fx.Name)
	}
	ss.M, ss.R, ss.Program = built.Model, built.Model.Runtime, built.Program
	if !ss.sandboxed {
		if err := env.Sandbox(ss.M); err != nil {
			return nil, err
		}
	}
	if ss.configDir != "" {
		ss.R.Config().Dir = ss.configDir
		ss.R.EnsureUserActions()
		ss.R.EnsureUserBindings()
	}
	for _, c := range s.Checks {
		if err := ss.addCheck(c); err != nil {
			return nil, err
		}
	}
	ss.size()
	ss.View()
	if built.Start != nil {
		if err := built.Start(); err != nil {
			return nil, err
		}
	}
	return ss, nil
}

// NewRuntime makes a sandboxed runtime and model for a fixture that builds
// its app from scratch: user config off, state only in StateDir, the
// story's settings declared and its values set, a headless foreground,
// and the story's theme.
func (e Env) NewRuntime() (*ochazuke.Runtime, *ochazuke.Model, error) {
	r := ochazuke.NewRuntime()
	if err := e.sandboxRuntime(r); err != nil {
		return nil, nil, err
	}
	m := ochazuke.NewModel(r)
	if err := e.sandboxModel(m); err != nil {
		return nil, nil, err
	}
	return r, m, nil
}

// Sandbox applies NewRuntime's sandbox to a model the app built itself.
// The host calls it after Build when the fixture did not; a fixture calls
// it itself when its app must see the story's settings while it builds.
func (e Env) Sandbox(m *ochazuke.Model) error {
	if err := e.sandboxRuntime(m.Runtime); err != nil {
		return err
	}
	return e.sandboxModel(m)
}

// sandboxRuntime: a story reads and writes no user config files, and
// state only in its own directory.
func (e Env) sandboxRuntime(r *ochazuke.Runtime) error {
	r.Config().Disable()
	r.Config().StateDir = e.StateDir
	return r.DeclareSetting(e.Story.Settings...)
}

func (e Env) sandboxModel(m *ochazuke.Model) error {
	if e.ss != nil {
		e.ss.sandboxed = true
	}
	s, r := e.Story, m.Runtime
	// Values set at the start record the driver's clock as it starts
	// (the epoch), so a session value's time reads the same every run.
	clock := m.Now
	m.Now = func() time.Time { return epoch }
	err := setStoryValues(r, s)
	m.Now = clock
	if err != nil {
		return err
	}
	// A foreground program runs headless (no terminal to hand over): no
	// input, its output dropped, its exit the job's completion.
	m.Foreground = func(msg ochazuke.ForegroundMsg) tea.Cmd {
		return func() tea.Msg {
			msg.Cmd.Stdin, msg.Cmd.Stdout, msg.Cmd.Stderr = strings.NewReader(""), io.Discard, io.Discard
			return msg.Done(msg.Cmd.Run())
		}
	}
	if e.Options.Mono {
		m.Appearance.Theme = "mono"
	}
	if len(s.Themes) > 0 {
		schemes, problems := ochazuke.ParseSchemes(s.Themes)
		m.Appearance.Schemes = ochazuke.NewSchemeSet(schemes)
		for _, p := range problems {
			r.UI().Notify(p)
		}
	}
	if s.Theme != "" {
		m.Appearance.Theme = s.Theme
	}
	if s.Accent != "" {
		m.Appearance.Accent = lipgloss.Color(s.Accent)
	}
	return nil
}

// Field decodes the story's app field name into v strictly, reporting
// whether the story has it.
func (e Env) Field(name string, v any) (bool, error) { return e.Story.Field(name, v) }

// now is the session's clock as Env.Now reads it.
func (ss *Session) now() time.Time {
	if ss.M != nil && ss.M.Now != nil {
		return ss.M.Now()
	}
	return epoch
}

// Rebuild builds the session again from its story, closing this one; a
// restart keeps the state directory, a reset starts with a new one.
func (ss *Session) Rebuild(restart bool) (*Session, error) {
	dir := ""
	if restart {
		dir = ss.StateDir
	}
	ss.close(restart)
	return build(ss.apps, ss.Story, ss.opt, dir)
}

// Start is the command that starts the session: the program's Init when
// the fixture asks for it, else the runtime's pending commands.
func (ss *Session) Start() tea.Cmd {
	if !ss.built.Init {
		return ss.R.PendingCommands()
	}
	if ss.Program != nil {
		return ss.Program.Init()
	}
	return ss.M.Init()
}

// Update delivers a message to the app's program (the model when it has
// none).
func (ss *Session) Update(msg tea.Msg) tea.Cmd {
	if ss.Program != nil {
		p, cmd := ss.Program.Update(msg)
		if p != nil {
			ss.Program = p
		}
		return cmd
	}
	_, cmd := ss.M.Update(msg)
	return cmd
}

// View renders the app's program (the model when it has none).
func (ss *Session) View() tea.View {
	if ss.Program != nil {
		return ss.Program.View()
	}
	return ss.M.View()
}

// Writes is what the app wrote (Built.Writes).
func (ss *Session) Writes() []string {
	if ss.built.Writes == nil {
		return nil
	}
	return ss.built.Writes()
}

// Emit makes every live fake pane print one more output line (the
// "output" tick), reporting whether one was live.
func (ss *Session) Emit() bool { return ss.built.Emit != nil && ss.built.Emit() }

// HasPane reports whether a fake pane is live.
func (ss *Session) HasPane() bool { return ss.built.HasPane != nil && ss.built.HasPane() }

// Snapshot bakes the session's current state into story data.
func (ss *Session) Snapshot() (Snapshot, error) {
	if ss.built.Snapshot == nil {
		return Snapshot{}, fmt.Errorf("app %q cannot snapshot a session", ss.App.Name)
	}
	return ss.built.Snapshot()
}

// addCheck hangs a story's verb check.
func (ss *Session) addCheck(c CheckSpec) error {
	verb, name := c.Verb, c.Name
	if verb == "" {
		verb = "app.quit"
	}
	if name == "" {
		name = "story"
	}
	return ss.R.AddVerbCheck(verb, ochazuke.VerbCheck{Name: name, Check: func(*ochazuke.Runtime, ochazuke.Context, ochazuke.Arguments) ochazuke.Verdict {
		if c.Dirty && len(ss.Writes()) == 0 {
			return ochazuke.Allow()
		}
		return ochazuke.Verdict{Ask: c.Ask, Refuse: c.Refuse}
	}})
}

// Close releases the fixture's fakes and the session's temp dirs.
func (ss *Session) Close() { ss.close(false) }

// close ends the session, removing its state directory unless keep.
func (ss *Session) close(keep bool) {
	if ss.built.Close != nil {
		ss.built.Close()
		ss.built.Close = nil
	}
	if ss.StateDir != "" && !keep {
		os.RemoveAll(ss.StateDir)
	}
	if ss.configDir != "" {
		os.RemoveAll(ss.configDir)
	}
	if ss.filesDir != "" {
		os.RemoveAll(ss.filesDir)
	}
}

// makeFiles makes the story's file tree in a temp directory.
func (ss *Session) makeFiles() error {
	if len(ss.Story.Files) == 0 {
		return nil
	}
	dir, err := os.MkdirTemp("", "monogatari-files-")
	if err != nil {
		return err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return err
	}
	ss.filesDir = dir
	for _, f := range ss.Story.Files {
		path := filepath.Join(dir, filepath.FromSlash(f))
		if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
			return fmt.Errorf("file %q leaves the tree", f)
		}
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(f+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeConfig writes the story's config files into a temp config
// directory; build loads them as Model.Init does (actions.json, then
// bindings.json) once the fixture has built the app.
func (ss *Session) writeConfig() error {
	if len(ss.Story.Config) == 0 {
		return nil
	}
	dir, err := os.MkdirTemp("", "monogatari-config-")
	if err != nil {
		return err
	}
	ss.configDir = dir
	for name, data := range ss.Story.Config {
		if name != filepath.Base(name) || name == "" {
			return fmt.Errorf("config file %q: a file name, not a path", name)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// size sets the model's viewport so the story's area is what the chrome
// mode shows: the whole shell (full), the page body alone (none: the
// shell is made exactly as much larger as its chrome and the body is cut
// out), or the ticker alone (ticker-only: the rule line above the footer,
// where the ticker rides, cut out of a full shell).
func (ss *Session) size() {
	w, h := ss.Story.Size()
	switch ss.Story.Chrome {
	case "none":
		ss.M.Width, ss.M.Height = w+4, h+6+min(2, len(ss.M.JobsTicker(w)))
	case "ticker-only":
		ss.M.Width, ss.M.Height = w+4, 24
	default:
		ss.M.Width, ss.M.Height = w, h
	}
}

// Frame renders the story's area: exactly Width x Height cells of trusted
// ANSI, by the story's chrome mode.
func (ss *Session) Frame() string {
	w, h := ss.Story.Size()
	ss.size()
	var rows []string
	view := ss.View()
	ss.cursor = view.Cursor
	switch ss.Story.Chrome {
	case "none":
		all := strings.Split(view.Content, "\n")
		for i := 3; i < 3+h && i < len(all); i++ {
			rows = append(rows, ansi.Cut(all[i], 2, 2+w))
		}
		if c := ss.cursor; c != nil {
			moved := *c
			moved.X, moved.Y = c.X-2, c.Y-3
			ss.cursor = &moved
		}
	case "ticker-only":
		all := strings.Split(view.Content, "\n")
		rows = []string{ansi.Cut(all[len(all)-3], 2, 2+w)}
		ss.cursor = nil
	default:
		rows = strings.Split(view.Content, "\n")
	}
	if c := ss.cursor; c != nil && (c.X < 0 || c.Y < 0 || c.X >= w || c.Y >= h) {
		ss.cursor = nil
	}
	if len(rows) > h {
		rows = rows[:h]
	}
	for len(rows) < h {
		rows = append(rows, "")
	}
	for i, row := range rows {
		if pad := w - ansi.StringWidth(row); pad > 0 {
			row += strings.Repeat(" ", pad)
		}
		rows[i] = ansi.Truncate(row, w, "")
	}
	return strings.Join(rows, "\n")
}

// Cursor is the terminal cursor of the last Frame, relative to the
// story's area, or nil.
func (ss *Session) Cursor() *tea.Cursor { return ss.cursor }

// setStoryValues sets the story's saved values, then starts the session
// and sets its session values, each in name order.
func setStoryValues(r *ochazuke.Runtime, s *Story) error {
	settings := r.Settings()
	for _, name := range slices.Sorted(maps.Keys(s.Values)) {
		if err := settings.Set(name, s.Values[name]); err != nil {
			return fmt.Errorf("values: %w", err)
		}
	}
	if len(s.Session) == 0 {
		return nil
	}
	settings.StartSession()
	for _, name := range slices.Sorted(maps.Keys(s.Session)) {
		if _, err := settings.SetSession(name, s.Session[name]); err != nil {
			return fmt.Errorf("session: %w", err)
		}
	}
	return nil
}

// label is what an instance shows: its label, else its id.
func label(inst *ochazuke.Instance) string {
	if inst == nil {
		return ""
	}
	if inst.Label != "" {
		return inst.Label
	}
	return inst.ID
}
