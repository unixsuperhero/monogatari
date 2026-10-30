package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
)

// rowsSeed is the rows test app's seed: lists of numbered rows (list a
// with 2 rows has a1 and a2) and the row that starts focused.
type rowsSeed struct {
	Lists []struct {
		ID   string `json:"id"`
		Rows int    `json:"rows"`
	} `json:"lists"`
	Focus string `json:"focus,omitempty"`
}

// rowsApp is the host tests' app, as the demo is o-story's: a page of the
// seed's lists, each drawn under its id, with ochazuke's motions between
// them. Its fixture "plain" builds the seed; "empty" ignores it.
func rowsApp() story.App {
	return story.App{
		Name:    "rows",
		Summary: "lists of numbered rows from the seed",
		Fixtures: []story.Fixture{
			{Name: "plain", Summary: "the seed's lists", Build: buildRows},
			{Name: "empty", Summary: "one list of one row, whatever the seed", Build: func(env story.Env) (story.Built, error) {
				env.Seed = json.RawMessage(`{"lists": [{"id": "e", "rows": 1}]}`)
				return buildRows(env)
			}},
		},
	}
}

// otherApp is a second app with rowsApp's fixtures under another name.
func otherApp() story.App {
	a := rowsApp()
	a.Name, a.Summary = "other", "rows under another name"
	return a
}

func buildRows(env story.Env) (story.Built, error) {
	var seed rowsSeed
	if len(env.Seed) > 0 {
		if err := json.Unmarshal(env.Seed, &seed); err != nil {
			return story.Built{}, err
		}
	}
	r, m, err := env.NewRuntime()
	if err != nil {
		return story.Built{}, err
	}
	r.Define("row")
	r.Define("app").Mode("normal").Bind("q", ochazuke.Do("app.quit"))
	lists := map[string]*ochazuke.List{}
	if _, err := r.AddPage("main", "Main", func(_ *ochazuke.Runtime, w, h int) ochazuke.Block {
		var b strings.Builder
		for _, l := range seed.Lists {
			list := lists[l.ID]
			b.WriteString(l.ID + "\n" + list.View(w, max(1, list.Height())) + "\n")
		}
		return ochazuke.Text(strings.TrimSuffix(b.String(), "\n"))
	}); err != nil {
		return story.Built{}, err
	}
	focus := seed.Focus
	for _, l := range seed.Lists {
		var items []ochazuke.ListItem
		for i := range l.Rows {
			id := fmt.Sprintf("%s%d", l.ID, i+1)
			items = append(items, ochazuke.ListItem{ID: id, Label: id, Noun: "row"})
		}
		list, err := r.AddList(l.ID, items, ochazuke.Parent("main"))
		if err != nil {
			return story.Built{}, err
		}
		lists[l.ID] = list
		if focus == "" {
			focus = list.FocusTarget()
		}
	}
	if err := r.OpenPage("main"); err != nil {
		return story.Built{}, err
	}
	if focus != "" {
		r.SetFocus(focus)
	}
	return story.Built{Model: m, Snapshot: func() (story.Snapshot, error) {
		s := seed
		s.Focus = r.Focus
		data, err := json.Marshal(s)
		return story.Snapshot{Seed: data}, err
	}}, nil
}

// testConfig is a host over the rows app and testdata/stories.
func testConfig() Config {
	return Config{Name: "rows-story", Prog: "rows story", Apps: []story.App{rowsApp(), otherApp()}, Stories: os.DirFS(filepath.Join("testdata", "stories")), Dir: filepath.Join("testdata", "stories")}
}

// TestStories is Test over the host's own stories.
func TestStories(t *testing.T) { Test(t, testConfig()) }
