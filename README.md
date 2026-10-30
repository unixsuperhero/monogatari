<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="monogatari" src="assets/logo-light.svg" width="411">
  </picture>
  <br>
  <a href="https://github.com/unixsuperhero/monogatari/releases"><img alt="Latest release" src="https://img.shields.io/github/v/release/unixsuperhero/monogatari"></a>
  <a href="https://pkg.go.dev/github.com/unixsuperhero/monogatari"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/unixsuperhero/monogatari.svg"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/unixsuperhero/monogatari"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/unixsuperhero/monogatari"></a>
</p>

<p align="center">Stories for apps built on <a href="https://github.com/unixsuperhero/ochazuke">ochazuke</a>: JSON keystroke scripts played headless against your app's real UI, with expectations on what the screen shows.</p>

<!-- Demo gif goes here once there is one (recorded with vhs, like oden's and ochazuke's):
<p align="center"><img alt="..." src="assets/demo.gif" width="720"></p>
-->

A story is a JSON file that names an app, a fixture and a script of keys,
and says what the screen must show afterwards. monogatari plays it against
the app's real `ochazuke.Model`, headless under `go test` or live in a
terminal, so a UI bug becomes a story before it is fixed and a reviewer can
walk an app change key by key without touching anyone's data.

Module `github.com/unixsuperhero/monogatari`; requires Go 1.26 or newer. It
requires [ochazuke](https://github.com/unixsuperhero/ochazuke) and
[oden](https://github.com/unixsuperhero/oden); neither requires it.

> **Status: pre-release.** Today the repository holds the module, its rules
> ([AGENTS.md](AGENTS.md)) and a smoke test that drives a model through
> `github.com/unixsuperhero/ochazuke/keytest`. The engine is ochazuke's
> `story` and `explore` packages and its `o-story` tool, which move here
> unchanged in the next lanes; everything marked *coming in v0.1.0* below
> works today in ochazuke's `o-story` and arrives here with that release.

## What it does

- **Stories.** JSON keystroke scripts with expectations: the focused list,
  the mode, text the screen contains or lacks, the highlighted rows, the
  page. Keys are named as bindings name them (`ctrl+n`, `esc`, `shift+tab`).
  *Coming in v0.1.0.*
- **Headless runs.** Every story runs under `go test` against the app's real
  UI, with a fake clock, jobs and ticks, so a run is the same every time.
  *Coming in v0.1.0.*
- **Recordings.** Every live session is recorded: a ring of the last 5 per
  story, plus named saves you `keep`. `replay` plays one back with its
  timing; `trim` cuts it down to the keys that matter; `run --from` turns
  one into a headless check. *Coming in v0.1.0.*
- **Explorer.** Walks an app's states from a story's start and reports what
  breaks (panics, focus traps, hints that lie, renders that overflow, jobs
  that never settle), each finding written out as a story that reproduces it. *Coming in v0.1.0.*
- **A host per app.** CLI subcommands, an index TUI and a player, as an
  [oden](https://github.com/unixsuperhero/oden) command an app mounts under
  its own name (`mtm story ...`). *Coming in v0.1.0.*

monogatari knows no app. An app's behaviour comes in through its fixtures
(named starting states that build its runtime from a seed) as data; the
library never branches on an app's name.

## A story

A story from ochazuke's own suite, in the format `o-story` reads today
(`page` builds a page for ochazuke's demo app). v0.1.0 adds `app`, `fixture`
and `seed`, so a story names which app to build and from which starting
state; a story with no `app` runs on the host's first app.

```json
{
  "name": "the palette highlights nothing until ctrl+n",
  "area": "palette",
  "page": { "lists": [{ "id": "a", "rows": 3 }] },
  "script": [":", "ctrl+n"],
  "expect": [
    { "after": 1, "highlighted": 0, "dimmed": 1, "mode": "insert", "lacks": ["> "] },
    { "highlighted": 0, "dimmed": 1, "contains": ["> accept"] }
  ]
}
```

An `expect` entry checks the screen after a step (`after`) or, without one,
at the end.

## Each app builds its own tester

An app links monogatari only in its tester build, behind the build tag
`monogatari`. Its story mount, its fixtures and its fakes live in files
marked `//go:build monogatari`; a `!monogatari` file registers nothing, so a
normal binary compiles and links none of it
(`go tool nm <binary> | grep -c monogatari` is 0).

```sh
go build -tags monogatari -o bin/mtm-story ./cmd/mtm   # the tester, never installed over the app
go test -race -tags monogatari ./...                   # every test, stories included
```

The tester never touches the owner's data: every session gets a temp
database and a socket nothing listens on. *Coming in v0.1.0* (the host), then
in each app:

```sh
mtm-story story run panes              # headless; non-zero exit on failure
mtm-story story open panes/detach-keeps-preview   # live, in the real chrome
mtm-story story history                # the ring: 1 = newest, and the saved names
mtm-story story keep 2 detach-bug      # promote ring entry 2 to a named save
mtm-story story replay detach-bug      # live, with the recorded timing
mtm-story story explore panes          # the state explorer
```

The rules for the tag (tagged files, the `!monogatari` stub, the gate that
runs both builds, the symbol check) are in [AGENTS.md](AGENTS.md).

## Install

```sh
go get github.com/unixsuperhero/monogatari@latest
```

Until v0.1.0 there is no tag and no engine to import; the module builds and
its smoke test runs.

## In the family

monogatari sits above ochazuke and beside the apps' test code. No module
below it requires it, and an app requires it only for its tester build:

| Repo | Relation |
|---|---|
| [ochazuke](https://github.com/unixsuperhero/ochazuke) | monogatari requires it (`Model`, `Runtime`, `cli`, `markdown`, `keytest`). Its `o-story` tool becomes one more host, the host of ochazuke's demo app, in a nested module; the root module never imports monogatari. |
| [oden](https://github.com/unixsuperhero/oden) | The host is an oden command an app mounts, so `mtm story` resolves like any other subcommand. |
| [matomeru](https://github.com/unixsuperhero/matomeru) (`mtm`) | Its `mtm-story` tester mounts the host with fixtures `seeded` and `empty` and its stories under `cmd/mtm/stories/`. Planned. |
| [o-json](https://github.com/unixsuperhero/o-json) | Runs its 14 stories through ochazuke's `story` package by hand today; moves to a tagged `o-json story` mount. Planned. |

Release order: tag ochazuke, then monogatari, then bump ochazuke's nested
modules and the apps' testers (see [AGENTS.md](AGENTS.md), "Releasing").

## Checks

```sh
gofmt -l . && GOWORK=off go vet ./... && GOWORK=off go test ./...
GOWORK=off go test -race ./...   # before a release
```

## Logo

The logo is the Shiori: an open book whose left page is the story script and
whose right page is the live terminal it plays against, with a red ribbon
bookmark for the recording that marks your place. `assets/logo-light.svg`,
`assets/logo-dark.svg` and `assets/icon.svg` are drawn by the family's logo
generator.

## License

MIT; see [LICENSE](LICENSE).
