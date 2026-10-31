package host

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// The catalog is an ochazuke runtime behind ochazuke/cli, additive to the
// commands in host.go: data nouns story, recording, fixture, app, verb and
// edge (the noun of an app is application: ochazuke's runtime owns the
// app noun, app.quit), each with list (rows, declared with Rows); story, recording,
// fixture and app with show; story with run, copy and state (every list's
// rows as JSON Lines); recording with delete. The relations are verbs on
// both nouns sharing one handler: story.list {app, fixture} with
// application.stories and fixture.stories, fixture.list {app} with
// application.fixtures,
// recording.list {story} with story.recordings; story.fixture shows the
// story's fixture. A row is {id, label, data}:
//
//	{id: "lists/spill", label: "...", data: {area: "lists", app: "demo", fixture: "pages", size: "80x24", chrome: "full", steps: 3, skipped: false}}
//	{id: "recording/1", label: "...", data: {ref: "1", story: "...", started: "...", events: 12}}
//	{id: "fixture/demo/pages", label: "demo pages", data: {app: "demo", name: "pages", summary: "...", default: true}}
//	{id: "application/demo", label: "demo", data: {name: "demo", summary: "...", fixtures: ["pages"], fields: ["page", "pages"], default: true}}
//	{id: "verb/normal/list.move", label: "normal list list.move", data: {mode: "normal", noun: "list", verb: "list.move"}}
//	{id: "edge/normal/insert/input.edit", label: "normal → insert via input.edit", data: {from, to, via}}

// catalogNouns are the catalog's data nouns, in state order.
var catalogNouns = []string{"story", "recording", "fixture", "application", "verb", "edge"}

// Row is one element of a list; Cells are its table cells when the noun
// declares columns (story: storyColumns).
type Row struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Data  any      `json:"data,omitempty"`
	Cells []string `json:"cells,omitempty"`
}

// storyColumns are the story noun's table, declared once: the catalog's
// columns (story list, state, nabe) and the TUI index (without area,
// which is the index's section heading). Size drops first, then steps,
// area and the gap; the story name is cut last.
var storyColumns = []ochazuke.Column{
	{Title: "story", Min: 24},
	{Title: "area", Priority: 3},
	{Title: "size", Priority: 4},
	{Title: "steps", Align: ochazuke.AlignRight, Priority: 2},
	{Title: "gap", Min: 12, Priority: 1},
}

// storyCells is a story's row of storyColumns.
func storyCells(s *story.Story) []string {
	w, h := s.Size()
	return []string{s.Name, s.Area, fmt.Sprintf("%dx%d", w, h), strconv.Itoa(s.EventCount()), strings.TrimPrefix(s.Skip, "gap: ")}
}

// withoutArea drops the area column (index 1) from storyColumns or a
// story's cells, for the index, where the area is the section heading.
func withoutArea[T any](row []T) []T { return slices.Delete(slices.Clone(row), 1, 2) }

// StoryRow is a story row's data.
type StoryRow struct {
	Area    string        `json:"area"`
	App     string        `json:"app"`
	Fixture string        `json:"fixture"`
	Size    string        `json:"size"`
	Chrome  string        `json:"chrome"`
	Steps   int           `json:"steps"`
	Skipped bool          `json:"skipped"`
	Expect  story.Expects `json:"expect,omitempty"`
}

// RecordingRow is a recording row's data: Ref is what replay, keep and
// run --from take (a history number, else the saved name).
type RecordingRow struct {
	Ref     string `json:"ref"`
	Kind    string `json:"kind"` // history or saved
	Story   string `json:"story"`
	Started string `json:"started"`
	Events  int    `json:"events"`
	Path    string `json:"path"`
}

// FixtureRow is one fixture of one of the host's apps; Default marks the
// app's first, which a story naming no fixture builds.
type FixtureRow struct {
	App     string `json:"app"`
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Default bool   `json:"default"`
}

