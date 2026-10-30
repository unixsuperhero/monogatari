package monogatari

import (
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/monogatari/explore"
	"github.com/unixsuperhero/ochazuke"
)

// Subject explores a story's application: every replay builds the story
// afresh through its app's fixture (its fakes included), drives the app's
// own program when the fixture has one, and closes it after. The app's
// Expected refusals are not findings.
func (lib Library) Subject(s *Story) explore.Subject {
	var mu sync.Mutex
	sessions := map[*ochazuke.Model]*Session{}
	session := func(m *ochazuke.Model) *Session {
		mu.Lock()
		defer mu.Unlock()
		return sessions[m]
	}
	var expected func(op, err string) bool
	if app, _, err := Lookup(lib.Apps, s); err == nil {
		expected = app.Expected
	}
	w, h := s.Size()
	return explore.Subject{
		Name: s.Area + "/" + strings.TrimSuffix(filepath.Base(s.Path), ".json"),
		Size: [2]int{w, h},
		Expected: func(op, err string) bool {
			return explore.FrameworkRefusal(op, err) || expected != nil && expected(op, err)
		},
		New: func() (*ochazuke.Model, error) {
			ss, err := lib.Build(s, Options{Mono: true})
			if err != nil {
				return nil, err
			}
			mu.Lock()
			sessions[ss.M] = ss
			mu.Unlock()
			return ss.M, nil
		},
		Program: func(m *ochazuke.Model) tea.Model {
			if ss := session(m); ss != nil && ss.Program != nil {
				return ss.Program
			}
			return m
		},
		Close: func(m *ochazuke.Model) {
			mu.Lock()
			ss := sessions[m]
			delete(sessions, m)
			mu.Unlock()
			if ss != nil {
				ss.Close()
			}
		},
	}
}
