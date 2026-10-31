package explore

import (
	"maps"
	"slices"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
)

// Subject is an application to explore.
type Subject struct {
	Name string
	// New builds a fresh application with every outside effect faked
	// (clock, streams, daemons, files, processes). It is called once per
	// replay, so it must be cheap and return the same application every
	// time. The explorer sets Model.Now and Runtime.BindingTimeout.
	New func() (*ochazuke.Model, error)
	// Program, when set, is the application's own tea.Model over a model
	// New built, when it wraps the model's Update and View (an app that
	// runs its own work around the framework's); the explorer then runs
	// Init, Update and View on it. Unset, the model is driven.
	Program func(*ochazuke.Model) tea.Model
	// Close, when set, releases what one replay's application holds (fake
	// goroutines, temp dirs). Unset, every instance under app is removed,
	// which cancels its jobs.
	Close func(*ochazuke.Model)
	// Seeds are key paths to start from besides the initial state.
	Seeds [][]string
	// Checks are the application's invariants, run in every state beside
	// the framework's own.
	Checks []Check
	// Size is the terminal size a replay starts at; default 80x24.
	Size [2]int
	// Sizes are the render sizes checked in every state; default
	// DefaultSizes.
	Sizes [][2]int
	// Fork, when set, copies a model so a state is expanded without
	// replaying its path. Unset, every expansion replays from New.
	Fork func(*ochazuke.Model) (*ochazuke.Model, error)
	// Serial replays one at a time. Unset, a state's actions are played
	// on parallel replays, which needs New to build independent
	// applications (no package-level state shared between them).
	Serial bool
	// Expected, when set, reports an operation error the application means
	// (a refusal); it is not a finding. Guard refusals ("is not
	// applicable now") and markdown's ErrRefused never are.
	Expected func(operation, err string) bool
}

// Check is a named invariant over a model; an error is a finding.
type Check struct {
	Name string
	Fn   func(*ochazuke.Model) error
}

// DefaultSizes are the render sizes checked when Subject.Sizes is empty.
var DefaultSizes = [][2]int{{40, 12}, {80, 24}, {160, 40}}

// KeyMode chooses what makes two moments the same state.
type KeyMode int

const (
	// Abstract keys a state by its interaction state (see the package doc).
	Abstract KeyMode = iota
	// Full adds a hash of the screen at 80x24, so states that look
	// different are never merged.
	Full
)

func (k KeyMode) String() string {
	if k == Full {
		return "full"
	}
	return "abstract"
}

// Budget bounds a run.
type Budget struct {
	Depth int // the longest key path explored; default 6
	Steps int // actions played while searching (replays not counted); default 20000
	// Key is the state key; default Abstract.
	Key KeyMode
	// CompareFull runs the search a second time keyed Full and reports both
	// counts (Report.States), so over-merging is measurable.
	CompareFull bool
	// NoShrink keeps each finding's path as the search found it.
	NoShrink bool
	// ShrinkPerCheck is how many findings of each check are shrunk;
	// default 8.
	ShrinkPerCheck int
	// TrapSteps bounds the extra search from each state that the explored
	// graph does not prove can reach home; default 400.
	TrapSteps int
	// Observe, when set, is told about every state the search reaches, in
	// order (see Step): the initial state and each seed's, every action
	// played, and the end of the trap search. It runs on the goroutine
	// that called Run, between steps, so a slow observer slows the run
	// and changes nothing it finds. A CompareFull second search is not
	// observed. The report's Budget leaves it out.
	Observe func(Step) `json:"-"`
}

func (b Budget) withDefaults() Budget {
	if b.Depth <= 0 {
		b.Depth = 6
	}
	if b.Steps <= 0 {
		b.Steps = 20000
	}
	if b.ShrinkPerCheck <= 0 {
		b.ShrinkPerCheck = 8
	}
	if b.TrapSteps <= 0 {
		b.TrapSteps = 400
	}
	return b
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
