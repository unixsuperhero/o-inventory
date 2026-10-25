# o-inventory: agent guide

The family's derived inventory and changelog (`o-inventory`, so `o inv` and `o inventory`). An oden app with an ochazuke runtime behind `ochazuke/cli`. Design: `~/hub/o-plans/plans/inventory.md`. Every fact is read from a source that must already be right (a tool's `catalog`, nabe's registry, `o-proj list`, the hub's `o/` links, `git log`); nothing is hand-written. A wrong fact is fixed in its tool.

## Plans and todos

Family plans, todos and the roadmap live in `~/hub/o-plans/` (`README.md` is the index). Read it before planning work; add or update items there. Do not add a TODO.md or docs/plans/ to this repo.

## Where does X live

| Concern | File |
|---|---|
| `Tool`, `main`, the oden app, `mount`, paths (hub, registry, config) | `main.go` |
| Operations (snapshot, diff, changelog, check, hook, daily, repos) and their flag parsing | `commands.go` |
| Collection: PATH tools, `BIN catalog`, framework and mounted nouns, nabe hosts, projects, o-story state | `collect.go` |
| Row type, JSON Lines read/write | `rows.go` |
| Snapshot files, lock, hub commits | `snapshot.go` |
| Detection rules (`compared`), `Diff`, change rows | `diff.go` |
| Changelog: concepts, line grammar, folds, commits, merge into files | `changelog.go` |
| Repos config (`repos.json`, seeded) | `repos.go` |
| Drift check (stale-catalog, stale-build, unreachable, ...) | `check.go` |
| o's roster (reachable, shadow rows), lib and dep rows, go.mod parsing | `libs.go` |
| `overview`: the latest snapshot as one tree (text, `--json`) | `overview.go` |
| Post-commit hook; the daily launchd job (plist, wrapper, install/uninstall/status/run via launchctl) | `hook.go`, `daily.go` |
| The runtime: row nouns, list/show, `inventory state` | `shell.go` |

## Invariants

- Read-only toward every tool, registry and store; writes only `~/hub/inventory/`, `~/hub/CHANGELOG.md`, each repo's `CHANGELOG.md` (`--write-repos`, never committed), its hook block, `repos.json`, and the daily job's plist, wrapper and logs. Commits only in the hub.
- Tests never touch the real LaunchAgents or launchctl: a temp HOME and fake `launchctl`/`plutil` on a temp PATH.
- A snapshot is written only when a compared field differs from `latest.jsonl`; ignored fields (registeredAt, binMtime, descriptions) never make one.
- Changelog lines start with `added:`/`changed:`/`removed:`/`fixed:`/`shadow:` (a claim an `o` command newly shadows); a `changed:` line has `; was`; `fixed:` only from `fix:` commit subjects. `TestChangelogEntryGrammarAndRepoFiles` pins it.
- Framework nouns (no application verb, cli.Mount's rule) are recorded once under `ochazuke`.
- Every list verb declares `.Rows(noun)`; no key bindings here; never bind function keys.
- Every commit: `gofmt -l . && go vet ./... && go test -race ./...` green, `go run . catalog` answers, `o proj check`.
