package monogatari_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/monogatari"
	"ofam.dev/monogatari/explore"
	"ofam.dev/ochazuke"
)

// counter is a test app: a page that shows a count (bumped by b, started
// from the seed), whether its state directory was seen before, and how
// many times the app was built. r resets the story through Env.Reset.
type counter struct {
	builds atomic.Int32
}

func (c *counter) app() monogatari.App {
	return monogatari.App{
		Name: "counter",
		Fixtures: []monogatari.Fixture{
			{Name: "plain", Summary: "count from zero", Build: c.build},
			{Name: "seeded", Summary: "count from the seed's start", Build: c.build},
		},
		Expected: func(op, err string) bool { return err == "counter refuses" },
	}
}

func (c *counter) build(env monogatari.Env) (monogatari.Built, error) {
	n := c.builds.Add(1)
	var seed struct {
		Start int `json:"start"`
	}
	if len(env.Seed) > 0 {
		if err := json.Unmarshal(env.Seed, &seed); err != nil {
			return monogatari.Built{}, err
		}
	}
	count := seed.Start
	state := "none"
	if env.StateDir != "" {
		mark := filepath.Join(env.StateDir, "seen")
		if _, err := os.Stat(mark); err == nil {
			state = "seen"
		} else {
			state = "new"
			if err := os.WriteFile(mark, nil, 0o644); err != nil {
				return monogatari.Built{}, err
			}
		}
	}
	r, m, err := env.NewRuntime()
	if err != nil {
		return monogatari.Built{}, err
	}
	r.Define("counter").
		Verb("bump", func(ochazuke.Ctx) ochazuke.Result { count++; return ochazuke.Done(nil) }, "Count one more").
		Verb("reset", func(ochazuke.Ctx) ochazuke.Result { env.Reset(); return ochazuke.Done(nil) }, "Rebuild the story").
		Mode("normal").
		Bind("b", ochazuke.Do("counter.bump")).
		Bind("r", ochazuke.Do("counter.reset"))
	if _, err := r.AddPage("main", "Main", func(*ochazuke.Runtime, int, int) ochazuke.Block {
		return ochazuke.Text(fmt.Sprintf("count %d state %s build %d", count, state, n))
	}, ochazuke.Kind("counter")); err != nil {
		return monogatari.Built{}, err
	}
	if err := r.OpenPage("main"); err != nil {
		return monogatari.Built{}, err
	}
	return monogatari.Built{Model: m}, nil
}

// wrapper is an app whose own tea.Model wraps the ochazuke model, as
// mtm's App does: w is its own key (the model never sees it) and its View
// adds a line the model does not draw.
type wrapper struct {
	m      *ochazuke.Model
	wraps  int
	inited bool
}

func (w *wrapper) Init() tea.Cmd { w.inited = true; return w.m.Init() }

func (w *wrapper) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "w" {
		w.wraps++
		return w, nil
	}
	_, cmd := w.m.Update(msg)
	return w, cmd
}

func (w *wrapper) View() tea.View {
	v := w.m.View()
	v.Content = fmt.Sprintf("wrapped %d init %v\n%s", w.wraps, w.inited, v.Content)
	return v
}

func (c *counter) wrapped() monogatari.App {
	app := c.app()
	app.Name = "wrapped"
	app.Fixtures = []monogatari.Fixture{{Name: "plain", Build: func(env monogatari.Env) (monogatari.Built, error) {
		b, err := c.build(env)
		if err != nil {
			return b, err
		}
		b.Program, b.Init = &wrapper{m: b.Model}, true
		return b, nil
	}}}
	return app
}

