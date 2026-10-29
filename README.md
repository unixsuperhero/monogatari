# monogatari

Stories for apps built on [ochazuke](https://github.com/unixsuperhero/ochazuke):
a story is a JSON file that names an app, a fixture and a script of keys,
and says what the screen must show afterwards. Each app mounts monogatari's
host to build its own story tester, so a UI bug becomes a story before it is
fixed and a reviewer can walk an app change headless or live.

Module `github.com/unixsuperhero/monogatari`; requires Go 1.26 or newer. It
requires ochazuke and oden; neither requires it. ochazuke's `o-story` is one
more host, the host of ochazuke's demo app.

Status: the repository is new (lane L0 of `~/hub/o-plans/plans/o-story-for-apps.md`).
The engine, the story format reference and "Stories for your app" arrive with
lane L1, moved from ochazuke's `story` and `explore` packages and `cmd/o-story`.

## Install

```sh
go get github.com/unixsuperhero/monogatari@latest
```

## In an app: a tagged tester build

An app links monogatari only in its tester build, behind the build tag
`monogatari`; a normal build of the app links none of it.

```sh
go build -tags monogatari -o bin/APP-story ./cmd/APP   # the tester
go test -race -tags monogatari ./...                   # every test, stories included
```

The rules for the tag (tagged files, the `!monogatari` stub, the gate that
runs both builds, the symbol check) are in [AGENTS.md](AGENTS.md).

## In the family

| Repo | Relation |
|---|---|
| [ochazuke](https://github.com/unixsuperhero/ochazuke) | monogatari requires it (`Model`, `Runtime`, `cli`, `markdown`, `keytest`); its `cmd/o-story` and `examples` nested modules are monogatari hosts |
| [oden](https://github.com/unixsuperhero/oden) | the host's command is an oden command an app mounts |
| [matomeru](https://github.com/unixsuperhero/matomeru) (`mtm`), o-json | apps whose tester builds mount the host (`mtm story ...`) |

Release order: tag ochazuke, then monogatari, then bump ochazuke's nested
modules (see AGENTS.md, "Releasing").

## Checks

```sh
gofmt -l . && GOWORK=off go vet ./... && GOWORK=off go test ./...
```
