package monogatari

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Recording is one live session in a story: the initial story and the
// ordered events (keys, job deliveries, timer ticks) with their times.
// Replaying the events against the initial story reproduces every frame.
type Recording struct {
	Story   string    `json:"story"`
	Started time.Time `json:"started"`
	Initial *Story    `json:"initial"`
	Events  []Event   `json:"events"`
	Expect  Expects   `json:"expect,omitempty"`
}

// Recorder collects a session's events as they happen.
type Recorder struct {
	Rec   Recording
	clock func() time.Time
}

// NewRecorder starts a recording of s now.
func NewRecorder(s *Story, now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	return &Recorder{Rec: Recording{Story: s.Name, Started: now(), Initial: s}, clock: now}
}

// Add appends an event stamped with the time since the start.
func (r *Recorder) Add(e Event) {
	e.T = max(1, r.clock().Sub(r.Rec.Started).Milliseconds())
	r.Rec.Events = append(r.Rec.Events, e)
}

// HistorySize is how many unnamed recordings the ring keeps.
const HistorySize = 5

// Store is a host's data directory: history/ is the ring of the last
// HistorySize recordings, saved/ the named ones, which the ring never drops.
type Store struct{ Dir string }

// DefaultStore is the store of the host named host (its slug: o-story,
// mtm-story): $XDG_DATA_HOME/HOST, else ~/.local/share/HOST. Each host has
// its own ring and saved recordings.
func DefaultStore(getenv func(string) string, host string) Store {
	base := getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return Store{Dir: filepath.Join(base, host)}
}

func (s Store) history() string { return filepath.Join(s.Dir, "history") }
func (s Store) saved() string   { return filepath.Join(s.Dir, "saved") }

// Entry is one recording on disk; N is its place in the ring, 1 newest
// (0 for a saved one).
type Entry struct {
	N    int
	Name string
	Path string
	Rec  *Recording
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Push writes a recording into the ring and drops the oldest past
// HistorySize. It returns the file written.
func (s Store) Push(rec *Recording) (string, error) {
	name := rec.Started.UTC().Format("20060102-150405.000000000") + "-" + Slug(rec.Story) + ".json"
	path := filepath.Join(s.history(), name)
	if err := writeJSON(path, rec); err != nil {
		return "", err
	}
	names, err := s.historyNames()
	if err != nil {
		return path, err
	}
	for len(names) > HistorySize {
		if err := os.Remove(filepath.Join(s.history(), names[len(names)-1])); err != nil {
			return path, err
		}
		names = names[:len(names)-1]
	}
	return path, nil
}

// historyNames lists the ring's files newest first.
func (s Store) historyNames() ([]string, error) {
	entries, err := os.ReadDir(s.history())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	return names, nil
}

func readRecording(path string) (*Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if rec.Initial == nil {
		return nil, fmt.Errorf("%s: no initial story", path)
	}
	return &rec, nil
}

// History lists the ring, newest first (N = 1).
func (s Store) History() ([]Entry, error) {
	names, err := s.historyNames()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for i, name := range names {
		path := filepath.Join(s.history(), name)
		rec, err := readRecording(path)
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{N: i + 1, Name: strings.TrimSuffix(name, ".json"), Path: path, Rec: rec})
	}
	return out, nil
}

// Saved lists the named recordings.
func (s Store) Saved() ([]Entry, error) {
	entries, err := os.ReadDir(s.saved())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.saved(), e.Name())
		rec, err := readRecording(path)
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Name: strings.TrimSuffix(e.Name(), ".json"), Path: path, Rec: rec})
	}
	return out, nil
}

func validName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("bad recording name %q", name)
	}
	if _, err := strconv.Atoi(name); err == nil {
		return fmt.Errorf("recording name %q is a number; numbers name the history ring", name)
	}
	return nil
}

// Move promotes a recording file out of the ring into saved/<name>.json.
func (s Store) Move(path, name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	dest := filepath.Join(s.saved(), name+".json")
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("saved recording %q exists", name)
	}
	if err := os.MkdirAll(s.saved(), 0o755); err != nil {
		return "", err
	}
	return dest, os.Rename(path, dest)
}

// Keep promotes history entry n under name.
func (s Store) Keep(n int, name string) (string, error) {
	hist, err := s.History()
	if err != nil {
		return "", err
	}
	if n < 1 || n > len(hist) {
		return "", fmt.Errorf("no history entry %d (the ring holds %d)", n, len(hist))
	}
	return s.Move(hist[n-1].Path, name)
}

// Save writes rec as saved/<name>.json.
func (s Store) Save(rec *Recording, name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	dest := filepath.Join(s.saved(), name+".json")
	return dest, writeJSON(dest, rec)
}

// Load finds a recording by ring number (1 newest) or saved name.
func (s Store) Load(ref string) (*Entry, error) {
	if n, err := strconv.Atoi(ref); err == nil {
		hist, err := s.History()
		if err != nil {
			return nil, err
		}
		if n < 1 || n > len(hist) {
			return nil, fmt.Errorf("no history entry %d (the ring holds %d)", n, len(hist))
		}
		return &hist[n-1], nil
	}
	path := filepath.Join(s.saved(), ref+".json")
	rec, err := readRecording(path)
	if err != nil {
		return nil, fmt.Errorf("recording %q: %w", ref, err)
	}
	return &Entry{Name: ref, Path: path, Rec: rec}, nil
}

// Delete removes a recording by ring number (1 newest) or saved name,
// returning the file it removed. Deleting a ring entry renumbers the
// older ones.
func (s Store) Delete(ref string) (string, error) {
	e, err := s.Load(ref)
	if err != nil {
		return "", err
	}
	if err := os.Remove(e.Path); err != nil {
		return "", err
	}
	return e.Path, nil
}
