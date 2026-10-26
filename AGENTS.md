# o-ai: agent guide

The AI hub's data tool (`o-ai`, so `o ai` and `mtm ai`). An oden app with an ochazuke runtime (`github.com/unixsuperhero/ochazuke`) behind `ochazuke/cli`: one noun per inventory kind (harness, provider, model, thinking, skill, agent, settings, mcp, mcp-tool) plus status, every verb printing JSON. It was aiview (hub/ai/utils/aiview; its history is in this repository's log). It has no TUI: nabe (`o tui`) mounts its catalog as the page `ai`.

## Plans and todos

Family plans, todos and the roadmap live in `~/hub/o-plans/` (`README.md` is the index). Read it before planning work; add or update items there. Do not add a TODO.md or docs/plans/ to this repo.

## Read in this order

1. This file, then `README.md` for the commands, the row shape and the aiview spellings.
2. `../ochazuke/AGENTS.md`, section "Building an application on ochazuke", and `../ochazuke/cli/{shell.go,mount.go,catalog.go}`.
3. `../nabe/README.md`: what a host reads (application nouns, a list verb with `rows`, rows `{id, label, data}`).
4. `~/hub/o-plans/plans/cli-extensions.md`, section 6 (`mtm-ai`): the plan the mcp and mcp-tool nouns came from.

## Where does X live

| Concern | File |
|---|---|
| `Tool`, `main`, the oden app, shared `ocli.Mount` groups and catalog, the explicit `mo` alias, plurals and the bare default, model selection file | `main.go` |
| The runtime: nouns and verbs, `specs` (each noun's lean-row fields and collection `Need`), parameters, rows, `Reveal`, `show`/`select`, option parsing, the per-process collection cache | `shell.go` |
| The inventory cache file (`~/.cache/o-ai/inventory.json`): key, TTL, lock, atomic write | `cache.go` |
| Inventory types, `Entries` (the projection every list reads), `categories` | `inventory.go` |
| Collection: harness probing, settings parsing and redaction, processes, models and providers | `collect.go`, `catalogs.go` |
| Skill and agent discovery, frontmatter, merging by path | `resources.go` |
| Skill sources (`~/.config/o-ai/sources.json`, the built-in ai-tools catalog), folding a source's skill into the harness rows that see the same file | `sources.go` |
| Tables: each noun's catalog columns and its rows' cells (`tables`, `cellsOf`), the context-size spellings | `table.go` |
| MCP servers from each harness's files (normalized, redacted), the tools omp cached in agent.db (`sqlite3 -readonly`, `mcp_tools:` rows only) | `mcp.go` |
| The quota noun: Claude and Codex logins, the usage endpoints, the Codex session-file fallback, quota rows | `quota.go` |
| Tests: collectors and redaction (`main_test.go`), catalog, rows and `--target` (`shell_test.go`), the cache (`cache_test.go`), quota against httptest servers (`quota_test.go`) | `*_test.go` |

## Invariants

- A list's rows are `{id: "NOUN/ENTRY_ID", label, data}`; `show` prints the inventory record in the shape aiview always printed, and `data` is that record minus the noun's `specs` omit fields (content, metadata, ...). A `status state` row's `data` is the record's `specs` brief keys only (a description cut to `briefText`): add a key there only when a host draws it, and measure the state (it was 2.35 MB for 8,262 rows). Change a record's JSON only with its consumers.
- Every list verb declares `.Rows(noun)`; `TestCatalogDeclaresRowsForEveryNoun` pins it.
- A table noun's columns live once, in `tables` (table.go): the runtime noun declares them (the catalog's `columns`) and every row of the noun, list, jsonl or state, carries one cell per column (`TestRowsCarryCellsPerColumn`). A column change is a cells change in the same entry.
- No TUI code and no key bindings here; a host renders. Never bind function keys.
- No verb-specific branches outside the runtime: a feature is a noun, its verbs and rows. The collectors know no command line.
- Read-only: nothing edits a harness's settings or sends an inference request, and no configured MCP server is started: tools come only from a harness's own cache. `model select` writes only `~/.config/aiview/selection.json`. `quota` reads the vendors' usage endpoints (a GET with the harness's login) and never refreshes or writes a login.
- `--no-probe` in every test that collects: tests never run the real harnesses. `TestMain` points `XDG_CACHE_HOME` at a temporary directory: tests never touch the user's cache.
- A noun's collection cost is data (`specs[noun].need`), never a switch on its name; processes are never cached. A `live` noun (quota) is fetched per call, outside the inventory and the state.
- Tests never reach the network or the user's keychain: `TestMain` replaces quota's transport and keychain reader, and quota tests inject httptest endpoints (`Tool.quota`).

## Every commit

`gofmt -l . && go vet ./... && go test -race ./...` green; `go run . catalog` prints schema 1. Commit on main, conventional messages, never amend. ochazuke (`v0.6.0`) and oden (`v0.1.1`) are required at their tags; an untracked `go.work` (`go work init . && go work use ../oden ../ochazuke`) builds against the sibling checkouts, and `GOWORK=off` builds from the tags as a fresh clone does.
