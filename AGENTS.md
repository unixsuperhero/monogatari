# monogatari: agent guide

The story library for apps built on ochazuke. A story is a JSON file that
names an app, a fixture and a script of keys, and says what the screen must
show afterwards. monogatari runs stories headless (`go test`), opens them
live, records sessions and explores them; each app mounts its host to build
its own story tester (`mtm story ...`, an o-json tester). ochazuke's
`o-story` is one more host, the host of ochazuke's demo app.

Module `github.com/unixsuperhero/monogatari`. It requires ochazuke (for
`Model`, `Runtime`, `cli`, `markdown`, `keytest`) and oden (for the host's
command); neither requires monogatari. README.md holds the contract (story
format, "Stories for your app", the release order); keep it in sync with the
code. The design is `~/hub/o-plans/plans/o-story-for-apps.md`.

Status: lanes L1 and L2 of that plan. The engine is here: the format, runner,
driver, recording and trimming in the root package (moved from ochazuke's
`story` at `465906e`; the demo's page builder stayed in ochazuke as
`cmd/o-story/demo`), the app seam (`app.go`: `App`, `Fixture`, `Env`,
`Built`, `Session`), and `explore` (moved from ochazuke's `explore`, with
`Subject.Program`), and the host (`host/`, moved from ochazuke's
`cmd/o-story` at `3a062c0` in lane L2: the commands, the catalog shell, the
index TUI, the player, explore's watch mode, `Config`/`Main`/`Command`/`Test`).
The host's tests run over the `rows` test app (`host/app_test.go`) and
`host/testdata/stories`; ochazuke's demo stories stay in ochazuke.

## Plans and todos

Family plans, todos and the roadmap live in `~/hub/o-plans/` (`README.md` is the index). Read it before planning work; add or update items there. Do not add a TODO.md or docs/plans/ to this repo.

## What monogatari owns, and what each app owns

| monogatari owns | each app (host) owns |
|---|---|
| the story format: size, chrome, theme, settings, `values`, `session`, `config`, `state`, `files`, `script`, `expect`, `skip`, `base`/`start`, and `app`/`fixture`/`seed` | its fixtures: named starting states that build its runtime from a seed |
| the runner (`Library`, `Run`, `Check`, `Resolve`, `Minimum`), the driver (events, the clock, jobs, ticks, reset, restart), recording (`Classify`), trimming | its fakes (the faked daemon, sessions, timers), outside `_test.go`, behind the `monogatari` tag |
| recordings: the ring of 5 per story, named saves, `keep`, `replay`, `trim` | its stories, embedded in its tester build (for example `cmd/mtm/stories/`) |
| per-host state dirs: user stories at `$XDG_CONFIG_HOME/<host-slug>/stories`, recordings at `$XDG_DATA_HOME/<host-slug>` | its host name (`Prog`, e.g. `mtm story`) and so its state dir slug (`mtm-story`) |
| explore (`monogatari/explore`, with `Subject.Program`) | its explore refusals (`App.Expected`) |
| the host (`monogatari/host`): CLI subcommands, the catalog shell, the index TUI and the player, as an oden command an app mounts, plus `Test` for `go test` | the mount in a `//go:build monogatari` file and its safety guard (temp database, dead socket) |

monogatari knows no app. No app, fixture, page or noun name appears in the
library; an app's behaviour comes in through the `App`/`Fixture` seam as
data. Never branch on one app's name.

## The `monogatari` build tag (rule for apps)

An app links monogatari only in its tester build, selected by the build tag
`monogatari` (not `story`: build tags are global to a build, and the
library's own name is unique in the family).

- The app's story mount, its fixtures and the fakes it shares with
  `_test.go` live in files marked `//go:build monogatari`; a
  `//go:build !monogatari` file registers nothing, so a normal build keeps
  today's behaviour.
- monogatari stays a requirement in the app's `go.mod`, but a normal build
  compiles and links none of it. Check: `go tool nm <normal binary> | grep -c monogatari` is 0.
- The tester is a separate binary (`go build -tags monogatari -o bin/<app>-story ./cmd/<app>`, `make story-build`), never installed over the app.
- The tester never touches the owner's data: every session gets a temp
  database and a socket nothing listens on, and `story` takes no `--data`,
  `--socket` or `--server`.
- The app's gate runs both builds, because `go vet` and `gofmt` see a tagged
  file only in the tagged run:
  `go build ./... && go vet ./... && go test -race ./...` and
  `go build -tags monogatari ./... && go vet -tags monogatari ./... && go test -race -tags monogatari ./...`.
- `gopls` needs `-tags monogatari` in its build flags to edit the tagged files; the app's `AGENTS.md` says so.

monogatari itself is not tag-gated: the whole module is test tooling, and
nothing here carries a `//go:build monogatari` line.

## Invariants

- One-way graph: ochazuke's root module never imports monogatari, not even
  in a test. ochazuke's own users (`cmd/o-story`, `examples`) are nested
  modules that require both.
- Never bind function keys (F1 to F12) anywhere: stories, fixtures, demos and tests included.
- Keys are named as bindings name them (`ctrl+c`, `esc`, `shift+tab`) and
  sent through `github.com/unixsuperhero/ochazuke/keytest`; never spell out a
  Bubble Tea key message here.
- Tests never touch the developer's data or config: temp `HOME`,
  `XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME`.

## Checks

```sh
gofmt -l . && GOWORK=off go vet ./... && GOWORK=off go test ./...
GOWORK=off go test -race ./...   # before a release
```

Build and test with `GOFLAGS=-trimpath` so worktrees share the build cache.

## Releasing

Semver tags on `main`; consumers (ochazuke's `cmd/o-story` and `examples`
nested modules, then the apps' testers) require the tag. monogatari requires
a tagged ochazuke: release ochazuke first when monogatari needs an unreleased
ochazuke change.

The order for a change that spans both (a new framework API or story format
field together with its stories):

1. Tag ochazuke `vX` (its own "Releasing" steps).
2. Bump monogatari to ochazuke `vX` and tag monogatari (only if the format or
   the runner changed), with the steps below.
3. Bump ochazuke's nested modules (`cmd/o-story`, `examples`) to both tags
   and pass their `GOWORK=off` run; then the apps' testers.

Steps for a monogatari tag:

1. Pick the version (`v0.x.y`: minor for API changes, patch for fixes).
2. Add a `## vX.Y.Z (DATE)` section at the top of CHANGELOG.md (above o-inventory's dated entries) with the highlights.
3. Run the checks with `GOWORK=off` and `go mod tidy` (no diff); commit and push.
4. `git tag -a vX.Y.Z -m "..."` on that exact commit, `git push origin vX.Y.Z`, `gh release create vX.Y.Z --notes ...`. Never move or delete a pushed tag; fix forward with the next patch.
5. Check the proxy: `GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod go list -m github.com/unixsuperhero/monogatari@vX.Y.Z`.
6. Bump consumers: `go get github.com/unixsuperhero/monogatari@vX.Y.Z && go mod tidy`, gate with `GOWORK=off` (and `-tags monogatari` in apps), commit and push.

Local development uses an untracked `go.work` (`go work init . && go work use ../ochazuke ../oden`), ignored via `.gitignore`, so builds use the sibling checkouts while `GOWORK=off` uses the tags. Never commit a `replace` to a local path or a shared `~/proj/go.work`.
