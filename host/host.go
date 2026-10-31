// Package host is a monogatari host: the story tester an app mounts. It
// is the CLI (run, open, list, history, keep, replay, trim, explore,
// verbs), the catalog shell (the story, recording, fixture, app, verb and
// edge nouns), the index TUI, the player and explore's watch mode, over
// the stories and apps a Config names. ochazuke's o-story is one host
// (the demo app's); an app's tester (mtm story) is another.
//
//	PROG                            the index; enter opens a story, q leaves it
//	PROG run [NAME|AREA...] [--update] [--dir DIR] [-v]
//	PROG run --from REF [--expect JSON]
//	PROG list
//	PROG history
//	PROG keep N NAME
//	PROG replay REF
//	PROG trim REF [--from I] [--to J] [--at K] [--as NAME] [--snapshot]
//	PROG verbs
//	PROG explore [NAME|AREA...] [--dir DIR] [--depth N] [--steps N] [--out DIR] [--compare-full]
//	PROG catalog                    the nouns as JSON
//	PROG NOUN VERB [--target ID]    story, recording, fixture, app, verb, edge
//
// REF is a history number (1 newest) or a saved recording's name.
// Stories come from Config.Stories (or --dir) and from
// $XDG_CONFIG_HOME/NAME/stories; recordings live under
// $XDG_DATA_HOME/NAME (history/ ring of 5, saved/), NAME being the host's
// name (Config.Name), so two hosts never share stories or recordings.
//
// Moved from ochazuke's cmd/o-story at 3a062c0 (lane L2 of the
// o-story-for-apps plan).
package host

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	story "github.com/unixsuperhero/monogatari"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

// Config is one host: its name, the apps its stories build and its
// stories.
type Config struct {
	// Name is the host's name: the title of its index and catalog, and the
	// slug of its state dirs ($XDG_CONFIG_HOME/NAME/stories,
	// $XDG_DATA_HOME/NAME). "" is Slug(Prog), so "mtm story" keeps its
	// stories under mtm-story.
	Name string
	// Prog is the command line that runs the host, in usage and messages
	// ("o-story", "mtm story"); "" is Name.
	Prog string
	// Apps are the apps the host's stories build; the first is the default
	// for a story that names none.
	Apps []story.App
	// Stories is the host's own stories, the FS's root their directory
	// (fs.Sub of an embedded stories/).
	Stories fs.FS
	// Dir is the same stories on disk (the repo's story directory), which
	// Test reads so its Update rewrites the goldens there; "" reads
	// Stories.
	Dir string
	// Getenv reads the environment (the XDG dirs); nil is os.Getenv.
	Getenv func(string) string
	// Update makes Test rewrite the golden frames (a test's -update flag).
	Update bool
}

// name is the host's name.
func (c Config) name() string {
	if c.Name != "" {
		return c.Name
	}
	return story.Slug(c.Prog)
}

// prog is the host's command line.
func (c Config) prog() string {
	if c.Prog != "" {
		return c.Prog
	}
	return c.name()
}

// core loads the host's own stories: dir on disk when set, else Stories.
func (c Config) core(dir string) ([]*story.Story, error) {
	if dir != "" {
		return story.LoadDir(dir)
	}
	if c.Stories == nil {
		return nil, nil
	}
	return story.LoadFS(c.Stories, ".")
}

// Main runs the host with args (the words after its command), the TUI
// when there are none.
func Main(cfg Config, args []string, stdout, stderr io.Writer) error {
	return New(cfg, os.Stdin, stdout, stderr).Main(args)
}

// Command is the host as an oden command named story, for an app to
// mount (inside its //go:build monogatari tester build).
func Command(cfg Config) oden.Command {
	return oden.Command{
		Name:    "story",
		Summary: "the app's stories: run, open, record, trim and explore them (" + cfg.prog() + " help)",
		Handle: func(c *oden.Ctx) error {
			return New(cfg, c.Stdin, c.Stdout, c.Stderr).Main(c.Args)
		},
	}
}

