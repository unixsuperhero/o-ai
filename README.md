# o-ai

The AI hub's data tool: the local AI harnesses (omp, Claude Code, Codex, Pi, OpenCode, Gemini, `clawed`) and their providers, models, thinking levels, skills, agents and settings, inspected without making a model request. It was `aiview` (hub/ai/utils/aiview, whose history this repository carries); it is now a family tool, so `o ai ...` and `mtm ai ...` run it, and nabe mounts it as a page.

Every verb prints JSON. There is no TUI in this repository: nabe is the TUI (`o tui`, below).

## Install

```sh
go build -o ~/bin/o-ai .
o-ai help            # the commands
o ai skill list      # through the o root
mtm ai skill list    # through mtm (its second lookup prefix is o)
```

Until ochazuke and oden are tagged, `go.mod` replaces them with the local checkouts in `~/proj`.

## Commands

The nouns are an ochazuke runtime behind `ochazuke/cli`: `o-ai NOUN VERB [ENTRY] [--ARG VALUE] [--target ROW_ID]`.

| Noun | Verbs | A row's data (the record aiview printed) |
| --- | --- | --- |
| `harness` | list, show | `Harness` |
| `provider` | list, show | `Provider` |
| `model` | list, show, select | `Model` (`cliSlug`, `args`, `thinking`) |
| `thinking` | list, show | the level with `model`, `cliSlug`, `launchArgs` |
| `skill` | list, show | `Resource` (path, resolvedPath, frontmatter; content in show) |
| `agent` | list, show | `Resource` |
| `settings` | list, show | `Setting` |
| `status` | list, show, state | list: counts per kind; show: the whole inventory; state: every list at once (below) |

```sh
o ai harness list
o ai model list --harness omp
o ai model show openai-codex/gpt-6-sol --harness omp
o ai thinking list --model openai-codex/gpt-6-sol --harness omp
o ai model select opus --harness claude
o ai model show                       # the saved selection
o ai skill list --search review
o ai skill show --target skill/skill:claude:/Users/me/.claude/skills/review/SKILL.md
o ai skills                           # a plural is NOUN list
o ai status show                      # the full inventory, warnings included
o ai status state                     # every noun's rows from one collection (the catalog's state)
o ai                                  # status list
o-ai catalog                          # the nouns and verbs, for a host
```

A list is rows:

```js
row = {
  id: "model/omp:openai-codex/gpt-6-sol",   // NOUN/ inventory id; pass it as --target
  label: "GPT-6 Sol [omp]",                 // name, then the harness(es)
  data: { id: "omp:openai-codex/gpt-6-sol", harness: "omp", cliSlug: "openai-codex/gpt-6-sol",
          args: ["omp", "--model", "openai-codex/gpt-6-sol"], thinking: [/* ... */] /* ... */ },
}
```

and `show` prints the whole record alone, unchanged from aiview. A list row's `data` is that record without its bulky fields, so a list stays small: skill and agent drop `content`, model `metadata`, harness `settings` (the settings noun lists them), provider `models` (`specs` in `shell.go`).

Words: a noun is resolved by unique prefix (`mo` is model, `sk` is skill; `s` is ambiguous between settings, skill and status), a verb too (`mo li`, `mo se`; `mo s` is ambiguous between select and show). Ambiguity is an error, never a first match. A bare word after `show` or `select` is ENTRY (an id, name, slug or path; with several matches it is an error listing them, so narrow with `--harness`/`--provider` or use an id). After `list`, a bare word fills `--harness`.

### Parameters

| Parameter | Behavior |
| --- | --- |
| `--harness NAME` | Filter by exact harness ID |
| `--provider NAME` | Filter by exact provider slug |
| `--model NAME` | Filter model/thinking rows by model ID, slug, or display name |
| `--search TEXT` | Case-insensitive name, ID, and description search |
| `--project DIRECTORY` | Inspect a project other than the current directory |
| `--home DIRECTORY` | Use another home for file discovery and child processes |
| `--timeout 15s` | Per-metadata-command deadline |
| `--no-probe` | Skip native harness commands; inspect files and caches only |
| `--fresh` | Collect now, even if the inventory cache is young |
| `--ttl 5m` | How old a cached inventory may be (`0s` always collects) |
| `--json '{...}'` | The parameters as one JSON object (`--json '{"harness":"omp"}'`) |
| `--target ROW_ID` | The row a show or select acts on (an `id` from the list) |

