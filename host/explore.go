package host

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	story "ofam.dev/monogatari"
	"ofam.dev/monogatari/explore"
	"ofam.dev/oden"
)

// Explore runs the state explorer over every story-built application (one
// run per distinct application: stories that differ only in script and
// expect share it) and prints each report; --out writes the reports and
// each finding as a skipped gap story.
func (t *Tool) Explore(args []string) error {
	flags := flag.NewFlagSet(t.Prog+" explore", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	dir := flags.String("dir", "", "read the core stories from this directory instead of the host's own")
	depth := flags.Int("depth", 3, "the longest key path explored")
	steps := flags.Int("steps", 2000, "actions played per application")
	out := flags.String("out", "", "write NAME.json reports and one gap story per finding under this directory")
	full := flags.Bool("compare-full", false, "also count states keyed with the screen (Full), to measure over-merging")
	top := flags.Int("top", 5, "findings listed per check (0 every one)")
	reproduce := flags.Bool("reproduce", false, "with --dedupe: replay each group's best story occurrence (explore.Reproduce) and say whether it still shows")
	dedupe := flags.String("dedupe", "", "explore nothing: group the findings of every report under this directory (NAME.json) by check and summary; --out writes groups.json and one gap story per group")
	watch := flags.Bool("watch", false, "watch the run in a full-screen TUI (the same walk and report; q stops watching)")
	delay := flags.Duration("delay", watchDelay, "with --watch: the pause after each step (0 is full speed)")
	names, err := parse(flags, args)
	if err != nil {
		return err
	}
	lib, err := t.library(*dir)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["delay"] && !*watch {
		return fmt.Errorf("--delay needs --watch")
	}
	if *dedupe != "" {
		if *watch {
			return fmt.Errorf("--watch watches a run; --dedupe runs nothing")
		}
		return t.dedupe(lib, *dedupe, *out, *reproduce)
	}
	apps := storyApps(lib, names)
	if len(apps) == 0 {
		return fmt.Errorf("no story or area matches %v", names)
	}
	budget := explore.Budget{Depth: *depth, Steps: *steps, CompareFull: *full}
	next := func(app *story.Story) explore.Report { return explore.Run(lib.Subject(app), budget) }
	if *watch {
		// The TUI watches the runs; the reports print after it, in order,
		// as the headless loop prints them.
		run := startWatchRun(lib, apps, budget, *delay)
		if err := t.watchTUI(run); err != nil {
			return err
		}
		next = func(*story.Story) explore.Report { return <-run.reports }
	}
	total := 0
	var reports []explore.Report
	for _, app := range apps {
		rep := next(app)
		fmt.Fprint(t.Stdout, rep.Text(*top))
		reports = append(reports, rep)
		total += len(rep.Findings)
		if *out != "" {
			if err := writeExplore(*out, app, rep); err != nil {
				return err
			}
		}
		if len(rep.Errors) > 0 {
			return &oden.ExitError{Code: 1}
		}
	}
	fmt.Fprint(t.Stdout, acrossApps(reports))
	if len(reports) > 0 {
		fmt.Fprint(t.Stdout, reports[0].Legend())
	}
	t.say("%s explore: %d applications, %d findings", t.Prog, len(apps), total)
	return nil
}

// acrossApps groups the findings of every report by check and summary:
// how many applications show each, and the shortest path among them.
func acrossApps(reports []explore.Report) string {
	type group struct {
		check, summary, subject string
		apps                    int
		path                    []string
	}
	groups := map[string]*group{}
	var order []string
	for _, rep := range reports {
		for _, f := range rep.Findings {
			key := f.Check + "\x00" + f.Summary
			g := groups[key]
			if g == nil {
				g = &group{check: f.Check, summary: f.Summary}
				groups[key] = g
				order = append(order, key)
			}
			g.apps++
			if g.path == nil || len(f.Path) < len(g.path) {
				g.path, g.subject = f.Path, rep.Subject
			}
		}
	}
	slices.Sort(order)
	var b strings.Builder
	fmt.Fprintf(&b, "across %d applications: %d distinct findings\n", len(reports), len(order))
	for _, key := range order {
		g := groups[key]
		fmt.Fprintf(&b, "  %s: %s (%d apps; %s: %s)\n", g.check, g.summary, g.apps, g.subject, strings.Join(g.path, " "))
	}
	return b.String()
}

// storyApps is one story per distinct application among the selected
// stories (all when names is empty), in library order; stories with a
// base (trimmed recordings) are left out. A skipped story (a gap) stands
// for its application only when no other story has it, after the rest,
// so a gap story copied from a story leaves that story's subject name.
func storyApps(lib story.Library, names []string) []*story.Story {
	seen := map[string]bool{}
	var out []*story.Story
	for _, gaps := range []bool{false, true} {
		for _, s := range lib.Stories {
			if s.Base != "" || (s.Skip != "") != gaps || !selected(s, names) {
				continue
			}
			key := appKey(s)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, s)
		}
	}
	return out
}

func selected(s *story.Story, names []string) bool {
	if len(names) == 0 {
		return true
	}
	file := strings.TrimSuffix(filepath.Base(s.Path), ".json")
	for _, n := range names {
		if s.Area == n || s.Name == n || story.Slug(s.Name) == n || s.Area+"/"+story.Slug(s.Name) == n || s.Area+"/"+file == n {
			return true
		}
	}
	return false
}

// appKey is the story without what does not change the application.
func appKey(s *story.Story) string {
	c := *s
	c.Name, c.Area, c.Skip, c.Script, c.Expect = "", "", "", nil, nil
	b, _ := json.Marshal(c)
	return string(b)
}

// writeExplore writes the report and one story per finding: the
// application's story with the finding's keys as its script, skipped as a
// gap so it documents the finding without failing a run.
func writeExplore(dir string, app *story.Story, rep explore.Report) error {
	slug := strings.ReplaceAll(rep.Subject, "/", "-")
	if err := os.MkdirAll(filepath.Join(dir, slug), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".json"), rep.JSON(), 0o644); err != nil {
		return err
	}
	for i, f := range rep.Findings {
		st := FindingStory(app, f)
		b, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return err
		}
		name := fmt.Sprintf("%02d-%s.json", i+1, story.Slug(f.Check))
		if err := os.WriteFile(filepath.Join(dir, slug, name), append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// FindingStory is the gap story of a finding: the application's story,
// the finding's path as its script (waits and resizes have no story event,
// so they are dropped and named in the skip), no expect.
func FindingStory(app *story.Story, f *explore.Finding) *story.Story {
	st := *app
	st.Name = fmt.Sprintf("explore %s: %s", f.Check, f.Summary)
	st.Area = "explore"
	st.Expect = nil
	st.Script = nil
	var dropped []string
	for _, a := range f.Path {
		switch {
		case a == explore.Paste:
			st.Script = append(st.Script, story.Event{Paste: explore.PasteText})
		case strings.HasPrefix(a, "<"):
			dropped = append(dropped, a)
		default:
			st.Script = append(st.Script, story.Event{Key: a})
		}
	}
	st.Skip = "gap: " + f.Check + ": " + f.Summary
	if len(dropped) > 0 {
		st.Skip += " (explore path " + strings.Join(f.Path, " ") + "; " + strings.Join(dropped, ", ") + " not replayed)"
	}
	return &st
}
