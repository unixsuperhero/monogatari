package monogatari

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

// Story is one scenario file.
type Story struct {
	Name   string `json:"name"`
	Area   string `json:"area"`
	Skip   string `json:"skip,omitempty"` // "gap: ..." marks a known framework gap; the story runs but cannot fail the suite
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Chrome string `json:"chrome,omitempty"` // full (default), none (page body only) or ticker-only
	// Theme renders the story in a theme other than mono (adaptive, dark,
	// light); Accent ("#RRGGBB") is the Appearance accent override. Goldens
	// stay escape-free, so a coloured story asserts colours with colored.
	Theme  string `json:"theme,omitempty"`
	Accent string `json:"accent,omitempty"`
	// Themes are colour schemes in the themes.json shape the session adds
	// to the built-in themes (Theme may name one); a scheme that does not
	// validate is a notice, as in an application.
	Themes json.RawMessage `json:"themes,omitempty"`
	// App names the application whose fixture builds the session ("" is
	// the host's first app); Fixture names one of its starting states (""
	// is the app's first); Seed is app-defined data handed to the fixture.
	App     string          `json:"app,omitempty"`
	Fixture string          `json:"fixture,omitempty"`
	Seed    json.RawMessage `json:"seed,omitempty"`
	// Base and Start make a trimmed story: replay the base's events before
	// Start headless, then play Script as the window under test.
	Base   string  `json:"base,omitempty"`
	Start  int     `json:"start,omitempty"`
	Script []Event `json:"script,omitempty"`
	// Setup reaches a tutorial's reset point. Each action starts in a fresh
	// session with Setup replayed; actions never inherit one another's edits.
	Setup   []Event  `json:"setup,omitempty"`
	Actions []Action `json:"actions,omitempty"`
	// Notes narrate script/setup positions (After 0 is the opening frame).
	Notes []Note `json:"notes,omitempty"`
	// Settings are declared on the session's runtime; with no config
	// directory (stories read no user files) their values last the session.
	Settings []ochazuke.Setting `json:"settings,omitempty"`
	// Values are setting values the session starts with, set as
	// settings.json would hold them (Settings.Set; with no config
	// directory they last the session): the saved layer.
	Values map[string]any `json:"values,omitempty"`
	// Session are session values set at the start, above Values (the
	// session started as Model.Init starts it, then Settings.SetSession):
	// what a :set line leaves unwritten.
	Session map[string]any `json:"session,omitempty"`
	// State gives the session a state directory of its own (a temp dir,
	// removed when the session ends), kept across a restart event, so a
	// story shows what the application remembers (pane dividers).
	State bool `json:"state,omitempty"`
	// Config is the user's config files the session starts with, by file
	// name: written into a temp config directory of its own (removed at
	// the end); actions.json and then bindings.json are loaded the way
	// Model.Init loads them, after the pages are built. A problem a load
	// reports is a notice.
	Config map[string]json.RawMessage `json:"config,omitempty"`
	// Checks hang verb checks on the session's verbs (Runtime.AddVerbCheck;
	// app.quit is BeforeQuit's), after the pages are built.
	Checks []CheckSpec `json:"checks,omitempty"`
	// Files is a file tree the session makes in a temp directory of its
	// own (removed at the end): paths relative to it, a trailing / for a
	// folder. {{files}} in a record field's base or value names it, and
	// writes show it as {{files}}.
	Files  []string `json:"files,omitempty"`
	Expect Expects  `json:"expect,omitempty"`

	// Fields are the story's other top-level fields, the ones an app
	// reads (App.Fields: the demo's page and pages), kept as written. A
	// field the story's app does not declare fails the build.
	Fields map[string]json.RawMessage `json:"-"`

	// Path is where the file was loaded from (not part of the data); rel
	// is the path inside fsys, disk the directory fsys reads when it is on
	// disk (goldens are written there).
	Path string `json:"-"`
	fsys fs.FS
	rel  string
	disk string
}

// CheckSpec is one verb check: on Verb (default app.quit), Ask or Refuse
// while the page's first editable tree has unwritten changes (Dirty: any
// write since the story started), else always (Dirty false).
type CheckSpec struct {
	Verb   string `json:"verb,omitempty"`
	Name   string `json:"name,omitempty"`
	Ask    string `json:"ask,omitempty"`
	Refuse string `json:"refuse,omitempty"`
	Dirty  bool   `json:"dirty,omitempty"`
}

