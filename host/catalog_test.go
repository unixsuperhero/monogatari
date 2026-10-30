package host

import (
	"bufio"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	story "github.com/unixsuperhero/monogatari"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

func runTool(t *testing.T, tool *Tool, args ...string) string {
	t.Helper()
	out := tool.Stdout.(interface {
		Reset()
		String() string
	})
	out.Reset()
	if err := tool.Main(args); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func listRows(t *testing.T, tool *Tool, noun string) []Row {
	t.Helper()
	var rows []Row
	if err := json.Unmarshal([]byte(runTool(t, tool, noun, "list", "--json", "{}")), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestCatalogDeclaresTheDataNouns(t *testing.T) {
	tool, _ := testTool(t)
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(runTool(t, tool, "catalog")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != 1 || doc.Name != "rows-story" || doc.State != "story.state" {
		t.Fatalf("schema %d name %q state %q", doc.Schema, doc.Name, doc.State)
	}
	want := map[string][]string{
		"story":       {"copy", "fixture", "list", "recordings", "run", "show", "state"},
		"recording":   {"delete", "list", "show"},
		"fixture":     {"list", "show", "stories"},
		"application": {"fixtures", "list", "show", "stories"},
		"verb":        {"list"},
		"edge":        {"list"},
	}
	for noun, verbs := range want {
		i := slices.IndexFunc(doc.Nouns, func(n ocli.CatalogNoun) bool { return n.Name == noun })
		if i < 0 {
			t.Fatalf("no noun %s", noun)
		}
		var got []string
		for _, v := range doc.Nouns[i].Verbs {
			got = append(got, v.Name)
			if v.Name == "list" && v.Rows != noun {
				t.Errorf("%s list rows %q", noun, v.Rows)
			}
		}
		if !slices.Equal(got, verbs) {
			t.Errorf("%s verbs %q, want %q", noun, got, verbs)
		}
	}
}

func TestStoryListRowsAreTheEmbeddedStories(t *testing.T) {
	tool, _ := testTool(t)
	core, err := testConfig().core("")
	if err != nil {
		t.Fatal(err)
	}
	story.Sort(core)
	rows := listRows(t, tool, "story")
	if len(rows) != len(core) {
		t.Fatalf("%d rows, %d stories", len(rows), len(core))
	}
	for i, s := range core {
		data, _ := rows[i].Data.(map[string]any)
		id := s.Area + "/" + strings.TrimSuffix(filepath.Base(s.Path), ".json")
		if rows[i].ID != id || rows[i].Label != s.Name || data["area"] != s.Area || data["steps"] != float64(len(s.Script)) || data["skipped"] != (s.Skip != "") {
			t.Fatalf("row %d %+v for story %s", i, rows[i], id)
		}
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(runTool(t, tool, "story", "run", "--target", rows[0].ID)), &res); err != nil {
		t.Fatal(err)
	}
	if res["ok"] != true || res["id"] != rows[0].ID {
		t.Fatalf("run %v", res)
	}
}

func TestVerbAndEdgeListsMatchVerbs(t *testing.T) {
	tool, _ := testTool(t)
	var wantVerbs, wantEdges []string
	for _, line := range strings.Split(runTool(t, tool, "verbs"), "\n") {
		if cells := strings.Split(line, " | "); len(cells) == 3 && strings.HasPrefix(line, "| ") && !strings.HasPrefix(line, "| mode") {
			for _, v := range strings.Fields(strings.TrimSuffix(cells[2], " |")) {
				wantVerbs = append(wantVerbs, strings.TrimPrefix(cells[0], "| ")+" "+cells[1]+" "+strings.Trim(v, "`"))
			}
		}
		if strings.HasPrefix(line, "- `") {
			wantEdges = append(wantEdges, strings.NewReplacer("- ", "", "`", "").Replace(line))
		}
	}
	var gotVerbs, gotEdges []string
	for _, r := range listRows(t, tool, "verb") {
		gotVerbs = append(gotVerbs, r.Label)
	}
	for _, r := range listRows(t, tool, "edge") {
		gotEdges = append(gotEdges, r.Label)
	}
	if len(wantVerbs) == 0 || !slices.Equal(gotVerbs, wantVerbs) {
		t.Fatalf("verb list\n%q\nverbs\n%q", gotVerbs, wantVerbs)
	}
	if len(wantEdges) == 0 || !slices.Equal(gotEdges, wantEdges) {
		t.Fatalf("edge list\n%q\nverbs\n%q", gotEdges, wantEdges)
	}
}

func TestStateIsEveryListAsJSONLines(t *testing.T) {
	tool, _ := testTool(t)
	if _, err := tool.store().Save(recording(spillStory(t), time.Now(), "j"), "walk"); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{}
	for _, noun := range catalogNouns {
		want[noun] = len(listRows(t, tool, noun))
	}
	if want["recording"] != 1 {
		t.Fatalf("recordings %d", want["recording"])
	}
	got := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(runTool(t, tool, "story", "state")))
	for sc.Scan() {
		var row ocli.Row
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil || row.ID == "" {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		got[row.Noun]++
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("state %v, lists %v", got, want)
	}
	if out := runTool(t, tool, "recording", "show", "--target", "recording/walk"); !strings.Contains(out, `"events"`) {
		t.Fatalf("show %s", out)
	}
}
