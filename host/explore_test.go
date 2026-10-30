package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/monogatari/explore"
)

// TestExploreWritesGapStories: rows story explore runs one story application
// and writes its report and one skipped gap story per finding, each a
// story that parses and plays.
func TestExploreWritesGapStories(t *testing.T) {
	tool, out := testTool(t)
	dir := t.TempDir()
	if err := tool.Explore([]string{"--depth", "2", "--steps", "60", "--out", dir, "motions/spill-down"}); err != nil {
		t.Fatalf("explore: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "rows story explore: 1 applications") {
		t.Fatalf("summary missing:\n%s", out)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*", "*.json"))
	if len(files) == 0 {
		t.Fatalf("no gap stories written:\n%s", out)
	}
	lib, err := tool.library("")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s, err := story.Parse(data, f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !strings.HasPrefix(s.Skip, "gap: ") || s.Area != "explore" {
			t.Errorf("%s: skip %q area %q, want a gap story in area explore", f, s.Skip, s.Area)
		}
		if res := lib.Run(s, story.RunOptions{}); res.Err != nil && !res.Skipped {
			t.Errorf("%s does not play: %v", f, res.Err)
		}
	}
}

// TestExploreDedupe: rows story explore --dedupe groups the findings of the
// reports under a directory and writes groups.json and one gap story per
// group on a story application, the same bytes on every run.
func TestExploreDedupe(t *testing.T) {
	tool, out := testTool(t)
	in := t.TempDir()
	reports := []explore.Report{
		{Subject: "motions/spill-down", Findings: []*explore.Finding{
			{Check: "operation-error", Summary: "history.repeat: nothing to repeat", Path: []string{"."}, Count: 2},
		}},
		{Subject: "core", Findings: []*explore.Finding{
			{Check: "operation-error", Summary: "app.history.repeat: nothing to repeat", Path: []string{"."}, Count: 1},
			{Check: "honest-hints", Summary: "x y.z fails when pressed: no", Path: []string{"x"}, Count: 1},
		}},
	}
	for _, rep := range reports {
		if err := os.WriteFile(filepath.Join(in, strings.ReplaceAll(rep.Subject, "/", "-")+".json"), rep.JSON(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var runs []string
	for range 2 {
		dir := t.TempDir()
		out.Reset()
		if err := tool.Explore([]string{"--dedupe", in, "--out", dir}); err != nil {
			t.Fatalf("dedupe: %v\n%s", err, out)
		}
		if !strings.Contains(out.String(), "2 reports, 3 findings, 2 distinct") {
			t.Fatalf("summary missing:\n%s", out)
		}
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		var all []string
		for _, f := range files {
			data, _ := os.ReadFile(f)
			all = append(all, filepath.Base(f)+"\n"+string(data))
		}
		runs = append(runs, out.String()+strings.Join(all, "\n"))
		if len(files) != 2 { // groups.json and the story of the repeat group (core is no story)
			t.Fatalf("files %v", files)
		}
		data, err := os.ReadFile(filepath.Join(dir, "02-operation-error-history-repeat-nothing-to-repeat.json"))
		if err != nil {
			t.Fatal(err)
		}
		s, err := story.Parse(data, "gaps/x.json")
		if err != nil {
			t.Fatal(err)
		}
		if s.Skip != "gap: operation-error: history.repeat: nothing to repeat" || len(s.Script) != 1 {
			t.Errorf("gap story skip %q script %v", s.Skip, s.Script)
		}
	}
	if runs[0] != runs[1] {
		t.Errorf("two runs differ:\n%s\n----\n%s", runs[0], runs[1])
	}
}
