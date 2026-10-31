package monogatari_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/monogatari"
)

func TestTutorialActionsResetStateAndRunSetup(t *testing.T) {
	c := &counter{}
	lib := monogatari.Library{Apps: []monogatari.App{c.app()}}
	s := parse(t, `{"name":"count independently","state":true,
	 "setup":["b"], "expect":{"contains":["count 1 state new"]},
	 "notes":[{"after":0,"note":"Begin at zero"}],
	 "actions":[
	 {"label":"Two more","caption":"b b counts twice","keys":[{"key":"b","note":"First bump"},"b"],"expect":{"contains":["count 3 state new"]}},
	 {"label":"One more","keys":["b"],"expect":[{"after":1,"contains":["count 2 state new"]},{"contains":["count 2 state new"]}]}
	 ]}`)
	ok(t, lib.Run(s, monogatari.RunOptions{Golden: true}))
	if c.builds.Load() != 2 {
		t.Fatalf("built %d sessions, want 2", c.builds.Load())
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	copy := parse(t, string(data))
	if copy.Actions[0].Keys[0].Note != "First bump" || len(copy.Notes) != 1 || len(copy.Fields) != 0 {
		t.Fatalf("lost native notes: %s", data)
	}
}

func TestTutorialEmptySetupAndOptionalExpects(t *testing.T) {
	c := &counter{}
	lib := monogatari.Library{Apps: []monogatari.App{c.app()}}
	ok(t, run(t, lib, `{"name":"no setup","expect":{"contains":["count 0"]},"actions":[{"label":"bump","keys":["b"]}]}`))
	res := run(t, lib, `{"name":"bad","actions":[{"label":"bump","keys":["b"],"expect":{"contains":["count 9"]}},{"label":"also bad","keys":["b"],"expect":{"contains":["count 8"]}}]}`)
	if !strings.Contains(strings.Join(res.Failed, "\n"), "action 1 (bump)") || !strings.Contains(strings.Join(res.Failed, "\n"), "action 2 (also bad)") {
		t.Fatalf("failures: %+v", res)
	}
}

func TestTutorialFormatRejectsAmbiguousScripts(t *testing.T) {
	for _, data := range []string{
		`{"name":"x","setup":["b"]}`,
		`{"name":"x","script":["b"],"actions":[{"label":"x","keys":["b"]}]}`,
		`{"name":"x","actions":[{"keys":["b"]}]}`,
		`{"name":"x","actions":[{"label":"x","keys":[]}]}`,
	} {
		if _, err := monogatari.Parse([]byte(data), "bad.json"); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}