// Test plays every one of the host's stories headless as a subtest, in
// parallel: per-step and final expectations, else the golden frame
// (rewritten when cfg.Update). A story marked skip (a known gap) runs but
// is reported as skipped. It reads cfg.Dir when set, else cfg.Stories.
func Test(t *testing.T, cfg Config) {
	t.Helper()
	stories, err := cfg.core(cfg.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(stories) == 0 {
		t.Fatalf("%s has no stories", cfg.name())
	}
	story.Sort(stories)
	lib := story.Library{Stories: stories, Store: story.Store{Dir: t.TempDir()}, Apps: cfg.Apps}
	for _, s := range stories {
		t.Run(s.Area+"/"+story.Slug(s.Name), func(t *testing.T) {
			t.Parallel()
			res := lib.Run(s, story.RunOptions{Golden: true, Update: cfg.Update})
			if res.Skipped {
				t.Skipf("%s (passes now: %v)", s.Skip, res.Err == nil && len(res.Failed) == 0)
			}
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			if len(res.Failed) > 0 {
				t.Fatal(strings.Join(res.Failed, "\n"))
			}
		})
	}
}

// Tool is one invocation of a host: its config, streams and environment.
type Tool struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Prog           string

	cfg Config
}

// New is an invocation of the host cfg over the given streams.
func New(cfg Config, stdin io.Reader, stdout, stderr io.Writer) *Tool {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Tool{Stdin: stdin, Stdout: stdout, Stderr: stderr, Getenv: getenv, Prog: cfg.prog(), cfg: cfg}
}

// Main dispatches through oden: inline commands, the TUI with no words.
func (t *Tool) Main(args []string) error {
	app := oden.New(t.cfg.name(), args, oden.WithBuiltins(), oden.WithStdio(t.Stdin, t.Stdout, t.Stderr), oden.WithDefault(func(_ context.Context, a []string) error {
		if len(a) > 0 {
			return oden.ErrNotFound
		}
		return t.TUI(nil)
	}))
	app.Add(
		oden.Command{Name: "run", Summary: "play stories headless and check them (expect, else the golden frame); --from REF plays a recording", Run: func(_ context.Context, a []string) error { return t.Run(a) }},
		oden.Command{Name: "export", Summary: "export checked stories as portable HTML frames in JSON; --out DIR --format json", Run: func(_ context.Context, a []string) error { return t.Export(a) }},
		oden.Command{Name: "open", Summary: "open NAME: open one story live in the real chrome (q returns to the index)", Run: func(_ context.Context, a []string) error { return t.Open(a) }},
		oden.Command{Name: "list", Summary: "the stories by area", Run: func(_ context.Context, a []string) error { return t.List(a) }},
		oden.Command{Name: "history", Summary: "the ring of the last 5 recordings, newest first, and the saved ones", Run: func(_ context.Context, a []string) error { return t.History(a) }},
		oden.Command{Name: "keep", Summary: "keep N NAME: move history entry N to saved/NAME.json", Run: func(_ context.Context, a []string) error { return t.Keep(a) }},
		oden.Command{Name: "replay", Summary: "replay REF live in the TUI with the recorded timing", Run: func(_ context.Context, a []string) error { return t.Replay(a) }},
		oden.Command{Name: "trim", Summary: "trim REF: write a story of a window of the recording's events", Run: func(_ context.Context, a []string) error { return t.Trim(a) }},
		oden.Command{Name: "explore", Summary: "explore every story-built application key by key and report bad states (explore.Run); --out writes gap stories", Run: func(_ context.Context, a []string) error { return t.Explore(a) }},
		oden.Command{Name: "verbs", Summary: "the noun.verb set the core stories' scripts resolve to, by noun", Run: func(_ context.Context, a []string) error { return t.Verbs(a) }},
	)
	ocli.Mount(app, t.shell())
	return app.Run(context.Background())
}

// parse parses flags that may come before or after the positional words.
func parse(flags *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, &oden.ExitError{Code: 2}
		}
		args = flags.Args()
		if len(args) == 0 {
			return words, nil
		}
		words = append(words, args[0])
		args = args[1:]
	}
}

func (t *Tool) say(format string, a ...any) { fmt.Fprintf(t.Stdout, format+"\n", a...) }

func (t *Tool) configDir() string {
	base := t.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, t.cfg.name())
}

// UserStories is where trimmed and hand-made stories of this user live.
func (t *Tool) UserStories() string { return filepath.Join(t.configDir(), "stories") }

// store is this host's recordings.
func (t *Tool) store() story.Store { return story.DefaultStore(t.Getenv, t.cfg.name()) }

// library loads the core stories (Config.Stories, or dir on disk) and the
// user's.
func (t *Tool) library(dir string) (story.Library, error) {
	core, err := t.cfg.core(dir)
	if err != nil {
		return story.Library{}, err
	}
	user, err := story.LoadDir(t.UserStories())
	if err != nil {
		return story.Library{}, err
	}
	all := append(core, user...)
	story.Sort(all)
	return story.Library{Stories: all, Store: t.store(), Apps: t.cfg.Apps}, nil
}