// AppRow is one of the host's apps; Default marks the first, which a
// story naming no app builds.
type AppRow struct {
	Name     string   `json:"name"`
	Summary  string   `json:"summary,omitempty"`
	Fixtures []string `json:"fixtures"`
	Fields   []string `json:"fields,omitempty"`
	Default  bool     `json:"default"`
}

// VerbRow is one (mode, noun, verb) of the derived minimum.
type VerbRow struct {
	Mode string `json:"mode"`
	Noun string `json:"noun"`
	Verb string `json:"verb"`
}

// EdgeRow is one mode edge: from to to via a noun.verb (or the row or
// tick that took it).
type EdgeRow struct {
	From string `json:"from"`
	To   string `json:"to"`
	Via  string `json:"via"`
}

// storyID is a story's row id: area/stem, which story.Find resolves.
func storyID(s *story.Story) string {
	return s.Area + "/" + strings.TrimSuffix(filepath.Base(s.Path), ".json")
}

// storyFixture is the app and fixture a story builds (a trimmed story's
// through its base): "" when they do not resolve (its run says why).
func storyFixture(lib story.Library, s *story.Story) (app, fixture string) {
	r := s
	if s.Base != "" {
		if resolved, _, err := lib.Resolve(s); err == nil {
			r = resolved
		}
	}
	a, f, err := story.Lookup(lib.Apps, r)
	if err != nil {
		return "", ""
	}
	return a.Name, f.Name
}

// storyRows are the stories, those of one app (and fixture) when named.
func storyRows(lib story.Library, app, fixture string) []Row {
	rows := []Row{}
	for _, s := range lib.Stories {
		a, f := storyFixture(lib, s)
		if (app != "" && a != app) || (fixture != "" && f != fixture) {
			continue
		}
		w, h := s.Size()
		chrome := s.Chrome
		if chrome == "" {
			chrome = "full"
		}
		rows = append(rows, Row{ID: storyID(s), Label: s.Name, Data: StoryRow{
			Area: s.Area, App: a, Fixture: f, Size: fmt.Sprintf("%dx%d", w, h), Chrome: chrome,
			Steps: s.EventCount(), Skipped: s.Skip != "", Expect: s.Expect,
		}, Cells: storyCells(s)})
	}
	return rows
}

// fixtureRow is one fixture's row.
func fixtureRow(app *story.App, i int) Row {
	f := app.Fixtures[i]
	return Row{ID: "fixture/" + app.Name + "/" + f.Name, Label: app.Name + " " + f.Name, Data: FixtureRow{App: app.Name, Name: f.Name, Summary: f.Summary, Default: i == 0}}
}

// fixtureRows are the fixtures of the host's apps, of one app when named.
func fixtureRows(apps []story.App, app string) []Row {
	rows := []Row{}
	for i := range apps {
		if app != "" && apps[i].Name != app {
			continue
		}
		for j := range apps[i].Fixtures {
			rows = append(rows, fixtureRow(&apps[i], j))
		}
	}
	return rows
}

// appRows are the host's apps.
func appRows(apps []story.App) []Row {
	rows := []Row{}
	for i, a := range apps {
		fixtures := []string{}
		for _, f := range a.Fixtures {
			fixtures = append(fixtures, f.Name)
		}
		rows = append(rows, Row{ID: "application/" + a.Name, Label: a.Name, Data: AppRow{Name: a.Name, Summary: a.Summary, Fixtures: fixtures, Fields: a.Fields, Default: i == 0}})
	}
	return rows
}

// recordingRows are the recordings, those of one story (by name) when
// named.
func recordingRows(store story.Store, of string) ([]Row, error) {
	hist, err := store.History()
	if err != nil {
		return nil, err
	}
	saved, err := store.Saved()
	if err != nil {
		return nil, err
	}
	rows := []Row{}
	add := func(e story.Entry, kind, ref string) {
		if of != "" && e.Rec.Story != of {
			return
		}
		started := e.Rec.Started.UTC().Format("2006-01-02T15:04:05Z")
		rows = append(rows, Row{ID: "recording/" + ref, Label: kind + " " + ref + ": " + e.Rec.Story, Data: RecordingRow{
			Ref: ref, Kind: kind, Story: e.Rec.Story, Started: started, Events: len(e.Rec.Events), Path: e.Path,
		}})
	}
	for _, e := range hist {
		add(e, "history", strconv.Itoa(e.N))
	}
	for _, e := range saved {
		add(e, "saved", e.Name)
	}
	return rows, nil
}