Parameters come after NOUN VERB. Collection warnings go to stderr.

### The inventory cache

A collection is cached in `$XDG_CACHE_HOME/o-ai/inventory.json` (else `~/.cache/o-ai/inventory.json`), mode `0600`, so a host that lists every noun (nabe, at mount and refresh) collects once. An entry is keyed by what changes the inventory: `--home`, `--project`, `--timeout`, `--no-probe` and the harness environment (`PATH`, `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, ...); the filters apply after collection and share it. It serves a verb while younger than `--ttl` (5 min; a host asks for a newer one with `--fresh`) and when it holds what the noun needs: skill and settings read files only, agent adds the runtime agents (Claude initialization, OMP's exporter), provider, model and thinking add the native catalogs, harness and status everything. Process status is never cached; harness and status inspect processes on every call. The file is replaced atomically under `inventory.json.lock`, so concurrent callers wait for one collection; a missing or corrupt file means collecting, never an error.

A model's stable inventory ID includes its harness and provider, such as `omp:openai-codex/gpt-6-sol`. Its `cliSlug` is the actual harness argument. Native metadata is preserved in `metadata`, including context limits, pricing, modalities, capabilities, resolved aliases, and reasoning descriptions when supplied by the source. Model details include argument arrays rather than shell-interpolated command strings. Claude uses `--effort`, Codex uses `-c 'model_reasoning_effort="..."'`, and OpenCode uses `--variant`. Thinking entries include the complete `launchArgs` array. Non-chat OMP catalog entries have empty launch arrays.

`model select` saves only o-ai's selection, in `~/.config/aiview/selection.json` (aiview's file, kept so an existing selection survives the rename), mode `0600`. It does not change a harness's selected model.

### From aiview

| aiview | o-ai |
| --- | --- |
| `aiview model list --harness omp` | `o ai model list --harness omp` |
| `aiview list model`, `aiview show model ID` | `o ai model list`, `o ai model show ID` (the verb-first spelling is gone) |
| `aiview status`, `aiview status --json` | `o ai status list` (counts), `o ai status show` (the inventory) |
| `aiview NOUN list --json` (array of records) | `o ai NOUN list` (rows; each record is a row's `data`) |
| `--json` (JSON output) | gone: output is always JSON; `--json` now takes the parameters as an object |
| flags before the command words | flags after NOUN VERB |
| `aiview tui` | `o tui`, after `o-nabe extension register o-ai` |
| `aiview --help` | `o-ai help` |

## The TUI is nabe

```sh
MTM_SERVER=default o-nabe extension register o-ai   # registers as "ai"
o tui                                               # a page ai with one list per noun
```

Restart nabe after `o-nabe extension refresh o-ai`. A row's verbs run as `o-ai NOUN VERB --target ROW_ID --json '{}' --yes`.

`status state` is the catalog's state source (`"state": "status.state"`, the ochazuke/cli convention): one process, one collection covering every noun's need, processes inspected once, answering every list:

```js
state = {
  harness: [{ id: "harness/claude", label: "Claude Code" }, /* ... */],
  skill:   [{ id: "skill/skill:claude:/Users/me/.claude/skills/review/SKILL.md", label: "review [claude]" }, /* 1,800 more */],
  /* provider, model, thinking, agent, settings, status */
}
```

Rows carry only id and label (no `data`): a host's list needs nothing else, and `show --target ID` prints the record. nabe runs it once at startup, then on its refresh interval, after a verb of the extension and on `extension refresh` (with `--fresh`), instead of eight `NOUN list` processes.

## Next

The nouns still to come, as verbs on this runtime: `mcp` (servers from `~/.claude.json`, `.mcp.json`, `~/.codex/config.toml`, omp), `mcp-tool` (start a stdio server, `initialize` + `tools/list`, stop it; pending work), and richer `agent` verbs. A sources file (`~/.config/o-ai/sources.json`) for roots the collectors do not discover, such as `~/proj/ai-tools/catalog/skills`, would be data merged over the built-ins by name:

```js
sources = { skills: [{ name: "ai-tools", path: "~/proj/ai-tools/catalog/skills" }] }
```

## Data sources and limits

| Harness | Model metadata | Settings and resources |
| --- | --- | --- |
| OMP | `omp models --json --no-extensions --kind all` | User/project YAML, shared and harness resource paths, bundled agent exporter |
| Claude Code | Initialization control response with no user prompt | User/project/local JSONC, installed plugin manifest, runtime agents |
| Codex | Local `models_cache.json`, including its fetch timestamp | TOML, configured agent files, skills |
| Pi | Installed `ModelRegistry` and `getSupportedThinkingLevels` | User/project JSON, custom models, skills and agent files |
| OpenCode | `opencode models --verbose --pure` | JSON/JSONC, inline agents, skill and agent directories |
| Gemini | Configured model only, no inferred capability catalog | User/project settings and discovered resource files |
| `clawed` | Probed only if an executable with that name exists; expects Claude-compatible initialization | Its own configuration directory |

The tool distinguishes installed executables, configuration presence, running processes, catalog membership, configured credentials where reported, and disabled providers. Catalog membership is not proof of authentication, subscription entitlement, network health, or successful inference. Pi can report configured credentials without validating them; other adapters do not claim authentication success.

Process discovery matches executable paths and Node/Bun/Deno script entrypoints. Counts include background helpers, not just interactive sessions. Unrecognized wrappers can remain unobserved. Only matching PIDs are retained, never process arguments.

Codex's model cache can be stale and is labeled with its fetch time. Custom Codex providers are not assigned the OpenAI cache's capabilities. OMP and OpenCode external extensions are excluded from metadata commands; Pi's catalog comes from its installed SDK rather than session extensions. Pi requires a Node-based installation whose SDK is available beside its executable. Adapter failures remain visible instead of fabricating model capabilities.

Resource entries include their source path, resolved symlink target, frontmatter, and content. Shared paths identify associated harnesses. Discovery does not prove that a particular running session loaded the resource. Plugin disablement and malformed frontmatter are labeled explicitly. Invalid resources remain inspectable. Hidden resource directories are skipped. Runtime agent records can coexist with file-backed definitions because the native response does not always provide a source path.

Settings retain file provenance and scope rather than claiming to reproduce every harness's effective session configuration. Explicit command-line overlays, managed enterprise policy, remote profiles, and live session overrides may differ. An OMP profile requires the corresponding `PI_CODING_AGENT_DIR` for file inspection; otherwise a warning explains the discrepancy. Malformed settings remain listed with an error, without dumping their unparsed contents.

## Safety

o-ai does not edit harness settings or launch an inference request. Native metadata commands can update their own caches and load trusted harness configuration. OMP bundled agents are exported into an owned temporary directory, read, then removed. Use `--no-probe` when only file inspection is wanted.

Settings and metadata redact secret-bearing fields, credential/header/environment containers, command hooks, recognized token formats, and URL credentials/query strings. Resource bodies remain inspectable with recognized token formats redacted. This is not a guarantee that arbitrary prose cannot contain a secret; do not publish a full inventory without reviewing it.

## Development

```sh
go test -race ./...
go vet ./...
go build .
```

The tests cover command ambiguity, the catalog (schema 1, `rows` on every list), row ids and `--target`, model selection, reasoning arguments, credential redaction, JSONC/TOML parsing, invalid configuration visibility, symlink cycles, non-chat launch handling, and script-based process identity. Real native commands must also be exercised after adapter changes (`o-ai status show`), and the nabe page after catalog changes (`o-nabe extension refresh o-ai && o-tui --smoke`).
