#!/usr/bin/env bash
# seed.sh DIR: build o-ai (and mtm) into DIR/bin and write an invented AI
# setup under DIR/home: two Claude Code skills and an agent, Claude and Codex
# settings, and an MCP server for each. It writes DIR/env, which points HOME
# and every XDG directory inside DIR, puts DIR/bin first on a bare PATH (so no
# real harness is found or probed) and starts in DIR/work/acme. Nothing reads
# or writes outside DIR.
#
#	D=$(mktemp -d /tmp/oai.XXXX) && docs/vhs/seed.sh "$D" && . "$D/env" && o-ai status list
#
# mtm is built from the matomeru checkout in $MATOMERU (default ../matomeru,
# the family's sibling layout); the demo's last step runs mtm ai.
set -euo pipefail

D=${1:?usage: seed.sh DIR}
D=$(cd "$D" && pwd -P)
H=$D/home
mkdir -p "$D"/{bin,cache,config,data,state,run,work/acme} "$H"/.claude/{skills/review,skills/release-notes,agents} "$H/.codex"

GOWORK=off go build -trimpath -o "$D/bin/o-ai" .
M=${MATOMERU:-../matomeru}
[ -d "$M/cmd/mtm" ] || { echo "seed.sh: no matomeru checkout at $M (set MATOMERU)" >&2; exit 1; }
(cd "$M" && GOWORK=off go build -trimpath -o "$D/bin/mtm" ./cmd/mtm)

cat > "$H/.claude/skills/review/SKILL.md" <<'MD'
---
name: review
description: Review a diff for bugs, missing tests and unclear names.
---
Read the diff, then list what to fix, most important first.
MD
cat > "$H/.claude/skills/release-notes/SKILL.md" <<'MD'
---
name: release-notes
description: Draft release notes from the commits since the last tag.
---
Group the commits by kind and write one line each.
MD
cat > "$H/.claude/agents/planner.md" <<'MD'
---
name: planner
description: Breaks a feature into small, testable steps.
---
Plan in steps that each end in a passing check.
MD
echo '{"model":"opus","permissions":{"allow":["Bash(go test:*)"]}}' > "$H/.claude/settings.json"
echo '{"mcpServers":{"weather":{"type":"http","url":"https://weather.example.com/mcp"}}}' > "$H/.claude.json"
cat > "$H/.codex/config.toml" <<'TOML'
model = "gpt-5-codex"

[mcp_servers.docs]
command = "docs-mcp"
args = ["--root", "~/notes"]
TOML

# The socket path is short on purpose (unix sockets cap near 104 bytes).
cat > "$D/env" <<ENV
unset TUIOS_ENV TUIOS_SESSION TUIOS_PANE_ID TUIOS_SOCKET MTM_CONFIG_DIR MTM_SERVER CLAUDE_CONFIG_DIR CODEX_HOME
export HOME=$H XDG_CACHE_HOME=$D/cache XDG_CONFIG_HOME=$D/config XDG_DATA_HOME=$D/data
export XDG_STATE_HOME=$D/state XDG_RUNTIME_DIR=$D/run
export PATH=$D/bin:/usr/bin:/bin PS1='\$ '
mtm() { "$D/bin/mtm" --data "$D/data/mtm.db" --config "$D/config" --socket "$D/run/none.sock" "\$@"; }
cd "$D/work/acme"
ENV