// minimumRows are the verb and edge rows of the core stories' minimum,
// the same set PROG verbs prints.
func (t *Tool) minimumRows() (verbs, edges []Row, err error) {
	core, err := t.cfg.core("")
	if err != nil {
		return nil, nil, err
	}
	m, err := coreMinimum(t.cfg.Apps, core)
	if err != nil {
		return nil, nil, err
	}
	verbs, edges = []Row{}, []Row{}
	for _, mode := range sortedKeys(m.Verbs) {
		for _, noun := range sortedKeys(m.Verbs[mode]) {
			for _, verb := range m.Verbs[mode][noun] {
				verbs = append(verbs, Row{ID: "verb/" + mode + "/" + verb, Label: mode + " " + noun + " " + verb, Data: VerbRow{mode, noun, verb}})
			}
		}
	}
	for _, e := range m.Edges {
		edges = append(edges, Row{ID: "edge/" + e.From + "/" + e.To + "/" + e.Via, Label: e.From + " → " + e.To + " via " + e.Via, Data: EdgeRow{e.From, e.To, e.Via}})
	}
	return verbs, edges, nil
}

// rows is one catalog noun's rows, every one (as its list verb with no
// argument answers).
func (t *Tool) rows(noun string) ([]Row, error) {
	switch noun {
	case "story":
		lib, err := t.library("")
		if err != nil {
			return nil, err
		}
		return storyRows(lib, "", ""), nil
	case "recording":
		return recordingRows(t.store(), "")
	case "fixture":
		return fixtureRows(t.cfg.Apps, ""), nil
	case "application":
		return appRows(t.cfg.Apps), nil
	}
	verbs, edges, err := t.minimumRows()
	if noun == "verb" {
		return verbs, err
	}
	return edges, err
}

// lines is rows as JSON Lines tagged with their noun.
func lines(noun string, rows []Row) ocli.Lines {
	out := make(ocli.Lines, len(rows))
	for i, r := range rows {
		out[i] = ocli.Row{Noun: noun, ID: r.ID, Label: r.Label, Data: r.Data, Cells: r.Cells}
	}
	return out
}