func parse(t *testing.T, doc string) *monogatari.Story {
	t.Helper()
	s, err := monogatari.Parse([]byte(doc), "test/story.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func run(t *testing.T, lib monogatari.Library, doc string) monogatari.Result {
	t.Helper()
	return lib.Run(parse(t, doc), monogatari.RunOptions{})
}

func ok(t *testing.T, res monogatari.Result) {
	t.Helper()
	if res.Err != nil || len(res.Failed) > 0 {
		t.Fatalf("err %v, failed:\n%s", res.Err, strings.Join(res.Failed, "\n"))
	}
}

// A story with no app builds the host's first app's first fixture; app,
// fixture and seed pick another.
func TestAppDefaultsAndSeed(t *testing.T) {
	c := &counter{}
	lib := monogatari.Library{Apps: []monogatari.App{c.app(), c.wrapped()}}
	ok(t, run(t, lib, `{"name": "default", "script": ["b"], "expect": {"contains": ["count 1 state none"]}}`))
	ok(t, run(t, lib, `{"name": "seeded", "app": "counter", "fixture": "seeded", "seed": {"start": 40}, "script": ["b", "b"], "expect": {"contains": ["count 42"]}}`))
}

// The driver sends events to the app's own program and the frame is the
// program's view; with Init the program's Init runs before the first event.
func TestAppProgramWrapper(t *testing.T) {
	c := &counter{}
	lib := monogatari.Library{Apps: []monogatari.App{c.wrapped()}}
	ok(t, run(t, lib, `{"name": "wrap", "script": ["w", "w", "b"], "expect": {"contains": ["wrapped 2 init true", "count 1"]}}`))
	// Through the model alone the wrapper's line is not drawn: the expect
	// needs the wrapper.
	plain := monogatari.Library{Apps: []monogatari.App{c.app()}}
	res := run(t, plain, `{"name": "wrap", "script": ["w"], "expect": {"contains": ["wrapped 1"]}}`)
	if res.Err != nil || len(res.Failed) == 0 {
		t.Fatalf("a plain app drew the wrapper's line: %v %v", res.Err, res.Failed)
	}
}

// reset (Env.Reset from a verb) and restart rebuild through the app's
// fixture: a reset gets a new state directory, a restart keeps it.
func TestAppResetAndRestart(t *testing.T) {
	c := &counter{}
	lib := monogatari.Library{Apps: []monogatari.App{c.wrapped()}}
	ok(t, run(t, lib, `{"name": "reset", "state": true, "script": ["w", "b", "r"],
		"expect": [{"after": 2, "contains": ["wrapped 1", "count 1 state new build 1"]},
		           {"contains": ["wrapped 0", "count 0 state new build 2"]}]}`))
	ok(t, run(t, lib, `{"name": "restart", "state": true, "script": ["b", {"restart": true}],
		"expect": [{"after": 1, "contains": ["count 1 state new"]},
		           {"contains": ["count 0 state seen"]}]}`))
}

// An app or fixture the host does not have, and an app field the story's
// app does not read, fail the build saying so.
func TestAppLookupFailures(t *testing.T) {
	c := &counter{}
	lib := monogatari.Library{Apps: []monogatari.App{c.app()}}
	for doc, want := range map[string]string{
		`{"name": "x", "app": "mtm"}`:                     `app "mtm" is not in this host (run it with mtm story)`,
		`{"name": "x", "fixture": "nope"}`:                `fixture "nope" is not in app "counter" (it has plain, seeded)`,
		`{"name": "x", "page": {"lists": [{"id": "a"}]}}`: `story field "page" is not one app "counter" reads`,
	} {
		res := run(t, lib, doc)
		if res.Err == nil || res.Err.Error() != want {
			t.Errorf("%s: err %v, want %q", doc, res.Err, want)
		}
	}
	failing := monogatari.App{Name: "failing", Fixtures: []monogatari.Fixture{{Name: "plain", Build: func(monogatari.Env) (monogatari.Built, error) {
		return monogatari.Built{}, errors.New("no seed")
	}}}}
	if res := run(t, monogatari.Library{Apps: []monogatari.App{failing}}, `{"name": "x", "state": true}`); res.Err == nil || res.Err.Error() != "no seed" {
		t.Errorf("a failing fixture: err %v, want its error", res.Err)
	}
	if res := run(t, monogatari.Library{}, `{"name": "x"}`); res.Err == nil {
		t.Error("a host with no app built a story")
	}
}

// Fields keep an app's top-level fields through a parse, a marshal and a
// parse again, and are decoded strictly.
func TestStoryFieldsRoundTrip(t *testing.T) {
	s := parse(t, `{"name": "x", "page": {"id": "p"}, "pages": [{"id": "q"}], "script": ["b"]}`)
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	again := parse(t, string(data))
	var page struct {
		ID string `json:"id"`
	}
	if has, err := again.Field("page", &page); !has || err != nil || page.ID != "p" {
		t.Fatalf("page %+v has %v err %v from %s", page, has, err, data)
	}
	if _, err := again.Field("pages", &[]struct{}{}); err == nil {
		t.Fatal("a strict decode accepted an unknown field")
	}
	if _, err := monogatari.Parse([]byte(`{"name": "x", "checks": [{"nope": 1}]}`), "x.json"); err == nil {
		t.Fatal("an unknown field inside the format's own fields parsed")
	}
}

// The explorer drives the app's program when the fixture has one
// (Library.Subject, explore.Subject.Program).
func TestAppExploreProgram(t *testing.T) {
	c := &counter{}
	lib := monogatari.Library{Apps: []monogatari.App{c.wrapped()}}
	s := parse(t, `{"name": "x", "area": "t"}`)
	frames, err := explore.Frames(lib.Subject(s), []string{"w", "w"})
	if err != nil {
		t.Fatal(err)
	}
	if last := frames[len(frames)-1]; !strings.Contains(last, "wrapped 2 init true") {
		t.Fatalf("the explorer drove past the wrapper:\n%s", last)
	}
	if sub := lib.Subject(s); !sub.Expected("counter.bump", "counter refuses") || sub.Expected("counter.bump", "other") {
		t.Fatal("the app's Expected is not the subject's")
	}
}