// Run plays the named stories (every one when none is named; an area name
// selects the area) and prints one line each and a summary.
func (t *Tool) Run(args []string) error {
	flags := flag.NewFlagSet(t.Prog+" run", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	update := flags.Bool("update", false, "rewrite the golden frames (needs --dir: goldens are written on disk)")
	dir := flags.String("dir", "", "read the core stories from this directory instead of the host's own")
	from := flags.String("from", "", "play a recording (history number or saved name) headless instead of stories")
	expect := flags.String("expect", "", "with --from: an expect (JSON object or list) to assert")
	verbose := flags.Bool("v", false, "print the failing frames")
	names, err := parse(flags, args)
	if err != nil {
		return err
	}
	lib, err := t.library(*dir)
	if err != nil {
		return err
	}
	if *from != "" {
		e, err := lib.Store.Load(*from)
		if err != nil {
			return err
		}
		var x story.Expects
		if *expect != "" {
			if err := json.Unmarshal([]byte(*expect), &x); err != nil {
				return fmt.Errorf("--expect: %w", err)
			}
		}
		res := lib.Replay(e.Rec, x)
		t.report(res, *verbose)
		if !res.OK() {
			return &oden.ExitError{Code: 1}
		}
		t.say("%s run: recording %s replayed, %d events, ok", t.Prog, e.Name, len(e.Rec.Events))
		return nil
	}
	selected := lib.Stories
	if len(names) > 0 {
		selected = nil
		for _, n := range names {
			var hit []*story.Story
			for _, s := range lib.Stories {
				if s.Area == n {
					hit = append(hit, s)
				}
			}
			if s := story.Find(lib.Stories, n); s != nil {
				hit = append(hit, s)
			}
			if len(hit) == 0 {
				return fmt.Errorf("no story or area %q", n)
			}
			selected = append(selected, hit...)
		}
	}
	passed, failed, skipped := 0, 0, 0
	for _, s := range selected {
		res := lib.Run(s, story.RunOptions{Golden: true, Update: *update})
		t.report(res, *verbose)
		switch {
		case res.Skipped:
			skipped++
		case res.OK():
			passed++
		default:
			failed++
		}
	}
	t.say("%s run: %d stories, %d passed, %d failed, %d skipped (gaps)", t.Prog, len(selected), passed, failed, skipped)
	if failed > 0 {
		return &oden.ExitError{Code: 1}
	}
	return nil
}

func (t *Tool) report(res story.Result, verbose bool) {
	s := res.Story
	id := s.Area + "/" + story.Slug(s.Name)
	switch {
	case res.Skipped:
		t.say("skip  %-48s %s", id, s.Skip)
	case res.Updated:
		t.say("upd   %-48s %s", id, s.Name)
	case res.OK():
		t.say("ok    %-48s %s", id, s.Name)
	default:
		t.say("FAIL  %-48s %s", id, s.Name)
		if res.Err != nil {
			t.say("      %s", res.Err)
		}
		for _, f := range res.Failed {
			if !verbose {
				f, _, _ = strings.Cut(f, "\nframe:")
				f, _, _ = strings.Cut(f, "\ngot:")
			}
			t.say("      %s", strings.ReplaceAll(f, "\n", "\n      "))
		}
	}
}

// List prints the stories by area.
func (t *Tool) List(args []string) error {
	lib, err := t.library("")
	if err != nil {
		return err
	}
	area := ""
	for _, s := range lib.Stories {
		if s.Area != area {
			area = s.Area
			t.say("%s", area)
		}
		gap := ""
		if s.Skip != "" {
			gap = "  [" + s.Skip + "]"
		}
		t.say("  %-28s %s%s", story.Slug(s.Name), s.Name, gap)
	}
	return nil
}

// History prints the ring (numbered, newest first) and the saved names.
func (t *Tool) History(args []string) error {
	store := t.store()
	hist, err := store.History()
	if err != nil {
		return err
	}
	t.say("history (%s, last %d):", filepath.Join(store.Dir, "history"), story.HistorySize)
	for _, e := range hist {
		t.say("  %d  %s  %-40s %d events", e.N, e.Rec.Started.Local().Format("2006-01-02 15:04:05"), e.Rec.Story, len(e.Rec.Events))
	}
	saved, err := store.Saved()
	if err != nil {
		return err
	}
	t.say("saved (%s):", filepath.Join(store.Dir, "saved"))
	for _, e := range saved {
		t.say("  %-20s %-40s %d events", e.Name, e.Rec.Story, len(e.Rec.Events))
	}
	return nil
}

// Keep promotes history entry N to saved/NAME.json.
func (t *Tool) Keep(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: %s keep N NAME", t.Prog)
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("keep: %q is not a history number", args[0])
	}
	dest, err := t.store().Keep(n, args[1])
	if err != nil {
		return err
	}
	t.say("kept %s", dest)
	return nil
}