// nounOf is the catalog noun a row id belongs to: its prefix, else story.
func nounOf(id string) string {
	prefix, _, _ := strings.Cut(id, "/")
	for _, noun := range catalogNouns[1:] {
		if prefix == noun {
			return noun
		}
	}
	return "story"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// shell is the catalog's runtime and command line.
func (t *Tool) shell() *ocli.Shell {
	r := ochazuke.NewRuntime()
	r.Config().Dir = t.configDir()
	if err := declareIndexSettings(r); err != nil {
		panic(err)
	}
	lists := map[string]*ochazuke.List{}
	for _, noun := range catalogNouns {
		var opts []ochazuke.InstanceOption
		if noun == "story" {
			opts = append(opts, ochazuke.Columns(storyColumns...))
		}
		list, err := r.AddList("list/"+noun, nil, opts...)
		if err != nil {
			panic(err) // a fresh runtime takes every list
		}
		lists[noun] = list
	}
	// shown puts rows in the noun's list (so a --target names one) and
	// answers them.
	shown := func(noun string, rows []Row, err error) ochazuke.Result {
		if err != nil {
			return ochazuke.Failed(err)
		}
		items := make([]ochazuke.ListItem, len(rows))
		for i, row := range rows {
			items[i] = ochazuke.ListItem{ID: row.ID, Label: row.Label, Noun: noun, Data: row.ID, Cells: row.Cells}
		}
		if err := lists[noun].SetItems(items); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(rows)
	}
	listed := func(noun string) ochazuke.Result {
		rows, err := t.rows(noun)
		return shown(noun, rows, err)
	}
	target := func(c ochazuke.Ctx, noun string) (string, error) {
		if c.Instance == nil || c.Instance.Noun != noun {
			return "", fmt.Errorf("%s needs --target ID (an id from %s list)", noun, noun)
		}
		id, _ := c.Instance.Data.(string)
		return id, nil
	}
	arg := func(c ochazuke.Ctx, name string) string {
		v, _ := c.Args[name].(string)
		return v
	}
	findStory := func(c ochazuke.Ctx) (story.Library, *story.Story, error) {
		id, err := target(c, "story")
		if err != nil {
			return story.Library{}, nil, err
		}
		lib, err := t.library("")
		if err != nil {
			return lib, nil, err
		}
		for _, s := range lib.Stories {
			if storyID(s) == id {
				return lib, s, nil
			}
		}
		return lib, nil, fmt.Errorf("no story %q", id)
	}
	findApp := func(name string) (*story.App, error) {
		for i := range t.cfg.Apps {
			if t.cfg.Apps[i].Name == name {
				return &t.cfg.Apps[i], nil
			}
		}
		return nil, fmt.Errorf("no app %q in %s (application list)", name, t.Prog)
	}
	findFixture := func(id string) (*story.App, int, error) {
		appName, name, _ := strings.Cut(strings.TrimPrefix(id, "fixture/"), "/")
		app, err := findApp(appName)
		if err != nil {
			return nil, 0, err
		}
		i := slices.IndexFunc(app.Fixtures, func(f story.Fixture) bool { return f.Name == name })
		if i < 0 {
			return nil, 0, fmt.Errorf("no fixture %q in app %q (fixture list)", name, appName)
		}
		return app, i, nil
	}
	// The relations' shared handlers: the stories of an app (and fixture),
	// the fixtures of an app, the recordings of a story.
	storiesOf := func(app, fixture string) ochazuke.Result {
		lib, err := t.library("")
		if err != nil {
			return ochazuke.Failed(err)
		}
		return shown("story", storyRows(lib, app, fixture), nil)
	}
	fixturesOf := func(app string) ochazuke.Result {
		if app != "" {
			if _, err := findApp(app); err != nil {
				return ochazuke.Failed(err)
			}
		}
		return shown("fixture", fixtureRows(t.cfg.Apps, app), nil)
	}
	recordingsOf := func(name string) ochazuke.Result {
		rows, err := recordingRows(t.store(), name)
		return shown("recording", rows, err)
	}

	r.Define("story").Columns(storyColumns...).
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return storiesOf(arg(c, "app"), arg(c, "fixture")) },
			"the stories, of one app (and fixture) when named: {id: AREA/NAME, label, data: {area, app, fixture, size, chrome, steps, skipped, expect?}, cells: [story, area, size, steps, gap]}").Rows("story").
		Verb("show", func(c ochazuke.Ctx) ochazuke.Result {
			_, s, err := findStory(c)
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(s)
		}, "one story's JSON: --target ID").
		Verb("run", func(c ochazuke.Ctx) ochazuke.Result {
			lib, s, err := findStory(c)
			if err != nil {
				return ochazuke.Failed(err)
			}
			res := lib.Run(s, story.RunOptions{Golden: true})
			out := map[string]any{"id": storyID(s), "ok": res.OK(), "skipped": res.Skipped, "failed": res.Failed}
			if res.Failed == nil {
				out["failed"] = []string{}
			}
			if res.Err != nil {
				out["error"] = res.Err.Error()
			}
			return ochazuke.Done(out)
		}, "play one story headless and check it: --target ID; {id, ok, skipped, failed, error?}").
		Verb("copy", func(c ochazuke.Ctx) ochazuke.Result {
			_, s, err := findStory(c)
			if err != nil {
				return ochazuke.Failed(err)
			}
			path, err := t.copyStory(s, arg(c, "as"))
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(map[string]any{"id": s.Area + "/" + story.Slug(arg(c, "as")), "path": path})
		}, "copy one story to the user's stories under a new name (its golden too): --target ID --as NAME; {id, path}").
		Verb("fixture", func(c ochazuke.Ctx) ochazuke.Result {
			lib, s, err := findStory(c)
			if err != nil {
				return ochazuke.Failed(err)
			}
			a, f := storyFixture(lib, s)
			app, i, err := findFixture("fixture/" + a + "/" + f)
			if err != nil {
				return ochazuke.Failed(fmt.Errorf("%s: %w", storyID(s), err))
			}
			return ochazuke.Done(fixtureRow(app, i))
		}, "the fixture one story builds: --target ID; a fixture row").
		Verb("recordings", func(c ochazuke.Ctx) ochazuke.Result {
			_, s, err := findStory(c)
			if err != nil {
				return ochazuke.Failed(err)
			}
			return recordingsOf(s.Name)
		}, "one story's recordings (recording list --story NAME): --target ID").Rows("recording").
		Verb("state", func(ochazuke.Ctx) ochazuke.Result {
			var out ocli.Lines
			for _, noun := range catalogNouns {
				rows, err := t.rows(noun)
				if err != nil {
					return ochazuke.Failed(err)
				}
				out = append(out, lines(noun, rows)...)
			}
			return ochazuke.Done(out)
		}, "every list's rows in one call as JSON Lines tagged by noun: "+strings.Join(catalogNouns, ", ")+" (the catalog's state)").State()
	r.Define("recording").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return recordingsOf(arg(c, "story")) },
			"the history ring (newest first) then the saved recordings, of one story when named: {id: recording/REF, label, data: {ref, kind, story, started, events, path}}").Rows("recording").
		Verb("show", func(c ochazuke.Ctx) ochazuke.Result {
			id, err := target(c, "recording")
			if err != nil {
				return ochazuke.Failed(err)
			}
			e, err := t.store().Load(strings.TrimPrefix(id, "recording/"))
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(e.Rec)
		}, "one recording's JSON: --target recording/REF").
		Verb("delete", func(c ochazuke.Ctx) ochazuke.Result {
			id, err := target(c, "recording")
			if err != nil {
				return ochazuke.Failed(err)
			}
			path, err := t.store().Delete(strings.TrimPrefix(id, "recording/"))
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(map[string]any{"id": id, "deleted": path})
		}, "delete one recording (a ring entry or a saved one): --target recording/REF; {id, deleted}")
	r.Define("fixture").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return fixturesOf(arg(c, "app")) },
			"the fixtures of the host's apps, of one app when named: {id: fixture/APP/NAME, label, data: {app, name, summary, default}}").Rows("fixture").
		Verb("show", func(c ochazuke.Ctx) ochazuke.Result {
			id, err := target(c, "fixture")
			if err != nil {
				return ochazuke.Failed(err)
			}
			app, i, err := findFixture(id)
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(fixtureRow(app, i))
		}, "one fixture: --target fixture/APP/NAME; its row").
		Verb("stories", func(c ochazuke.Ctx) ochazuke.Result {
			id, err := target(c, "fixture")
			if err != nil {
				return ochazuke.Failed(err)
			}
			app, i, err := findFixture(id)
			if err != nil {
				return ochazuke.Failed(err)
			}
			return storiesOf(app.Name, app.Fixtures[i].Name)
		}, "the stories one fixture builds (story list --app APP --fixture NAME): --target fixture/APP/NAME").Rows("story")
	r.Define("application").
		Verb("list", func(ochazuke.Ctx) ochazuke.Result { return listed("application") },
			"the host's apps, the default first: {id: application/NAME, label, data: {name, summary, fixtures, fields, default}}").Rows("application").
		Verb("show", func(c ochazuke.Ctx) ochazuke.Result {
			id, err := target(c, "application")
			if err != nil {
				return ochazuke.Failed(err)
			}
			for _, row := range appRows(t.cfg.Apps) {
				if row.ID == id {
					return ochazuke.Done(row)
				}
			}
			return ochazuke.Failed(fmt.Errorf("no app %q in %s (application list)", strings.TrimPrefix(id, "application/"), t.Prog))
		}, "one app: --target application/NAME; its row").
		Verb("stories", func(c ochazuke.Ctx) ochazuke.Result {
			id, err := target(c, "application")
			if err != nil {
				return ochazuke.Failed(err)
			}
			if _, err := findApp(strings.TrimPrefix(id, "application/")); err != nil {
				return ochazuke.Failed(err)
			}
			return storiesOf(strings.TrimPrefix(id, "application/"), "")
		}, "the stories one app builds (story list --app NAME): --target application/NAME").Rows("story").
		Verb("fixtures", func(c ochazuke.Ctx) ochazuke.Result {
			id, err := target(c, "application")
			if err != nil {
				return ochazuke.Failed(err)
			}
			return fixturesOf(strings.TrimPrefix(id, "application/"))
		}, "one app's fixtures (fixture list --app NAME): --target application/NAME").Rows("fixture")
	r.Define("verb").
		Verb("list", func(ochazuke.Ctx) ochazuke.Result { return listed("verb") },
			"the derived minimum, one row per (mode, noun, verb): {id, label, data: {mode, noun, verb}} ("+t.Prog+" verbs)").Rows("verb")
	r.Define("edge").
		Verb("list", func(ochazuke.Ctx) ochazuke.Result { return listed("edge") },
			"the mode edges the core stories take: {id, label, data: {from, to, via}} ("+t.Prog+" verbs)").Rows("edge")
	params(r, "story.list", map[string]ochazuke.Parameter{
		"app":     {Type: "string", Noun: "application", Description: "only the stories of this app"},
		"fixture": {Type: "string", Description: "only the stories of this fixture (of --app, else of any app)"},
	})
	params(r, "story.copy", map[string]ochazuke.Parameter{
		"as": {Type: "string", Required: true, Description: "the copy's name (its file is the name's slug, in the story's area)"},
	})
	params(r, "recording.list", map[string]ochazuke.Parameter{
		"story": {Type: "string", Description: "only the recordings of this story (its name)"},
	})
	params(r, "fixture.list", map[string]ochazuke.Parameter{
		"app": {Type: "string", Noun: "application", Description: "only the fixtures of this app"},
	})

	sh := ocli.New(r, t.cfg.name(), t.Stdin, t.Stdout, t.Stderr)
	sh.Usage = ocli.DefaultUsage(t.Prog)
	// Reveal makes the target row exist; show and run look it up afresh.
	sh.Reveal = func(id string) error {
		noun := nounOf(id)
		return lists[noun].SetItems([]ochazuke.ListItem{{ID: id, Label: id, Noun: noun, Data: id}})
	}
	return sh
}

// params declares an operation's parameters.
func params(r *ochazuke.Runtime, name string, p map[string]ochazuke.Parameter) {
	op := r.Operations[name]
	op.Parameters = p
	r.Operations[name] = op
}

// copyStory writes a copy of s named as into the user's stories, beside
// it the copy of its golden frame when it has one.
func (t *Tool) copyStory(s *story.Story, as string) (string, error) {
	as = strings.TrimSpace(as)
	if as == "" {
		return "", fmt.Errorf("story copy needs --as NAME")
	}
	c := *s
	c.Name = as
	path := filepath.Join(t.UserStories(), c.Area, story.Slug(as)+".json")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s exists", path)
	}
	path, err := story.WriteStory(t.UserStories(), &c)
	if err != nil {
		return "", err
	}
	if golden, err := s.ReadGolden(); err == nil {
		if err := os.WriteFile(strings.TrimSuffix(path, ".json")+".golden", golden, 0o644); err != nil {
			return path, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return path, err
	}
	return path, nil
}
