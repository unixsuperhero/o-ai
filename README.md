<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="o-ai" src="assets/logo-light.svg" width="120">
  </picture>
  <br>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/unixsuperhero/o-ai"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/unixsuperhero/o-ai"></a>
</p>

<p align="center">Your local AI setup as JSON: the harnesses, providers, models, skills, agents, settings, MCP servers and quotas on this machine, inspected without a single model request.</p>

<p align="center"><img alt="o-ai over an invented setup: status counts, the harnesses and their state, skills by unique prefix, one skill's record, the configured MCP servers, and the same tool as mtm ai" src="assets/demo.gif" width="720"></p>

The AI hub's data tool: the local AI harnesses (omp, Claude Code, Codex, Pi, OpenCode, Gemini, `clawed`) and their providers, models, thinking levels, skills, agents and settings, inspected without making a model request. It was `aiview` (hub/ai/utils/aiview, whose history this repository carries); it is now a family tool, so `o ai ...` and `mtm ai ...` run it, and nabe mounts it as a page.

Every verb prints JSON. There is no TUI in this repository: nabe is the TUI (`o tui`, below).

## Install

Module `github.com/unixsuperhero/o-ai`; requires Go 1.26 or newer.

```sh
go install github.com/unixsuperhero/o-ai@latest
```

or, from a checkout:

```sh
go build -o ~/bin/o-ai .
o-ai help            # the commands
o ai skill list      # through the o root
mtm ai skill list    # through mtm (its second lookup prefix is o)
```

`go.mod` requires ochazuke (`v0.16.0`) and oden (`v0.1.1`) at their tags, so a fresh clone builds as is. For work across the family, an untracked `go.work` (`go work init . && go work use ../oden ../ochazuke`) builds against the sibling checkouts; `GOWORK=off` builds from the tags.

## Usage

A first look, over the invented setup the demo uses (two Claude Code skills and an agent, Claude and Codex settings, an MCP server each; [docs/vhs](docs/vhs)). Every verb prints JSON; lists are rows of `{id, label, data, cells}`:

```console
$ o-ai status list
[
  {"id":"status/harnesses","label":"harnesses: 7","data":{"kind":"harnesses","count":7},"cells":["harnesses","7"]},
  {"id":"status/providers","label":"providers: 2","data":{"kind":"providers","count":2},"cells":["providers","2"]},
  {"id":"status/models","label":"models: 2","data":{"kind":"models","count":2},"cells":["models","2"]},
  ...
]
$ o-ai mcp list | jq -c '.[0]'
{"id":"mcp/claude:user:weather","label":"weather [claude]","data":{"id":"claude:user:weather","name":"weather","harness":"claude","scope":"user","config":"/private/tmp/oai.XXXX/home/.claude.json","transport":"http","url":"https://weather.example.com/mcp"},"cells":["weather","claude","user","http","https://weather.example.com/mcp"]}
$ o-ai sk li | jq -r '.[].label'          # sk li is skill list
release-notes [claude]
review [claude]
$ o-ai skill show review | jq '{name, description, harnesses, scope}'
{
  "name": "review",
  "description": "Review a diff for bugs, missing tests and unclear names.",
  "harnesses": [
    "claude"
  ],
  "scope": "user"
}
$ mtm ai model list | jq -r '.[].label'   # the same tool through mtm
opus [claude]
gpt-5-codex [codex]
```

