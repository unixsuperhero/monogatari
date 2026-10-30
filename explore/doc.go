// Package explore explores an ochazuke application's interaction states
// by pressing keys, checks properties in every state it reaches, and
// reports bad and impossible states and actions with the shortest key path
// to each. It is deterministic code, no agents: the same Subject and Budget
// give the same Report (Elapsed aside).
//
//	rep := explore.Run(explore.Subject{Name: "app", New: newFakedApp}, explore.Budget{})
//	fmt.Print(rep.Text(5), rep.Legend())
//
// From a test, explore.Survey(t, subject) runs a small budget (depth 3,
// 2000 steps; OCHAZUKE_EXPLORE=deep for depth 6, 20000) and fails only on
// the explorer's own failures, never on findings; such tests carry the
// explore build tag, so go test ./... runs no survey (make explore-quick,
// make explore). o-story explore runs it
// over every story-built application.
//
// # Driving
//
// A replay calls Subject.New, runs Init and a 80x24 WindowSizeMsg, then
// plays actions through Model.Update, rendering a frame after every
// message as Bubble Tea does (motions read the last frame's drawing
// order). Commands run inline: the parts of a batch concurrently, each
// until it finishes or parks (its goroutine blocked on a channel, select,
// condition or sleep on three polls in a row: a fake stream waiting for
// output, a fake job waiting for a key), at most 150ms; messages are
// delivered in issue order. Parked commands are waited on again only at
// Wait, never between keys, so a late answer cannot change what a key
// sees. Timers are never run: tea.Tick, tea.Every and the sequence timer
// are dropped, and Wait delivers the framework's own through their tick
// accessors (Runtime.BindingTimeoutTick, Model.JobLingerTick, the
// completer's, trees' and record editors' DebounceTick) with Model.Now on
// a fake clock that only Wait advances. Runtime.BindingTimeout is set to
// an hour, so a sequence ends by its next key or by Wait, never by the
// wall clock. An application's own timers are counted
// (Coverage.Timers) and not delivered.
//
// The actions tried in a state, in this order: the next key of every row
// that answers at the focus in the mode (Runtime.ListBindings; a sequence
// pressed key by key; a key class by a representative: digit 5, alpha a,
// upper A, ctrl+alpha ctrl+o, printable x, paste a bracketed paste of
// "pasted", any z), the hostile keys (Hostile: esc enter tab shift+tab
// backspace ctrl+c), Wait, and a resize to each of Subject.Sizes. Reaching
// app.quit is an end state "quit", not a failure.
//
// # The state key
//
// Two moments are the same state only when every key would behave the
// same (Abstract): the page; the focus path (each instance's id and noun,
// a list row by its noun and list, never its id); the mode name with the
// payload's parts (count, opcount, operator, shadow, stored request, the
// return of a pending mode) and a sequence prefix; the open modals in
// order, each with the noun and mode closing it restores; every
// registered guard evaluated at the focus; a hash of the effective rows at
// the focus in the mode (keys, request, arguments, sources, guard); for a
// focused list its position (empty, only, first, middle, last) with
// filter, marks and visual; for a focused input its cursor class (empty,
// start, middle, end); macro recording and an active register. Full adds a
// hash of the screen at 80x24, so states that look different are never
// merged; Budget.CompareFull reports both counts.
//
// The key ignores (IgnoresDoc): row ids and counts, scroll offsets, input
// text, register contents, notices and the status line, the jobs list,
// table column offsets and widths, settings values, detail scroll, the
// terminal size (unless Full), application state the runtime does not
// hold, and commands in flight. States it merges may differ there.
//
// # Search and checks
//
// Breadth first by replay from New (or Subject.Fork), deduplicated by the
// key, within Budget.Depth and Budget.Steps; every state keeps its
// shortest path. A state's actions play on parallel replays unless
// Subject.Serial. Checks, each a name findings group under:
//
//   - panic: Update, View, a command or a check panicked;
//   - focus-top-level, page-holds-focus, mode-registered, row-selected:
//     the focus is inside the top modal, Runtime.Page is an ancestor of
//     BaseFocus, the mode is defined, a focused list row is its list's
//     selected row;
//   - render-fits: at every size no line is wider than the width, the
//     frame has at most height lines, the cursor is on screen;
//   - honest-hints: every key the footer hints (HintBindings) answers when
//     pressed, is not refused by its verb guard and does not fail;
//   - no-trap: from every state a key path of at most Depth keys reaches
//     home (the initial page, normal mode, no modal): proven from the
//     explored graph, else searched from the state (Budget.TrapSteps);
//     "no way home" is proven, "no way home found" ran out of steps;
//   - jobs-settle: after a Wait no job runs with nothing in flight (jobs
//     whose command is parked are listed as long-running, not failures);
//   - operation-error: a key's operation failed, grouped by operation,
//     unless it is a guard refusal, markdown's ErrRefused or
//     Subject.Expected;
//   - commands-settle: one action delivered more than 256 messages;
//   - static-trap: a mode no row leaves (read from the tables, no search);
//   - and every Subject.Checks entry.
//
// Each finding keeps the first (shortest) path the search met it by and
// is shrunk by delta debugging (replaying subsets until the same check
// with the same summary no longer shows). Finding.GoTest writes a Go test
// around Reproduce.
package explore
