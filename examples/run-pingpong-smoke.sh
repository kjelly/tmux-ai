#!/usr/bin/env bash
set -euo pipefail

# Start a self-contained pingpong smoke test in a fresh tmux session.
# Unknown options are passed through to `tmux-ai pingpong`.

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)

preset="$script_dir/pingpong-qwen35-4b-smoke.yaml"
agent_script="$script_dir/pingpong-demo-agent.py"
session_prefix="tmux-ai-pingpong-smoke"
keep_session=0
tmux_ai_override="${TMUX_AI_BIN:-}"
model=""
host=""
passthrough=()

usage() {
    cat <<'EOF'
Usage:
  examples/run-pingpong-smoke.sh [wrapper options] [pingpong options]

The wrapper creates one fresh tmux session with two windows:
  planner  -> examples/pingpong-demo-agent.py planner
  reviewer -> examples/pingpong-demo-agent.py reviewer

Wrapper options:
  --model MODEL          Pass the Lemonade/Ollama model to tmux-ai.
  --host URL             Pass the Lemonade/Ollama host to tmux-ai.
  --preset FILE          Use a different pingpong preset.
  --agent-script FILE    Use a different demo-agent script.
  --session-prefix NAME  Prefix for the generated tmux session name.
  --tmux-ai PATH         Use a specific tmux-ai executable.
  --keep-session         Keep the generated session after tmux-ai exits.
  -h, --help             Show this help.

Any other options are passed to `tmux-ai pingpong`. For example:
  --max-rounds 4 --coordinator-timeout 90 --dry-run

The wrapper always supplies --pane1 and --pane2 for its two generated windows.
Use --start pane1 to avoid an extra start-pane selector call.

The bundled mock agent is a plain terminal stdin program rather than a TUI
composer, so this wrapper disables UI-specific submission checks by default.
Pass later values to test them explicitly, for example:
  --submit-confirm-timeout 8000

The wrapper also supplies --timeout 3600. A coordinator timeout cannot extend
the parent workflow timeout, so pass a later --timeout value at least as large
as --coordinator-timeout when testing a longer-running model.
EOF
}

while (($# > 0)); do
    case "$1" in
        --model)
            [[ $# -ge 2 ]] || { echo "--model requires a value" >&2; exit 2; }
            model=$2
            shift 2
            ;;
        --host)
            [[ $# -ge 2 ]] || { echo "--host requires a value" >&2; exit 2; }
            host=$2
            shift 2
            ;;
        --preset)
            [[ $# -ge 2 ]] || { echo "--preset requires a value" >&2; exit 2; }
            preset=$2
            shift 2
            ;;
        --agent-script)
            [[ $# -ge 2 ]] || { echo "--agent-script requires a value" >&2; exit 2; }
            agent_script=$2
            shift 2
            ;;
        --session-prefix)
            [[ $# -ge 2 ]] || { echo "--session-prefix requires a value" >&2; exit 2; }
            session_prefix=$2
            shift 2
            ;;
        --tmux-ai)
            [[ $# -ge 2 ]] || { echo "--tmux-ai requires a value" >&2; exit 2; }
            tmux_ai_override=$2
            shift 2
            ;;
        --keep-session)
            keep_session=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        --)
            shift
            passthrough+=("$@")
            break
            ;;
        *)
            passthrough+=("$1")
            shift
            ;;
    esac
done

[[ -f "$preset" ]] || { echo "preset not found: $preset" >&2; exit 1; }
[[ -f "$agent_script" ]] || { echo "agent script not found: $agent_script" >&2; exit 1; }
command -v tmux >/dev/null 2>&1 || { echo "tmux is required" >&2; exit 1; }

if [[ -n "$tmux_ai_override" ]]; then
    tmux_ai_cmd=("$tmux_ai_override")
elif command -v go >/dev/null 2>&1; then
    tmux_ai_cmd=(go run .)
elif [[ -x "$repo_root/tmux-ai" ]]; then
    tmux_ai_cmd=("$repo_root/tmux-ai")
elif command -v tmux-ai >/dev/null 2>&1; then
    tmux_ai_cmd=(tmux-ai)
else
    echo "tmux-ai was not found; build it or set TMUX_AI_BIN/--tmux-ai" >&2
    exit 1
fi

session="${session_prefix}-$$"
while tmux has-session -t "$session" 2>/dev/null; do
    session="${session_prefix}-${RANDOM}"
done

cleanup() {
    status=$?
    if ((keep_session == 0)); then
        tmux kill-session -t "$session" 2>/dev/null || true
    else
        echo "Keeping tmux session: $session" >&2
    fi
    exit "$status"
}
trap cleanup EXIT

echo "Creating tmux session: $session" >&2
tmux new-session -d -s "$session" -n planner -c "$repo_root" python3 "$agent_script" planner
tmux new-window -t "$session" -n reviewer -c "$repo_root" python3 "$agent_script" reviewer
# Keep a failed demo process visible long enough to capture its diagnostic
# instead of letting tmux remove the pane and hide the underlying error.
tmux set-option -t "$session:planner" remain-on-exit on
tmux set-option -t "$session:reviewer" remain-on-exit on

# Do not assume pane index 0. User tmux configuration may set
# pane-base-index=1 (or another value), so use the stable %pane_id values.
planner_pane=$(tmux list-panes -t "$session:planner" -F '#{pane_id}')
reviewer_pane=$(tmux list-panes -t "$session:reviewer" -F '#{pane_id}')
[[ -n "$planner_pane" ]] || { echo "could not find planner pane" >&2; exit 1; }
[[ -n "$reviewer_pane" ]] || { echo "could not find reviewer pane" >&2; exit 1; }

# A coding-agent TUI visibly changes from a pending composer to a submitted
# transcript after Enter. The tiny Python demo has no such screen state, so
# those UI-specific checks cannot confirm a successful submission. These are
# wrapper-only defaults: appended passthrough arguments can override them.
pingpong_args=(
    pingpong
    --preset "$preset"
    --start pane1
    --timeout 3600
    --submit-confirm-timeout 0
    --input-echo-check off
    --pane1 "$planner_pane"
    --pane2 "$reviewer_pane"
)
if [[ -n "$model" ]]; then
    pingpong_args+=(--model "$model")
fi
if [[ -n "$host" ]]; then
    pingpong_args+=(--host "$host")
fi
pingpong_args+=("${passthrough[@]}")

echo "Planner pane:  $planner_pane ($session:planner)" >&2
echo "Reviewer pane: $reviewer_pane ($session:reviewer)" >&2
echo "Running: ${tmux_ai_cmd[*]} ${pingpong_args[*]}" >&2
"${tmux_ai_cmd[@]}" "${pingpong_args[@]}"
