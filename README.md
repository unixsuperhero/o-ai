# aiview

A standalone Go CLI and terminal browser for local AI harnesses, providers, models, thinking levels, skills, and agents. It has no dependency on Ochazuke. JSON output is the integration point for other tools.

## Run

Requires Go 1.25 or newer to build. The generated binary does not need Go.

```sh
cd ~/hub/ai/utils/aiview
go build -o aiview .
./aiview status
./aiview tui
```

Install on your command path:

```sh
go install .
# Add $(go env GOPATH)/bin to PATH if needed.
```

## Commands

```sh
aiview harness list
aiview provider list --harness omp
aiview model list --harness omp
aiview model show openai-codex/gpt-6-sol --harness omp
aiview thinking list --model openai-codex/gpt-6-sol --harness omp
aiview thinking show max --model openai-codex/gpt-6-sol --harness omp

aiview model show opus --harness claude
aiview model select opus --harness claude
aiview model show

aiview skill list --search review
aiview agent show scout --harness omp
aiview settings list --harness codex
aiview status --json
```

`list model` and `show model ID` also work. Every command word accepts a unique prefix. `mo li`, `prov sh`, `th li`, and `mo se` work. `s` is ambiguous between settings, show, skill, and status. `mo s` is ambiguous between select and show. Ambiguity is an error, never a first-match selection. Plural resource names are accepted.

A model's stable inventory ID includes its harness and provider, such as `omp:openai-codex/gpt-6-sol`. Its `cliSlug` is the actual harness argument, such as `openai-codex/gpt-6-sol`. Native metadata is preserved in `metadata`, including context limits, pricing, modalities, capabilities, resolved aliases, and reasoning descriptions when supplied by the source.

Model details include argument arrays rather than shell-interpolated command strings:

```json
{
  "harness": "omp",
  "name": "GPT-6 Sol",
  "cliSlug": "openai-codex/gpt-6-sol",
  "args": ["omp", "--model", "openai-codex/gpt-6-sol"],
  "thinking": [
    {"slug": "xhigh", "name": "Extra high", "args": ["--thinking", "xhigh"]}
  ]
}
```

Claude uses `--effort`, Codex uses `-c 'model_reasoning_effort="..."'`, and OpenCode uses `--variant`. Thinking entries include the complete `launchArgs` array. OpenCode variants are reported as variants, not assumed to mean reasoning effort. Non-chat OMP catalog entries have empty launch arrays rather than an invalid chat command.

`model select` saves only aiview's selection, in `~/.config/aiview/selection.json`, with mode `0600`. It does not change a harness's selected model. Selecting a model with multiple matches requires an exact ID or a harness/provider filter.

### Options

| Option | Behavior |
| --- | --- |
| `--harness NAME` | Filter by exact harness ID |
| `--provider NAME` | Filter by exact provider slug |
| `--model NAME` | Filter model/thinking rows by model ID, slug, or display name |
| `--search TEXT` | Case-insensitive name, ID, and description search |
| `--project DIRECTORY` | Inspect a project other than the current directory |
| `--home DIRECTORY` | Use another home for file discovery and child processes |
| `--timeout 15s` | Per-metadata-command deadline |
| `--no-probe` | Skip native harness commands; inspect files and caches only |
| `--json` | Emit structured JSON on stdout |

Flags may appear before or after command words. `status --json` emits the complete inventory, including warnings. Resource list/show commands emit the selected records. Environment-based configuration roots still take precedence over `--home`; clear them when inspecting an isolated home.

## Terminal browser

```sh
aiview tui --project ~/proj/example
```

The list and detail panes share the CLI inventory. Enter opens a full-width, scrollable detail view.

| Key | Action |
| --- | --- |
| Tab / Shift-Tab | Next / previous category |
| Up/Down or k/j | Select an entry |
| Enter / Esc | Expand details / return |
| Page Up / Page Down | Page through list or expanded details |
| `/` | Search the current category; Enter finishes editing |
| `h` | Cycle harness filters |
| `m` on a provider | Browse that provider's models |
| `t` on a model | Browse that model's thinking levels |
| `s` on a model | Save the CLI model selection |
| `b` | Clear filters |
| `r` | Refresh the inventory |
| `w` | Inspect collection warnings |
| `o` | Browse folders or inspect a configuration file |
| `q` / Ctrl-C | Quit |

In the file picker, Right opens a directory, Enter selects a folder or configuration file, Space uses the current directory as the project, and Esc cancels. Files are parsed and redacted before display. The browser requires at least 55 columns and 13 rows.

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

Aiview does not edit harness settings or launch an inference request. Native metadata commands can update their own caches and load trusted harness configuration. OMP bundled agents are exported into an owned temporary directory, read, then removed. Use `--no-probe` when only file inspection is wanted.

Settings and metadata redact secret-bearing fields, credential/header/environment containers, command hooks, recognized token formats, and URL credentials/query strings. Resource bodies remain inspectable with recognized token formats redacted. This is not a guarantee that arbitrary prose cannot contain a secret; do not publish a full inventory without reviewing it.

## Development

```sh
go test -race ./...
go vet ./...
go build .
```

The tests cover command ambiguity, model selection, reasoning arguments, credential redaction, JSONC/TOML parsing, invalid configuration visibility, symlink cycles, non-chat launch handling, and script-based process identity. Real native commands and the interactive browser must also be exercised after adapter or UI changes.
