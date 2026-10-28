# The README demo: o-ai reading an invented AI setup and printing JSON.
# Regenerate from the repository root:  vhs docs/vhs/demo.tape
# (with a matomeru checkout at ../matomeru, or MATOMERU=PATH vhs ...)
#
# seed.sh builds o-ai and mtm into a fresh /tmp/oai.XXXX and writes the
# setup there, with HOME, the XDG directories and a bare PATH inside it.
# The recording never reads your own harnesses, skills, settings, logins or
# TUIOS daemon, and o-ai never makes a model request.

Output assets/demo.gif

Require go
Require jq

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontSize 14
Set Width 1200
Set Height 460
Set Padding 16
Set WindowBar Colorful
Set Framerate 15
Set TypingSpeed 50ms
Set CursorBlink false

# Off camera: build, seed.
Hide
Type "export D=$(mktemp -d /tmp/oai.XXXX) && docs/vhs/seed.sh $D && . $D/env && clear"
Enter
Wait@120s /^\$$/
Show
Sleep 500ms

# Every verb prints JSON: rows of {id, label, data, cells}.
Type "o-ai status list"
Sleep 300ms
Enter
Sleep 2.5s

# The harnesses, without running one: configured from files, none installed here.
Ctrl+L
Type "o-ai harness list | jq -r '.[].cells[:2] | @tsv' | column -t -s $'\t'"
Sleep 300ms
Enter
Sleep 2s

# Skills by unique prefix (sk li is skill list), then one skill's record.
Type "o-ai sk li | jq -r '.[].label'"
Sleep 300ms
Enter
Sleep 1.5s
Type "o-ai skill show review | jq '{name, description, harnesses, scope}'"
Sleep 300ms
Enter
Sleep 2.5s

# Every configured MCP server of every harness; none is started.
Ctrl+L
Type "o-ai mcps | jq -r '.[].cells | @tsv' | column -t -s $'\t'"
Sleep 300ms
Enter
Sleep 2s

# The same tool through mtm: mtm ai runs o-ai.
Type "mtm ai agent list | jq -r '.[].cells | @tsv' | column -t -s $'\t'"
Sleep 300ms
Enter
Sleep 3s

Hide
Type "cd / && rm -rf $D"
Enter
Sleep 500ms
