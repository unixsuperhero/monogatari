package host

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	story "ofam.dev/monogatari"
	"ofam.dev/monogatari/explore"
)

// dedupe reads every explore report under in (PROG explore --out's
// NAME.json, explore.Survey's OCHAZUKE_EXPLORE_OUT NAME.json), groups their
// findings (explore.Dedupe: by check and normalized summary; per group the
// shortest path, then the smallest story application, then the name) and
// prints one line per group. A story application weighs its story's size;
// a subject that is no story (an example's survey) weighs most, so among
// equal paths a story wins. With out it writes out/groups.json and, per
// group, the gap story of its best occurrence on a story application
// (out/NN-CHECK-SUMMARY.json), when one has it. With reproduce each line
// ends in whether that occurrence's path still shows the finding on this
// build (explore.Reproduce): "reproduces", "gone", or "no story" for a
// group seen only on subjects that are no story.
func (t *Tool) dedupe(lib story.Library, in, out string, reproduce bool) error {
	reports, err := readReports(in)
	if err != nil {
		return err
	}
	apps := map[string]*story.Story{}
	for _, s := range lib.Stories {
		if s.Base == "" {
			apps[lib.Subject(s).Name] = s
		}
	}
	weight := func(subject string) int {
		if s := apps[subject]; s != nil {
			return len(appKey(s))
		}
		return math.MaxInt32
	}
	groups := explore.Dedupe(reports, weight)
	findings := 0
	for _, rep := range reports {
		findings += len(rep.Findings)
	}
	t.say("%d reports, %d findings, %d distinct", len(reports), findings, len(groups))
	for i, g := range groups {
		b := g.Best()
		status := ""
		if reproduce {
			status = "  [" + reproduces(lib, apps, g) + "]"
		}
		t.say("%3d  %s: %s (%d apps, ×%d; %s: %s)%s", i+1, g.Check, g.Summary, g.Apps, g.Count, b.Subject, strings.Join(b.Path, " "), status)
	}
	if out == "" {
		return nil
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(groups, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "groups.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	for i, g := range groups {
		for _, o := range g.Occurrences {
			app := apps[o.Subject]
			if app == nil {
				continue
			}
			st := FindingStory(app, &explore.Finding{Check: g.Check, Summary: g.Summary, Path: o.Path})
			st.Area = ""
			b, err := json.MarshalIndent(st, "", "  ")
			if err != nil {
				return err
			}
			name := fmt.Sprintf("%0*d-%s.json", max(2, len(fmt.Sprint(len(groups)))), i+1, trimSlug(story.Slug(g.Check+" "+g.Summary), 60))
			if err := os.WriteFile(filepath.Join(out, name), append(b, '\n'), 0o644); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// readReports is every explore report under dir, in path order: a JSON
// file whose subject is set and that has a findings field.
func readReports(dir string) ([]explore.Report, error) {
	var out []explore.Report
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".json" {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var probe map[string]json.RawMessage
		if json.Unmarshal(data, &probe) != nil || probe["findings"] == nil || probe["subject"] == nil {
			return nil
		}
		var rep explore.Report
		if err := json.Unmarshal(data, &rep); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, rep)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no explore report (NAME.json) under %s", dir)
	}
	return out, nil
}

func trimSlug(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimRight(s[:n], "-")
}

// reproduces replays the group's best occurrence on a story application
// and reports whether it still shows (explore.Shows).
func reproduces(lib story.Library, apps map[string]*story.Story, g explore.Group) string {
	for _, o := range g.Occurrences {
		app := apps[o.Subject]
		if app == nil {
			continue
		}
		shows, err := explore.Shows(lib.Subject(app), g.Check, g.Summary, o.Path)
		switch {
		case err != nil:
			return "replay failed: " + err.Error()
		case shows:
			return "reproduces"
		}
		return "gone"
	}
	return "no story"
}
