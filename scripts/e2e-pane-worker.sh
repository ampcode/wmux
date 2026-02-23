#!/usr/bin/env bash
set -euo pipefail

printf '__WMUX_E2E_READY__\n'

while true; do
  if IFS= read -r line; then
    # %q makes control chars explicit (for example tab becomes $'\t').
    printf '__WMUX_INPUT_Q__:%q\n' "$line"
  fi
  sleep 0.02
done
