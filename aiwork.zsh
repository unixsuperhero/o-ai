# Named, centrally stored OMP workspaces. Loaded by ai.zsh.
aiwork() {
  emulate -L zsh
  local base=${AIWORK_HOME:-$HOME/.config/omp-workspaces}
  local action=${1:-help} name dir root instructions='' item
  local -a roots args
  (( $# )) && shift
  case $action in
    help|-h|--help)
      print -r -- 'Usage:
  aiwork new NAME ["INSTRUCTIONS"] [--add-dir DIR ...]
  aiwork start NAME [OMP_OPTIONS_OR_PROMPT ...]
  aiwork list

new saves the current directory plus additional roots; it does not launch AI.
Profiles live in ~/.config/omp-workspaces (override with AIWORK_HOME).
Edit a profile’s AGENTS.md to maintain its compact briefing.'
      return 0 ;;
    list)
      (( $# == 0 )) || { print -u2 -- 'Usage: aiwork list'; return 2; }
      for dir in "$base"/*(N/); do
        [[ -f "$dir/roots.txt" ]] && print -r -- "${dir:t}"
      done
      return 0 ;;
    new|start) ;;
    *) print -u2 -r -- "Unknown action: $action (see aiwork help)"; return 2 ;;
  esac
  name=${1:-}
  # A profile is a single directory component, never a path.
  if [[ -z $name || $name == .* || $name == *[^a-zA-Z0-9_.-]* ]]; then
    print -u2 -- 'Profile names must start with a letter, digit, _ or -, and contain only letters, digits, _, - or .'
    return 2
  fi
  shift
  dir="$base/$name"
  if [[ $action == new ]]; then
    roots=("${PWD:A}")
    if (( $# )) && [[ $1 != --add-dir ]]; then
      instructions=$1
      shift
    fi
    while (( $# )); do
      if [[ $1 != --add-dir || $# -lt 2 ]]; then
        print -u2 -- 'Usage: aiwork new NAME ["INSTRUCTIONS"] [--add-dir DIR ...]'
        return 2
      fi
      root=$2
      [[ -d $root ]] || { print -u2 -r -- "Not a directory: $root"; return 2; }
      roots+=("${root:A}")
      shift 2
    done
    roots=("${(@u)roots}")
    for root in "${roots[@]}"; do
      [[ $root != *$'\n'* ]] || { print -u2 -- 'Project paths cannot contain newlines.'; return 2; }
    done
    command mkdir -p -- "$base" || return
    # mkdir refuses existing profiles; never overwrite saved instructions.
    command mkdir -- "$dir" || return
    if ! (
      umask 077
      printf '%s\n' "${roots[@]}" > "$dir/roots.txt" &&
      printf '%s\n' 'autoResume: false' > "$dir/fresh.yml" &&
      {
        printf '# %s working context\n\n' "$name"
        printf '%s\n' 'Project directories:'
        printf -- '- `%s`\n' "${roots[@]}"
        printf '\n%s\n' 'Keep startup lean. Do not tour repositories or read whole files without a task. Read relevant source on demand, respect each repository’s AGENTS.md, and verify current code before making precise claims. Target the requested repository explicitly. Keep this briefing architectural, not a transcript.'
        if [[ -n $instructions ]]; then
          printf '\n## Additional instructions\n\n%s\n' "$instructions"
        fi
      } > "$dir/AGENTS.md"
    ); then
      command rm -rf -- "$dir"
      return 1
    fi
    print -r -- "Created $name: $dir"
    print -r -- "Start with: aiwork start $name"
    return 0
  fi
  for item in roots.txt AGENTS.md fresh.yml; do
    [[ -f "$dir/$item" ]] || { print -u2 -r -- "Missing profile file: $dir/$item"; return 1; }
  done
  roots=("${(@f)$(<"$dir/roots.txt")}")
  (( ${#roots} )) || { print -u2 -- 'Profile has no project directories.'; return 1; }
  args=(--cwd "$dir" --config "$dir/fresh.yml")
  for root in "${roots[@]}"; do
    [[ $root == /* && -d $root ]] || { print -u2 -r -- "Project directory unavailable: $root"; return 1; }
    args+=(--add-dir "$root")
  done
  # Resolve the user's omp shell function, retaining its existing setup.
  omp "${args[@]}" "$@"
}
