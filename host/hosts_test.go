package host

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	story "ofam.dev/monogatari"
	"ofam.dev/oden"
)

// A host's name defaults to its Prog's slug: mtm story keeps its state
// under mtm-story.
func TestNameIsProgsSlug(t *testing.T) {
	for _, c := range []struct {
		cfg        Config
		name, prog string
	}{
		{Config{Prog: "mtm story"}, "mtm-story", "mtm story"},
		{Config{Name: "o-story", Prog: "mtm story"}, "o-story", "mtm story"},
		{Config{Name: "o-story"}, "o-story", "o-story"},
	} {
		if c.cfg.name() != c.name || c.cfg.prog() != c.prog {
			t.Errorf("%+v: name %q prog %q, want %q %q", c.cfg, c.cfg.name(), c.cfg.prog(), c.name, c.prog)
		}
	}
}

// Two hosts on one machine (one XDG environment) keep their own stories,
// user stories and recordings: each lists only its own.
func TestHostsKeepTheirOwnStoriesAndRecordings(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"XDG_DATA_HOME": filepath.Join(dir, "data"), "XDG_CONFIG_HOME": filepath.Join(dir, "config")}
	getenv := func(k string) string { return env[k] }
	first := testConfig()
	first.Getenv = getenv
	second := Config{Prog: "other story", Apps: []story.App{otherApp()}, Getenv: getenv, Stories: fstest.MapFS{
		"lists/second.json": {Data: []byte(`{"name": "the second host's story", "area": "lists", "seed": {"lists": [{"id": "s", "rows": 2}]}, "expect": {"focus": "s1"}}`)},
	}}
	var out bytes.Buffer
	a, b := New(first, strings.NewReader(""), &out, &out), New(second, strings.NewReader(""), &out, &out)
	if a.store().Dir == b.store().Dir || a.UserStories() == b.UserStories() {
		t.Fatalf("shared dirs: %s %s, %s %s", a.store().Dir, b.store().Dir, a.UserStories(), b.UserStories())
	}
	if !strings.HasSuffix(b.store().Dir, filepath.Join("data", "other-story")) {
		t.Fatalf("second store %s", b.store().Dir)
	}
	// A recording and a trimmed (user) story of the first host.
	s := spillStory(t)
	if _, err := a.store().Save(recording(s, time.Now(), "j", "j", "j"), "walk"); err != nil {
		t.Fatal(err)
	}
	if err := a.Trim([]string{"walk", "--from", "0", "--to", "1", "--as", "walk start"}); err != nil {
		t.Fatal(err, out.String())
	}
	run := func(tool *Tool, args ...string) string {
		t.Helper()
		out.Reset()
		if err := tool.Main(args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	for _, c := range []struct {
		tool       *Tool
		has, lacks []string
	}{
		{a, []string{"spill: j at the last row", "walk start"}, []string{"the second host's story"}},
		{b, []string{"the second host's story"}, []string{"spill: j at the last row", "walk start"}},
	} {
		list := run(c.tool, "list")
		for _, w := range c.has {
			if !strings.Contains(list, w) {
				t.Errorf("%s list lacks %q:\n%s", c.tool.Prog, w, list)
			}
		}
		for _, w := range c.lacks {
			if strings.Contains(list, w) {
				t.Errorf("%s list has %q:\n%s", c.tool.Prog, w, list)
			}
		}
	}
	if h := run(a, "history"); !strings.Contains(h, "walk") {
		t.Errorf("first history lacks its recording:\n%s", h)
	}
	if h := run(b, "history"); strings.Contains(h, "walk") {
		t.Errorf("second history has the first's recording:\n%s", h)
	}
	if rows := listRows(t, b, "recording"); len(rows) != 0 {
		t.Errorf("second recording list %+v", rows)
	}
	if res := run(b, "run"); !strings.Contains(res, "1 stories, 1 passed") {
		t.Errorf("second run:\n%s", res)
	}
}

// The relations: story.list {app, fixture} and application.stories,
// fixture.stories share one handler; fixture.list {app} and
// application.fixtures; recording.list {story} and story.recordings.
func TestCatalogRelations(t *testing.T) {
	tool, _ := testTool(t)
	ids := func(args ...string) []string {
		t.Helper()
		var rows []Row
		if err := json.Unmarshal([]byte(runTool(t, tool, append(args, "--json", "{}")...)), &rows); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	other := []string{"lists/other-app"}
	if got := ids("story", "list", "--app", "other"); !slices.Equal(got, other) {
		t.Errorf("story list --app other %q", got)
	}
	if got := ids("application", "stories", "--target", "application/other"); !slices.Equal(got, other) {
		t.Errorf("application stories %q", got)
	}
	empty := []string{"lists/one-row"}
	if got := ids("story", "list", "--app", "rows", "--fixture", "empty"); !slices.Equal(got, empty) {
		t.Errorf("story list --app rows --fixture empty %q", got)
	}
	if got := ids("fixture", "stories", "--target", "fixture/rows/empty"); !slices.Equal(got, empty) {
		t.Errorf("fixture stories %q", got)
	}
	if got := ids("fixture", "list"); !slices.Equal(got, []string{"fixture/rows/plain", "fixture/rows/empty", "fixture/other/plain", "fixture/other/empty"}) {
		t.Errorf("fixture list %q", got)
	}
	if got, want := ids("application", "fixtures", "--target", "application/other"), ids("fixture", "list", "--app", "other"); !slices.Equal(got, want) || len(got) != 2 {
		t.Errorf("application fixtures %q, fixture list --app other %q", got, want)
	}
	if got := ids("application", "list"); !slices.Equal(got, []string{"application/rows", "application/other"}) {
		t.Errorf("application list %q", got)
	}
	var fx Row
	if err := json.Unmarshal([]byte(runTool(t, tool, "story", "fixture", "--target", "lists/one-row", "--json", "{}")), &fx); err != nil || fx.ID != "fixture/rows/empty" {
		t.Errorf("story fixture %+v %v", fx, err)
	}
	var app Row
	if err := json.Unmarshal([]byte(runTool(t, tool, "application", "show", "--target", "application/rows", "--json", "{}")), &app); err != nil || app.ID != "application/rows" {
		t.Errorf("application show %+v %v", app, err)
	}
	s := spillStory(t)
	if _, err := tool.store().Save(recording(s, time.Now(), "j"), "walk"); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.store().Push(recording(s, time.Now(), "k")); err != nil {
		t.Fatal(err)
	}
	if got := ids("story", "recordings", "--target", "motions/spill-down"); !slices.Equal(got, []string{"recording/1", "recording/walk"}) {
		t.Errorf("story recordings %q", got)
	}
	if got := ids("recording", "list", "--story", "another story"); len(got) != 0 {
		t.Errorf("recording list --story another %q", got)
	}
}

// story.copy writes a copy under the user's stories, which then lists and
// runs; recording.delete removes one recording.
func TestStoryCopyAndRecordingDelete(t *testing.T) {
	tool, _ := testTool(t)
	var res map[string]string
	if err := json.Unmarshal([]byte(runTool(t, tool, "story", "copy", "--target", "motions/spill-down", "--as", "my spill", "--json", "{}")), &res); err != nil {
		t.Fatal(err)
	}
	if res["id"] != "motions/my-spill" || !strings.HasPrefix(res["path"], tool.UserStories()) {
		t.Fatalf("copy %v", res)
	}
	if out := runTool(t, tool, "run", "motions/my-spill"); !strings.Contains(out, "1 stories, 1 passed") {
		t.Fatalf("the copy does not run:\n%s", out)
	}
	if err := tool.Main([]string{"story", "copy", "--target", "motions/spill-down", "--as", "my spill"}); err == nil {
		t.Fatal("a copy over an existing story succeeded")
	}
	if _, err := tool.store().Save(recording(spillStory(t), time.Now(), "j"), "walk"); err != nil {
		t.Fatal(err)
	}
	runTool(t, tool, "recording", "delete", "--target", "recording/walk")
	if rows := listRows(t, tool, "recording"); len(rows) != 0 {
		t.Fatalf("after delete %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(tool.store().Dir, "saved", "walk.json")); !os.IsNotExist(err) {
		t.Fatalf("walk.json: %v", err)
	}
}

// Command mounts the host as an app's story command: the app's story
// list is the host's.
func TestCommandMountsUnderAnApp(t *testing.T) {
	cfg := testConfig()
	cfg.Getenv = func(string) string { return t.TempDir() }
	var out bytes.Buffer
	app := oden.New("rows", []string{"story", "list"}, oden.WithStdio(strings.NewReader(""), &out, &out))
	app.Add(Command(cfg))
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "spill: j at the last row") {
		t.Fatalf("rows story list:\n%s", out.String())
	}
}