// Action is one independent lesson, played after Setup. Keys accepts the
// same strings and events as Script, including job deliveries and notes.
type Action struct {
	ID      string  `json:"id,omitempty"` // optional stable identity; Label is for people
	Label   string  `json:"label"`
	Caption string  `json:"caption,omitempty"`
	Keys    []Event `json:"keys"`
	Expect  Expects `json:"expect,omitempty"`
	Notes   []Note  `json:"notes,omitempty"`
}

// Note is narration at a script, setup or action position.
type Note struct {
	After int    `json:"after"`
	Keys  string `json:"keys,omitempty"` // optional key legend, kept for older notes
	Note  string `json:"note"`
}

// EventCount is the authored event count (setup once, each action once).
// Reset replays do not inflate the count in the host's index.
func (s *Story) EventCount() int {
	n := len(s.Script) + len(s.Setup)
	for _, a := range s.Actions {
		n += len(a.Keys)
	}
	return n
}

// Event is one action: a key press, typed text, a paste, a job delivering,
// or a framework timer firing. T is milliseconds since the session started
// (recordings only; scripts leave it 0).
type Event struct {
	T     int64  `json:"t,omitempty"`
	Key   string `json:"key,omitempty"`
	Type  string `json:"type,omitempty"`
	Paste string `json:"paste,omitempty"`
	Job   string `json:"job,omitempty"`   // noun.verb of the job that delivered
	State string `json:"state,omitempty"` // done or failed
	Tick  string `json:"tick,omitempty"`  // linger, binding, terminal (a pane frame), output (fake output line), debounce (a tree line's, a form's or a completion's pause) or segment (the status segments' next refresh, answered)
	Note  string `json:"note,omitempty"`  // shown while this event plays
	// Restart quits the session and builds it again from the story with
	// the same state directory (State), as a user reopening the app.
	Restart bool `json:"restart,omitempty"`
}

// UnmarshalJSON reads a bare string as a key press.
func (e *Event) UnmarshalJSON(b []byte) error {
	var key string
	if err := json.Unmarshal(b, &key); err == nil {
		*e = Event{Key: key}
		return nil
	}
	type plain Event
	return json.Unmarshal(b, (*plain)(e))
}

// MarshalJSON writes an untimed key press as a bare string.
func (e Event) MarshalJSON() ([]byte, error) {
	if e.T == 0 && e.Key != "" && e.Type == "" && e.Paste == "" && e.Job == "" && e.Tick == "" && e.Note == "" && !e.Restart {
		return json.Marshal(e.Key)
	}
	type plain Event
	return json.Marshal(plain(e))
}

// String is the event as a script reads it.
func (e Event) String() string {
	switch {
	case e.Key != "":
		return e.Key
	case e.Type != "":
		return fmt.Sprintf("type %q", e.Type)
	case e.Paste != "":
		return fmt.Sprintf("paste %q", e.Paste)
	case e.Job != "":
		return "job " + e.Job + " " + e.State
	case e.Tick != "":
		return "tick " + e.Tick
	case e.Restart:
		return "restart"
	}
	return "(empty)"
}