### Commands

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
| `mcp` | list, show, tools | a configured MCP server: `harness`, `scope`, `config`, `transport`, `command`, `args`, `url`, env and header names (below) |
| `mcp-tool` | list, show | a tool a harness cached for a server: `server`, `name`, `description`; `inputSchema` in show |
| `status` | list, show, state | list: counts per kind; show: the whole inventory; state: every list at once (below) |
| `quota` | list, show | a rate-limit window: `usedPercent`, `remainingPercent`, `resetsAt` (below) |

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
o ai status state                     # every noun's rows from one collection, JSON Lines (the catalog's state)
o ai skill list --format jsonl        # one {noun, id, label, data} per line
o ai mcps                             # every configured MCP server of every harness
o ai mcp tools tuios                  # the tuios server's tools (mcp-tool list tuios)
o ai                                  # status list
o-ai catalog                          # the nouns and verbs, for a host
```

Help uses the same grouped command metadata as the runtime catalog, including the framework's nouns. A bare group (`o ai model` or `o ai provider`) lists its verbs without collecting inventory or contacting a provider; `--help` also works on groups and operations (`o ai model list --help`).

Words: a noun is resolved by unique prefix (`sk` is skill); `mo` is an explicit alias for model, even with the framework's mode and modal groups present. A verb accepts a unique prefix too (`mo li`, `mo se`; `mo s` is ambiguous between select and show). Ambiguity is an error, never a first match. A bare word after `show` or `select` is ENTRY (an id, name, slug or path; with several matches it is an error listing them, so narrow with `--harness`/`--provider` or use an id). After `list`, a bare word fills `--harness` (`--server` for `mcp list`, `mcp tools` and `mcp-tool list`).

### Parameters

| Parameter | Behavior |
| --- | --- |
| `--harness NAME` | Filter by exact harness ID |
| `--provider NAME` | Filter by exact provider slug |
| `--model NAME` | Filter model/thinking rows by model ID, slug, or display name |
| `--search TEXT` | Case-insensitive name, ID, and description search |
| `--project DIRECTORY` | Inspect a project other than the current directory |
| `--home DIRECTORY` | Use another home for file discovery and child processes |
| `--timeout 15s` | Per-metadata-command deadline; quota's usage request deadline |
| `--no-probe` | Skip native harness commands; inspect files and caches only |
| `--fresh` | Collect now, even if the inventory cache is young |
| `--ttl 5m` | How old a cached inventory may be (`0s` always collects) |
| `--json '{...}'` | The parameters as one JSON object (`--json '{"harness":"omp"}'`) |
| `--target ROW_ID` | The row a show or select acts on (an `id` from the list) |

Parameters come after NOUN VERB. Collection warnings go to stderr.

### Quota

`quota` is how much of the plan's rate-limit windows Claude Code and Codex have used and have left, fetched on every call (never cached, not in `status state`):

```sh
o ai quota list                # one row per window, claude then codex
o ai quota list codex          # one harness (--harness codex)
o ai quota show claude         # the whole record: plan, source, observedAt, windows, error
o ai quota show                # both records
```

```js
row = {
  id: "quota/claude:weekly_all",                // quota/HARNESS:WINDOW; a scoped window adds its model (quota/claude:weekly_scoped:fable)
  label: "claude weekly_all 47% used, 53% left, resets 2026-10-28 08:00",   // the reset in local time
  data: { harness: "claude", window: "weekly_all", usedPercent: 47, remainingPercent: 53,
          resetsAt: "2026-10-28T08:00:00Z", resetsIn: "63h7m0s", severity: "normal",
          source: "api", observedAt: "2026-10-25T16:52:06Z" },
}
```

Claude's windows are the usage answer's `limits` (`session`, `weekly_all`, `weekly_scoped (MODEL)`), else `five_hour` and `seven_day`. Codex's are `primary (5h)` and `secondary (7d)`, named by their length. `source` is `api` (the usage endpoint) or `session` (Codex's newest session file, observed at the file's modification time; a window that has reset since is `stale`, and its label says so). A harness that cannot answer (no login, an API-key Codex login, an expired Claude token, a network error) is one row `quota/HARNESS:error` whose label says why, and `show` prints it as `error`. `quota list` fails only when no harness answered. `--timeout` is the request's deadline (15s).

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

## Concepts

### Rows

A list is rows:

```js
row = {
  id: "model/omp:openai-codex/gpt-6-sol",   // NOUN/ inventory id; pass it as --target
  label: "GPT-6 Sol [omp]",                 // name, then the harness(es)
  data: { id: "omp:openai-codex/gpt-6-sol", harness: "omp", cliSlug: "openai-codex/gpt-6-sol",
          args: ["omp", "--model", "openai-codex/gpt-6-sol"], thinking: [/* ... */] /* ... */ },
}
```

and `show` prints the whole record alone, unchanged from aiview. A row of a table noun also carries `cells`, one per column its noun declares in the catalog (`columns`), so a host draws the list as a table:

| Noun | Columns (the first never drops; the rest drop when narrow, last listed first) |
| --- | --- |
| skill, agent | name, source (the harnesses, else the skill source), scope, description (one line, 160 characters) |
| model | model, harness, provider, context (`200k`, `1M`, from the catalog's metadata), thinking (the level slugs) |
| harness | harness, state (installed, configured, missing), procs, catalog, executable |
| provider | provider, harness, models (a count), access |
| mcp | server, harness, scope, transport, command (with its args, or the url) |
| mcp-tool | tool, server, description |
| quota | harness, window, used, left, resets (local time) |
| status | kind, count |

thinking and settings rows have no cells (a plain list of labels). The same cells are on `--format jsonl` lines and the state's rows. A list row's `data` is that record without its bulky fields, so a list stays small: skill and agent drop `content`, model `metadata`, harness `settings` (the settings noun lists them), provider `models` (`specs` in `shell.go`).

### The inventory cache

A collection is cached in `$XDG_CACHE_HOME/o-ai/inventory.json` (else `~/.cache/o-ai/inventory.json`), mode `0600`, so a host that lists every noun (nabe, at mount and refresh) collects once. An entry is keyed by what changes the inventory: `--home`, `--project`, `--timeout`, `--no-probe` and the harness environment (`PATH`, `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, ...); the filters apply after collection and share it. It serves a verb while younger than `--ttl` (5 min; a host asks for a newer one with `--fresh`) and when it holds what the noun needs: skill and settings read files only, agent adds the runtime agents (Claude initialization, OMP's exporter), provider, model and thinking add the native catalogs, harness and status everything. Process status is never cached; harness and status inspect processes on every call. The file is replaced atomically under `inventory.json.lock`, so concurrent callers wait for one collection; a missing or corrupt file means collecting, never an error.

Each collection also records its inputs in `inputs.json` beside it: every path the collectors read or looked for (settings and MCP files, `models_cache.json`, Claude's plugin manifest, omp's `agent.db`, every skill and agent file read and every directory walked to reach one, the harness executables and their link targets, the `PATH` directories, `sources.json` and the source roots, the project's ancestors), about 6,000 paths on the owner's machine, with their stat (`cli.StatFingerprint`: size, mtime, mode). An entry whose inputs no longer stat the same is collected again, so a settings edit or a new skill shows on the next call rather than after the TTL. A directory's mtime changes when an entry is added or removed, so a new skill deep in a tree changes the stat of its directory. Not covered: what a probe prints (the native catalogs and runtime agents come from running the harness; only its executable is stat'd, so a catalog that changes behind an unchanged executable shows after the TTL, as before), a file rewritten with the same size and mtime, and a file changed while the collection runs (stat'd after it).

A model's stable inventory ID includes its harness and provider, such as `omp:openai-codex/gpt-6-sol`. Its `cliSlug` is the actual harness argument. Native metadata is preserved in `metadata`, including context limits, pricing, modalities, capabilities, resolved aliases, and reasoning descriptions when supplied by the source. Model details include argument arrays rather than shell-interpolated command strings. Claude uses `--effort`, Codex uses `-c 'model_reasoning_effort="..."'`, and OpenCode uses `--variant`. Thinking entries include the complete `launchArgs` array. Non-chat OMP catalog entries have empty launch arrays.

`model select` saves only o-ai's selection, in `~/.config/aiview/selection.json` (aiview's file, kept so an existing selection survives the rename), mode `0600`. It does not change a harness's selected model.

### The state

`status state` is the catalog's state source (`"state": "status.state"`, the ochazuke/cli convention): one process, one collection covering every noun's need, processes inspected once, answering every list as JSON Lines, one row per line tagged with its noun, no outer array:

```js
// o-ai status state
{"noun":"harness","id":"harness/claude","label":"claude","data":{"id":"claude","installed":true,"configured":true,"executable":"/Users/me/.local/bin/claude","processStatus":"3 running processes (including helpers)","catalogStatus":"live initialization metadata"}}
{"noun":"skill","id":"skill/skill:claude:/Users/me/.claude/skills/review/SKILL.md","label":"review [claude]","data":{"name":"review","description":"Review a diff…","path":"/Users/me/.claude/skills/review/SKILL.md","harnesses":["claude"],"scope":"Claude user"}}
// ... every row of harness, provider, model, thinking, skill, agent, settings, status
```

A state row's `data` is the record's brief fields (`specs[noun].brief` in `shell.go`), enough for a host to draw a row's details without a `show`, which still prints the whole record:

| Noun | `data` keys in the state |
| --- | --- |
| harness | `id`, `installed`, `configured`, `executable`, `processStatus`, `catalogStatus` |
| provider | `harness`, `slug`, `access` |
| model | `harness`, `provider`, `name`, `cliSlug` |
| thinking | `slug`, `harness`, `provider`, `cliSlug` (the model is the row id without `:SLUG`) |
| skill, agent | `name`, `description` (one line, cut to 160 characters with `…`), `path`, `harnesses`, `scope`, `source` |
| settings | `path`, `scope`, `error` |
| status | `kind`, `count` |

On the owner's machine (8,262 rows) the answer is 2.35 MB, against 1.02 MB with ids and labels only and 5.4 MB for the eight lists' lean records. A noun with no records has no lines. `NOUN list --format jsonl` prints the same line shape with `data` (the default `json` stays an array). nabe runs `status state` once at startup, then on its refresh interval, after a verb of the extension and on `extension refresh` (with `--fresh`), instead of eight `NOUN list` processes.

`status state` speaks ochazuke's state deltas (ochazuke v0.8.0; the catalog's `stateDelta: 1`): a host sends `--since-cache ID --since LIST=HASH,...` and gets `unchanged`, a `delta` or the list in `full` per list. Its cache is `$XDG_CACHE_HOME/o-ai/state/`, scoped by the inventory cache key. Its fingerprint (`stateFingerprint` in `inputs.go`) is the recorded inputs' stat, the collection's time, the o-ai binary's stat (a rebuild answers in full) and the harnesses' process statuses, inspected with one `ps` as the state would (processes are never cached, so a harness starting or exiting is a changed harness row). When it equals the last answer's, an unchanged poll answers without reading the inventory. It refuses, and the verb runs, with no recorded collection, one that does not cover the state's need, or one as old as `--ttl`, so the probes still refresh every TTL. On the owner's machine an unchanged poll takes about 130 ms (of which `ps` is 70 ms) against 390 ms without the fingerprint, 954 bytes either way (the full answer is 2.9 MB).

