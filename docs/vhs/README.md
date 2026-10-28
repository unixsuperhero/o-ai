# README demo

`demo.tape` records `assets/demo.gif`, the animation under the README header. It runs `o-ai` (and once `mtm ai`) over an invented AI setup that `seed.sh` writes: two Claude Code skills and an agent, Claude and Codex settings, and one MCP server for each.

Regenerate from the repository root (needs [VHS](https://github.com/charmbracelet/vhs), Go, `jq` and a matomeru checkout; `brew install vhs`):

```sh
vhs docs/vhs/demo.tape                                  # with matomeru at ../matomeru
MATOMERU=~/proj/matomeru vhs docs/vhs/demo.tape         # or anywhere else
```

It takes about a minute and writes a GIF of about 30 seconds and under 300 KB.

## Your data stays out of it

Off camera, the tape makes a fresh `/tmp/oai.XXXX` and runs `seed.sh` on it. The script builds `o-ai` and `mtm` into that directory's `bin` and writes an `env` file that points `HOME` and every `XDG_*` directory inside it, sets `PATH` to that `bin` plus `/usr/bin:/bin` (so no real harness is found, probed or listed as installed), unsets `TUIOS_*`, `MTM_CONFIG_DIR`, `MTM_SERVER`, `CLAUDE_CONFIG_DIR` and `CODEX_HOME`, defines an `mtm` function that always passes `--data`, `--config` and a `--socket` nothing listens on, and starts in `DIR/work/acme`, outside the fake home. The recording never reads your harnesses, skills, settings or logins, and o-ai makes no model request. The tape deletes the directory when it ends.

One thing is not sandboxed: o-ai inspects running processes to count each harness's, so `harness list`'s raw rows carry the machine's real process counts. The tape prints only each harness's name and state (`.cells[:2]`), which come from the fake home.

To look at the demo data by hand:

```sh
D=$(mktemp -d /tmp/oai.XXXX) && docs/vhs/seed.sh "$D" && (. "$D/env" && o-ai status list); rm -rf "$D"
```

## Changing it

Edit the steps between `Show` and `Hide`. Pipe a list through `jq` to keep lines short: a raw row is one long JSON line. Check a few frames before committing, for example:

```sh
ffmpeg -i assets/demo.gif -vf fps=1/2 /tmp/frame%02d.png
```
