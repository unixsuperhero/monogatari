package host

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	story "ofam.dev/monogatari"
)

func TestExportTutorialReplay(t *testing.T) {
	tool, _ := testTool(t)
	out := t.TempDir()
	runTool(t, tool, "export", "tutorials/independent-motions", "--out", out, "--format", "json")
	path := filepath.Join(out, "tutorials", "independent-motions.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ex story.ExportedStory
	if err := json.Unmarshal(first, &ex); err != nil {
		t.Fatal(err)
	}
	if ex.App != "rows" || ex.Fixture != "plain" || len(ex.Chapters) != 3 {
		t.Fatalf("metadata: %+v", ex)
	}
	setup, down, up := ex.Chapters[0], ex.Chapters[1], ex.Chapters[2]
	if len(setup.Steps) != 2 || setup.Steps[1].Label != "Reset point" {
		t.Fatalf("setup: %+v", setup)
	}
	if down.Steps[0].HTML != up.Steps[0].HTML || down.Steps[0].HTML != setup.Steps[1].HTML {
		t.Fatal("actions did not reset to the same frame")
	}
	if down.Steps[1].Expect[0].Focus != "a3" || up.Steps[1].Expect[0].Focus != "a1" || up.Caption == "" || down.Steps[1].Keys[0] != "j" {
		t.Fatal("lost tutorial narration, keys or assertions")
	}
	runTool(t, tool, "export", "--out", out, "tutorials/independent-motions")
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Fatal("non-deterministic export")
	}
	if _, err := os.Stat(filepath.Join(out, "index.json")); err != nil {
		t.Fatal(err)
	}
}

func TestExportSelectionAndFailures(t *testing.T) {
	tool, _ := testTool(t)
	out := t.TempDir()
	runTool(t, tool, "export", "tutorials", "--out", out)
	data, _ := os.ReadFile(filepath.Join(out, "index.json"))
	var index struct {
		Stories []struct{ Area, File string }
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	core, err := tool.cfg.core("")
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, s := range core {
		if s.Area == "tutorials" && s.Skip == "" {
			want++
		}
	}
	if len(index.Stories) != want {
		t.Fatalf("area selection: %s", data)
	}
	for _, entry := range index.Stories {
		if entry.Area != "tutorials" || !filepath.IsLocal(entry.File) {
			t.Fatalf("wrong area or nonportable path: %+v", entry)
		}
	}
	for _, args := range [][]string{{"export"}, {"export", "--out", out, "--format", "svg"}, {"export", "unknown", "--out", out}} {
		if err := tool.Main(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	s := &story.Story{Name: "fails", Area: "test", App: "rows", Seed: json.RawMessage(`{"lists":[{"id":"a","rows":2}]}`), Actions: []story.Action{{Label: "bad", Keys: []story.Event{{Key: "j"}}, Expect: story.Expects{{Focus: "missing"}}}}}
	lib := story.Library{Apps: []story.App{rowsApp()}}
	if _, err := lib.Export(s); err == nil {
		t.Fatal("exported failing story")
	}
	s.Skip = "gap: example"
	if _, err := lib.Export(s); err == nil {
		t.Fatal("exported skipped story")
	}
}

func TestExportSandboxOwnership(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	sandbox := filepath.Join(root, "monogatari-export")
	if err := os.Mkdir(sandbox, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sandbox, "owner")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (story.Library{Apps: []story.App{rowsApp()}}).Export(&story.Story{Name: "example"}); err == nil {
		t.Fatal("reused an existing sandbox")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("touched pre-existing sandbox")
	}
}