// Replay opens a recording's story in the TUI and plays its events live.
func (t *Tool) Replay(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: %s replay REF", t.Prog)
	}
	e, err := t.store().Load(args[0])
	if err != nil {
		return err
	}
	return t.TUI(e.Rec)
}

// Trim writes a story of a window of a recording's (or a story's) events.
func (t *Tool) Trim(args []string) error {
	flags := flag.NewFlagSet(t.Prog+" trim", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	from := flags.Int("from", -1, "first event of the window (0-based; default --at minus 3)")
	to := flags.Int("to", -1, "last event of the window (inclusive; default --at plus 2)")
	at := flags.Int("at", -1, "the failing event; centres the default window")
	as := flags.String("as", "", "the new story's name (default REF-I-J)")
	snapshot := flags.Bool("snapshot", false, "bake the before-state into the story's page instead of naming a base")
	words, err := parse(flags, args)
	if err != nil {
		return err
	}
	if len(words) != 1 {
		return fmt.Errorf("usage: %s trim REF [--from I] [--to J] [--at K] [--as NAME] [--snapshot]", t.Prog)
	}
	lib, err := t.library("")
	if err != nil {
		return err
	}
	ref := words[0]
	var base string
	var initial *story.Story
	var events []story.Event
	if s := story.Find(lib.Stories, ref); s != nil {
		resolved, prefix, err := lib.Resolve(s)
		if err != nil {
			return err
		}
		base, initial, events = "story:"+s.Name, resolved, append(prefix, s.Script...)
	} else {
		e, err := lib.Store.Load(ref)
		if err != nil {
			return err
		}
		initial, events = e.Rec.Initial, e.Rec.Events
		name := e.Name
		if e.N > 0 && !*snapshot {
			// A ring entry is dropped after 5 more sessions: a story that
			// names it as its base keeps a saved copy.
			name = story.Slug(fmt.Sprintf("%s-base-%s", e.Rec.Story, e.Rec.Started.UTC().Format("20060102-150405")))
			if _, err := lib.Store.Load(name); err != nil {
				if _, err := lib.Store.Save(e.Rec, name); err != nil {
					return err
				}
			}
		}
		base = "recording:" + name
	}
	spec := story.TrimSpec{From: *from, To: *to, At: *at, As: *as, Snapshot: *snapshot}
	if spec.As == "" {
		i, j, err := spec.Window(len(events))
		if err != nil {
			return err
		}
		spec.As = fmt.Sprintf("%s %d-%d", story.Slug(strings.TrimPrefix(strings.TrimPrefix(base, "story:"), "recording:")), i, j)
	}
	out, warn, err := lib.Trim(base, initial, events, spec)
	if err != nil {
		return err
	}
	path, err := story.WriteStory(t.UserStories(), out)
	if err != nil {
		return err
	}
	for _, w := range warn {
		t.say("note: %s", w)
	}
	t.say("wrote %s", path)
	return nil
}

// TUI runs the index (and, with a recording, replays it first).
func (t *Tool) TUI(replay *story.Recording) error {
	if replay != nil {
		return t.tui(startReplay{replay})
	}
	return t.tui(nil)
}

// Open opens one story (NAME, AREA/NAME or its title) live in the TUI.
func (t *Tool) Open(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: %s open NAME", t.Prog)
	}
	lib, err := t.library("")
	if err != nil {
		return err
	}
	s := story.Find(lib.Stories, args[0])
	if s == nil {
		return fmt.Errorf("no story %q (%s list)", args[0], t.Prog)
	}
	return t.tui(startStory{s})
}

// tui runs the index, sending first (when set) once it starts.
func (t *Tool) tui(first tea.Msg) error {
	lib, err := t.library("")
	if err != nil {
		return err
	}
	h, err := newHost(lib, t.cfg.name())
	if err != nil {
		return err
	}
	p := tea.NewProgram(h)
	if first != nil {
		go p.Send(first)
	}
	_, err = p.Run()
	if errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	return err
}