// Expect is what must hold after a step (After events) or at the end
// (After 0). Every field is optional.
type Expect struct {
	After int     `json:"after,omitempty"`
	Focus string  `json:"focus,omitempty"`
	Mode  string  `json:"mode,omitempty"`
	Modal *string `json:"modal,omitempty"` // top modal instance, "" for none
	Quit  *bool   `json:"quit,omitempty"`  // Runtime.Quit: app.quit ran
	Page  string  `json:"page,omitempty"`
	// Shift is the current page's pane divider (Runtime.PaneShift).
	Shift   *int  `json:"shift,omitempty"`
	Visible *bool `json:"visible,omitempty"` // the focused element's label is on screen
	// Highlighted counts rows holding a full-strength highlight (reverse
	// video not under faint); Dimmed counts rows holding a dimmed one (an
	// unfocused page list's selected row, a highlight beneath a modal).
	// Dimmed is read in the mono theme, where a dim is the faint attribute.
	Highlighted *int `json:"highlighted,omitempty"`
	Dimmed      *int `json:"dimmed,omitempty"`
	// HighlightedText is text every highlighted row holds (the selected row's label).
	HighlightedText string `json:"highlightedText,omitempty"`
	// Cell is the text of a table's current cell (drawn reverse and
	// underlined inside its row, or reverse on a background, dimmed or
	// not); "" for none.
	Cell *string `json:"cell,omitempty"`
	// Badge is the mode badge at the right of the title row (full
	// chrome): its whole text, padded by a cell on each side, followed by
	// the end of the row or a " · " (the focus display; a recording's
	// "· REC @q" too, so expect the whole badge while recording) (R2.10).
	Badge    string   `json:"badge,omitempty"`
	Contains []string `json:"contains,omitempty"`
	Lacks    []string `json:"lacks,omitempty"`
	Order    []string `json:"order,omitempty"` // substrings that appear on screen in this order
	Ticker   []string `json:"ticker,omitempty"`
	NoTicker bool     `json:"noTicker,omitempty"`
	// Segments are the status segments the header rule draws
	// (Model.StatusSegments); NoSegments expects none.
	Segments   []string `json:"segments,omitempty"`
	NoSegments bool     `json:"noSegments,omitempty"`
	Notices    []string `json:"notices,omitempty"`
	NoNotices  bool     `json:"noNotices,omitempty"`
	// Colored maps on-screen text to a "#RRGGBB" foreground some row holding
	// that text draws in; NoColor lists foregrounds no cell may use.
	Colored map[string]string `json:"colored,omitempty"`
	NoColor []string          `json:"noColor,omitempty"`
	// Styled maps on-screen text to whether it starts a run drawn in Hex
	// ("#RRGGBB"): the SGR right before the text sets that foreground,
	// cell-exact where Colored is row-wide. "!#RRGGBB" expects any other.
	Styled map[string]string `json:"styled,omitempty"`
	// Attrs maps on-screen text to the attributes its first cell is drawn
	// with, followed along its row's SGR state: a comma list of bold,
	// faint, reverse, underline, fg (any foreground colour) and bg (any
	// background colour), each
	// "-name" for absent. "bold,-fg" reads a mono tone.
	Attrs map[string]string `json:"attrs,omitempty"`
	// Writes is every change the page's editable trees, record editors and
	// Markdown documents wrote, in order (TreeChange.String,
	// RecordWrite.String, "toggle LINE [x] TEXT"); [] expects none.
	Writes *[]string `json:"writes,omitempty"`
	// Register is the unnamed register's text (a yank).
	Register *string `json:"register,omitempty"`
}

// Expects is one final Expect or a list of per-step ones.
type Expects []Expect

func (x *Expects) UnmarshalJSON(b []byte) error {
	if strings.HasPrefix(strings.TrimSpace(string(b)), "[") {
		var list []Expect
		if err := json.Unmarshal(b, &list); err != nil {
			return err
		}
		*x = list
		return nil
	}
	var one Expect
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*x = Expects{one}
	return nil
}

func (x Expects) MarshalJSON() ([]byte, error) {
	if len(x) == 1 && x[0].After == 0 {
		return json.Marshal(x[0])
	}
	return json.Marshal([]Expect(x))
}

// Size is the story's width and height (80x24 unless given).
func (s *Story) Size() (int, int) { return cmp(s.Width, 80), cmp(s.Height, 24) }

func cmp(a, b int) int {
	if a > 0 {
		return a
	}
	return b
}

// Slug is the file name stem of a name.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// storyFields are the JSON names of Story's own fields.
var storyFields = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeFor[Story]()
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}()

// storyData is Story without its JSON methods.
type storyData Story

// UnmarshalJSON reads Story's own fields strictly and keeps every other
// top-level field in Fields, for the story's app to read.
func (s *Story) UnmarshalJSON(b []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	own := map[string]json.RawMessage{}
	var fields map[string]json.RawMessage
	for k, v := range all {
		if storyFields[k] {
			own[k] = v
			continue
		}
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
		fields[k] = v
	}
	data, err := json.Marshal(own)
	if err != nil {
		return err
	}
	var plain storyData
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plain); err != nil {
		return err
	}
	plain.Fields = fields
	plain.Path, plain.fsys, plain.rel, plain.disk = s.Path, s.fsys, s.rel, s.disk
	*s = Story(plain)
	return nil
}

