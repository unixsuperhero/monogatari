package host

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	story "ofam.dev/monogatari"
)

// Export writes one deterministic JSON file per selected story and index.json.
// All stories are checked before writing; invalid selections never publish a
// partial replay. Filenames retain the story area to avoid cross-area clashes.
func (t *Tool) Export(args []string) error {
	flags := flag.NewFlagSet(t.Prog+" export", flag.ContinueOnError)
	flags.SetOutput(t.Stderr)
	out := flags.String("out", "", "output directory (required)")
	format := flags.String("format", "json", "export format: json")
	dir := flags.String("dir", "", "read core stories from this directory")
	names, err := parse(flags, args)
	if err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("export requires --out DIR")
	}
	if *format != "json" {
		return fmt.Errorf("unsupported export format %q", *format)
	}
	lib, err := t.library(*dir)
	if err != nil {
		return err
	}
	selected := map[*story.Story]bool{}
	if len(names) == 0 {
		for _, s := range lib.Stories {
			selected[s] = true
		}
	}
	for _, n := range names {
		hit := false
		for _, s := range lib.Stories {
			if s.Area == n {
				selected[s] = true
				hit = true
			}
		}
		if s := story.Find(lib.Stories, n); s != nil {
			selected[s] = true
			hit = true
		}
		if !hit {
			return fmt.Errorf("no story or area %q", n)
		}
	}
	type entry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Area string `json:"area"`
		File string `json:"file"`
	}
	index := struct {
		Version int     `json:"version"`
		Stories []entry `json:"stories"`
	}{Version: 1, Stories: []entry{}}
	files := map[string][]byte{}
	for _, s := range lib.Stories {
		if !selected[s] {
			continue
		}
		ex, err := lib.Export(s)
		if err != nil {
			return err
		}
		area := story.Slug(ex.Area)
		if area == "" {
			area = "stories"
		}
		name := area + "/" + story.Slug(ex.ID) + ".json"
		if _, exists := files[name]; exists {
			return fmt.Errorf("duplicate export path %s", name)
		}
		data, err := exportJSON(ex)
		if err != nil {
			return err
		}
		files[name] = data
		index.Stories = append(index.Stories, entry{ex.ID, ex.Name, ex.Area, name})
	}
	data, err := exportJSON(index)
	if err != nil {
		return err
	}
	files["index.json"] = data
	// Write index last: it is the inventory a consumer uses, including when a
	// selection is refreshed in an existing output directory.
	for _, e := range index.Stories {
		if err := writeExport(*out, e.File, files[e.File]); err != nil {
			return err
		}
	}
	return writeExport(*out, "index.json", files["index.json"])
}

func exportJSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), err
}
func writeExport(dir, name string, data []byte) error {
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
