# o-ai: agent guide

The AI hub's data tool (`o-ai`, so `o ai` and `mtm ai`). An oden app with an ochazuke runtime (`github.com/unixsuperhero/ochazuke`) behind `ochazuke/cli`: one noun per inventory kind (harness, provider, model, thinking, skill, agent, settings) plus status, every verb printing JSON. It was aiview (hub/ai/utils/aiview; its history is in this repository's log). It has no TUI: nabe (`o tui`) mounts its catalog as the page `ai`.

## Read in this order

1. This file, then `README.md` for the commands, the row shape and the aiview spellings.
2. `../ochazuke/AGENTS.md`, section "Building an application on ochazuke", and `../ochazuke/cli/{shell.go,mount.go,catalog.go}`.
3. `../nabe/README.md`: what a host reads (application nouns, a list verb with `rows`, rows `{id, label, data}`).
4. `../matomeru/docs/plans/cli-extensions.md`, section 6 (`mtm-ai`): the nouns still to come (mcp, mcp-tool).

## Where does X live

| Concern | File |
|---|---|
| `Tool`, `main`, the oden app, `mount` (this tool's nouns as commands, plus `catalog`), plurals and the bare default, model selection file | `main.go` |
| The runtime: nouns and verbs, parameters, rows, `Reveal`, `show`/`select`, option parsing, the per-process collection cache | `shell.go` |
| Inventory types, `Entries` (the projection every list reads), `categories` | `inventory.go` |
| Collection: harness probing, settings parsing and redaction, processes, models and providers | `collect.go`, `catalogs.go` |
| Skill and agent discovery, frontmatter, merging by path | `resources.go` |
| Tests: collectors and redaction (`main_test.go`), catalog, rows and `--target` (`shell_test.go`) | `*_test.go` |

## Invariants

- A list's rows are `{id: "NOUN/ENTRY_ID", label, data}`; `data` and `show` print the inventory record in the shape aiview always printed. Change a record's JSON only with its consumers.
- Every list verb declares `.Rows(noun)`; `TestCatalogDeclaresRowsForEveryNoun` pins it.
- No TUI code and no key bindings here; a host renders. Never bind function keys.
- No verb-specific branches outside the runtime: a feature is a noun, its verbs and rows. The collectors know no command line.
- Read-only: nothing edits a harness's settings or sends an inference request. `model select` writes only `~/.config/aiview/selection.json`.
- `--no-probe` in every test that collects: tests never run the real harnesses.

## Every commit

`gofmt -l . && go vet ./... && go test -race ./...` green; `go run . catalog` prints schema 1. Commit on main, conventional messages, never amend. ochazuke and oden are local `replace`s on `../ochazuke` and `../oden`; drop them once those are tagged.