// MarshalJSON writes Story's own fields, then Fields in name order.
func (s Story) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(storyData(s))
	if err != nil || len(s.Fields) == 0 {
		return data, err
	}
	var b bytes.Buffer
	b.Write(data[:len(data)-1])
	for _, k := range slices.Sorted(maps.Keys(s.Fields)) {
		if storyFields[k] {
			return nil, fmt.Errorf("story field %q is Story's own; set it, not Fields", k)
		}
		key, _ := json.Marshal(k)
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.Write(key)
		b.WriteByte(':')
		v := s.Fields[k]
		if len(v) == 0 {
			v = json.RawMessage("null")
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Field decodes the top-level field name into v strictly, reporting
// whether the story has it.
func (s *Story) Field(name string, v any) (bool, error) {
	raw, ok := s.Fields[name]
	if !ok {
		return false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return true, fmt.Errorf("%s: %w", name, err)
	}
	return true, nil
}

// Parse reads one story file.
func Parse(data []byte, where string) (*Story, error) {
	var s Story
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("%s: a story needs a name", where)
	}
	if len(s.Actions) > 0 && (len(s.Script) > 0 || s.Base != "") {
		return nil, fmt.Errorf("%s: actions use setup, not script or base", where)
	}
	if len(s.Setup) > 0 && len(s.Actions) == 0 {
		return nil, fmt.Errorf("%s: setup needs actions", where)
	}
	for i, a := range s.Actions {
		if a.Label == "" || len(a.Keys) == 0 {
			return nil, fmt.Errorf("%s: action %d needs a label and keys", where, i+1)
		}
	}
	if s.Area == "" {
		s.Area = path.Base(path.Dir(filepath.ToSlash(where)))
	}
	s.Path = where
	return &s, nil
}

// LoadFS reads every <area>/<name>.json under root in fsys.
func LoadFS(fsys fs.FS, root string) ([]*Story, error) {
	var out []*Story
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".json" {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		s, err := Parse(data, p)
		if err != nil {
			return err
		}
		s.fsys, s.rel = fsys, p
		out = append(out, s)
		return nil
	})
	return out, err
}

// LoadDir reads the stories under a directory on disk; a missing directory
// is no stories.
func LoadDir(dir string) ([]*Story, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	stories, err := LoadFS(os.DirFS(dir), ".")
	for _, s := range stories {
		s.Path, s.disk = filepath.Join(dir, s.Path), dir
	}
	return stories, err
}

// Sort orders stories by area, then file path.
func Sort(stories []*Story) {
	slices.SortStableFunc(stories, func(a, b *Story) int {
		if c := strings.Compare(a.Area, b.Area); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
}

// Find returns the story whose name, slug or area/slug is name.
func Find(stories []*Story, name string) *Story {
	for _, s := range stories {
		stem := strings.TrimSuffix(path.Base(filepath.ToSlash(s.Path)), ".json")
		if s.Name == name || stem == name || s.Area+"/"+stem == name {
			return s
		}
	}
	return nil
}

// GoldenPath is the golden frame file beside the story.
func (s *Story) GoldenPath() string { return strings.TrimSuffix(s.Path, ".json") + ".golden" }

// ReadGolden reads the golden frame, or reports fs.ErrNotExist.
func (s *Story) ReadGolden() ([]byte, error) {
	if s.fsys == nil {
		return os.ReadFile(s.GoldenPath())
	}
	return fs.ReadFile(s.fsys, strings.TrimSuffix(s.rel, ".json")+".golden")
}

// WriteGolden writes the golden frame beside a story loaded from disk.
func (s *Story) WriteGolden(frame string) error {
	if s.fsys != nil && s.disk == "" {
		return fmt.Errorf("%s: embedded story; pass --dir to update goldens on disk", s.Path)
	}
	return os.WriteFile(s.GoldenPath(), []byte(frame), 0o644)
}