### MCP servers and tools

`mcp list` reads the servers each harness configures, from its own files, without starting one:

| Harness | Files (scope) |
| --- | --- |
| Claude Code | `~/.claude.json` `mcpServers` (user) and `projects[PROJECT].mcpServers` (local), `PROJECT/.mcp.json` (project) |
| Codex | `~/.codex/config.toml` `[mcp_servers.*]` (user), `PROJECT/.codex/config.toml` |
| omp | `~/.omp/agent/mcp.json` (user), `PROJECT/.omp/mcp.json`; a name in `disabledServers` is `disabled` |
| Gemini | `~/.gemini/settings.json` and `PROJECT/.gemini/settings.json` `mcpServers` |
| OpenCode | `opencode.json`/`opencode.jsonc` `mcp` (`type: local` with a command array, `type: remote`) |

A row is `mcp/HARNESS:SCOPE:NAME`, labelled `NAME [HARNESS]`; `--server NAME` keeps one name. `env` and `headers` are names only, never values; `url` loses its credentials and query; an argument after or attached to a secret-looking flag (`--api-key VALUE`, `--token=VALUE`) and recognized token formats read `[REDACTED]`.

`mcp-tool list [SERVER]` (also `mcp tools [SERVER]`, or `mcp tools --target mcp/...` for that row's server) lists the tools a server exposes **as a harness cached them**: omp keeps each server's `tools/list` answer in `~/.omp/agent/agent.db` (table `cache`, keys `mcp_tools:SERVER`, 30 days), which o-ai reads with `sqlite3 -readonly`, selecting those rows alone (the database also holds logins). A row is `mcp-tool/omp:SERVER/TOOL`, with `expiresAt` from the cache; `show` adds the `inputSchema`. The cache is keyed by server name, so a server another harness configures under the same name shows omp's tools for it. The other harnesses keep no tool list on disk: listing their servers' tools would mean starting each server (`initialize`, `tools/list`), which o-ai does not do (it is read-only and never runs a configured command), so a server omp has not connected to has no tool rows.

### Skill sources

Skill roots outside every harness's search path are scanned directly. The built-in is `ai-tools` (`~/proj/ai-tools/catalog/skills`); `~/.config/o-ai/sources.json` (under `--home`) adds roots and replaces or removes built-ins by name, an empty path removing one:

```js
sources = { skills: [{ name: "team", path: "~/team/skills" }, { name: "ai-tools", path: "" }] }
```

A source's skill that a harness also sees (the same file after links, such as `~/.claude/skills/review -> ~/proj/ai-tools/catalog/skills/review`) is not listed twice: the harness rows carry `source: "ai-tools"` and the source has no row of its own. A skill no harness links is a row of its own, `skill/skill:ai-tools:PATH`, labelled `NAME [ai-tools]`, scope `source`, with no harnesses (so `--harness` drops it). The sources file is part of the inventory cache key: editing it collects again.

### Data sources and limits

| Harness | Model metadata | Settings and resources |
| --- | --- | --- |
| OMP | `omp models --json --no-extensions --kind all` | User/project YAML, shared and harness resource paths, bundled agent exporter |
| Claude Code | Initialization control response with no user prompt | User/project/local JSONC, installed plugin manifest, runtime agents |
| Codex | Local `models_cache.json`, including its fetch timestamp | TOML, configured agent files, skills |
| Pi | Installed `ModelRegistry` and `getSupportedThinkingLevels` | User/project JSON, custom models, skills and agent files |
| OpenCode | `opencode models --verbose --pure` | JSON/JSONC, inline agents, skill and agent directories |
| Gemini | Configured model only, no inferred capability catalog | User/project settings and discovered resource files |
| `clawed` | Probed only if an executable with that name exists; expects Claude-compatible initialization | Its own configuration directory |

#### Quota

`quota` makes a read-only HTTPS `GET` to each vendor's usage endpoint with the harness's own login, never an inference request, and never refreshes or writes a login:

| Harness | Login | Endpoint | Fallback |
| --- | --- | --- | --- |
| Claude Code | macOS keychain item `Claude Code-credentials` (when `--home` is your own), else `$CLAUDE_CONFIG_DIR/.credentials.json` (`~/.claude`); an expired token is an error telling you to run `claude` | `https://api.anthropic.com/api/oauth/usage` (`anthropic-beta: oauth-2025-04-20`) | `five_hour`/`seven_day` when the answer has no `limits` |
| Codex | `$CODEX_HOME/auth.json` (`~/.codex`) with `auth_mode: chatgpt`; the account id from `tokens.account_id`, else the access token's claim. An API-key login has no plan quota and says so | `https://chatgpt.com/backend-api/codex/usage` (`ChatGPT-Account-Id`) | the last `token_count` event with `rate_limits` in the newest `$CODEX_HOME/sessions/YYYY/MM/DD/*.jsonl` |

The tool distinguishes installed executables, configuration presence, running processes, catalog membership, configured credentials where reported, and disabled providers. Catalog membership is not proof of authentication, subscription entitlement, network health, or successful inference. Pi can report configured credentials without validating them; other adapters do not claim authentication success.

Process discovery matches executable paths and Node/Bun/Deno script entrypoints. Counts include background helpers, not just interactive sessions. Unrecognized wrappers can remain unobserved. Only matching PIDs are retained, never process arguments.

Codex's model cache can be stale and is labeled with its fetch time. Custom Codex providers are not assigned the OpenAI cache's capabilities. OMP and OpenCode external extensions are excluded from metadata commands; Pi's catalog comes from its installed SDK rather than session extensions. Pi requires a Node-based installation whose SDK is available beside its executable. Adapter failures remain visible instead of fabricating model capabilities.

Resource entries include their source path, resolved symlink target, frontmatter, and content. Shared paths identify associated harnesses. Discovery does not prove that a particular running session loaded the resource. Plugin disablement and malformed frontmatter are labeled explicitly. Invalid resources remain inspectable. Hidden resource directories are skipped. Runtime agent records can coexist with file-backed definitions because the native response does not always provide a source path.

Settings retain file provenance and scope rather than claiming to reproduce every harness's effective session configuration. Explicit command-line overlays, managed enterprise policy, remote profiles, and live session overrides may differ. An OMP profile requires the corresponding `PI_CODING_AGENT_DIR` for file inspection; otherwise a warning explains the discrepancy. Malformed settings remain listed with an error, without dumping their unparsed contents.

### Safety

o-ai does not edit harness settings or launch an inference request; `quota` reads the vendors' usage endpoints only. Native metadata commands can update their own caches and load trusted harness configuration. OMP bundled agents are exported into an owned temporary directory, read, then removed. Use `--no-probe` when only file inspection is wanted.

Settings and metadata redact secret-bearing fields, credential/header/environment containers, command hooks, recognized token formats, and URL credentials/query strings. Resource bodies remain inspectable with recognized token formats redacted. This is not a guarantee that arbitrary prose cannot contain a secret; do not publish a full inventory without reviewing it.

## In the family

o-ai is one of the family's `o-*` tools: an [oden](https://github.com/unixsuperhero/oden) app whose nouns are an [ochazuke](https://github.com/unixsuperhero/ochazuke) runtime behind `ochazuke/cli`, with no TUI of its own.

| Where | How o-ai appears |
| --- | --- |
| `o ai ...` | oden's `o` root runs any `o-<tool>` on PATH. |
| `mtm ai ...` | [matomeru](https://github.com/unixsuperhero/matomeru)'s `mtm` discovers the family's `o-<sub>` tools after its own `mtm-<sub>` bins, so `mtm ai` runs `o-ai` unless an `mtm-ai` is on PATH. |
| nabe (`o tui`) | The TUI host mounts o-ai's catalog as the page `ai`, one list per noun, fed by `status state`. |

### The TUI is nabe

```sh
MTM_SERVER=default o-nabe extension register o-ai   # registers as "ai"
o tui                                               # a page ai with one list per noun
```

Restart nabe after `o-nabe extension refresh o-ai`. A row's verbs run as `o-ai NOUN VERB --target ROW_ID --json '{}' --yes`.

The state nabe reads is under [Concepts](#the-state).

## Next

Richer `agent` verbs; an opt-in `mcp-tool list --probe` that starts a stdio server, asks `tools/list` and stops it (pending work), for servers no harness has cached.

## Development

```sh
go test -race ./...
go vet ./...
go build .
```

The tests cover command ambiguity, the catalog (schema 1, `rows` on every list), row ids and `--target`, model selection, reasoning arguments, credential redaction, JSONC/TOML parsing, invalid configuration visibility, symlink cycles, non-chat launch handling, and script-based process identity. Real native commands must also be exercised after adapter changes (`o-ai status show`), and the nabe page after catalog changes (`o-nabe extension refresh o-ai && o-tui --smoke`).

## Logo and demo

The logo is the letters "ai" in the family's rounded stroke with a spark for the i's dot: ai (藍) is indigo, and o-ai looks at the AI on a machine. An alternative, Unplugged (a plug kept out of its socket, because o-ai never makes a model request), is in [docs/logo/alt](docs/logo/alt) while the choice is open. `python3 docs/logo/build.py` writes both sets (`assets/icon.svg`, `assets/logo-light.svg`, `assets/logo-dark.svg`, and the same names under `docs/logo/alt`), with the wordmark drawn in the family's lettering (ochazuke's and matomeru's; `--check` confirms the glyphs still redraw theirs, `--preview DIR` renders the icons at 16, 32 and 128 px). `vhs docs/vhs/demo.tape` records `assets/demo.gif` in a throwaway home; see [docs/vhs/README.md](docs/vhs/README.md).

MIT license.
