package host

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	story "ofam.dev/monogatari"
	"ofam.dev/oden"
)

// The README's generated section sits between these markers: the begin
// marker is a comment starting with verbsBegin (its rest names the
// command that writes it, which splice keeps).
const (
	verbsBegin = "<!-- verbs:begin"
	verbsEnd   = "<!-- verbs:end -->"
)

// coreMinimum plays the core stories (skipped ones excluded) and derives
// the verbs and mode edges their scripts exercise.
func coreMinimum(apps []story.App, core []*story.Story) (story.Minimum, error) {
	lib := story.Library{Stories: core, Apps: apps}
	var traces [][]story.Step
	for _, s := range core {
		if s.Skip != "" {
			continue
		}
		res := lib.Run(s, story.RunOptions{})
		if res.Err != nil {
			return story.Minimum{}, fmt.Errorf("%s: %w", s.Name, res.Err)
		}
		traces = append(traces, res.Trace)
	}
	m := story.Derive(traces...)
	if trapped := m.Trapped(); len(trapped) > 0 {
		return m, fmt.Errorf("modes entered with no exit back to normal in any story: %s", strings.Join(trapped, ", "))
	}
	return m, nil
}

// splice replaces the generated section of a README.
func splice(readme, table string) (string, error) {
	head, rest, ok := strings.Cut(readme, verbsBegin)
	note, rest, ok1 := strings.Cut(rest, "-->")
	_, tail, ok2 := strings.Cut(rest, verbsEnd)
	if !ok || !ok1 || !ok2 {
		return "", errors.New("README lacks the verbs markers")
	}
	return head + verbsBegin + note + "-->\n\n" + table + "\n" + verbsEnd + tail, nil
}

// Verbs prints the minimum (or rewrites the README section with --readme).
func (t *Tool) Verbs(args []string) error {
	flags := flag.NewFlagSet(t.Prog+" verbs", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	dir := flags.String("dir", "", "read the core stories from this directory instead of the host's own")
	readme := flags.String("readme", "", "rewrite the generated section of this README")
	if _, err := parse(flags, args); err != nil {
		return err
	}
	core, err := t.cfg.core(*dir)
	if err != nil {
		return err
	}
	m, err := coreMinimum(t.cfg.Apps, core)
	if err != nil {
		return err
	}
	if *readme == "" {
		fmt.Fprint(t.Stdout, m.Markdown())
		return nil
	}
	data, err := os.ReadFile(*readme)
	if err != nil {
		return err
	}
	out, err := splice(string(data), m.Markdown())
	if err != nil {
		return &oden.ExitError{Code: 1, Err: err}
	}
	return os.WriteFile(*readme, []byte(out), 0o644)
}
