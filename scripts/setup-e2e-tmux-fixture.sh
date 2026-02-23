#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <session-name>" >&2
  exit 2
fi

session="$1"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
worker="$root_dir/scripts/e2e-pane-worker.sh"

if ! command -v tmux >/dev/null 2>&1; then
  echo "tmux not found in PATH" >&2
  exit 1
fi

if [[ ! -x "$worker" ]]; then
  echo "worker script missing: $worker" >&2
  exit 1
fi

# Always start from a clean deterministic session.
tmux kill-session -t "$session" >/dev/null 2>&1 || true
tmux new-session -d -s "$session"

pane_id="$(tmux list-panes -t "$session" -F '#{pane_id}' | head -n1)"
if [[ -z "$pane_id" ]]; then
  echo "failed to resolve initial pane id for session $session" >&2
  exit 1
fi

tmux respawn-pane -k -t "$pane_id" "bash '$worker'"

pane_id="$(tmux list-panes -t "$session" -F '#{pane_id}' | head -n1)"
if [[ -z "$pane_id" ]]; then
  echo "failed to resolve pane id for session $session" >&2
  exit 1
fi

printf '%s\n' "$pane_id"
