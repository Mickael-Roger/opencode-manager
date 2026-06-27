#!/usr/bin/env bash
# Runs a command, retrying a few times with a delay between attempts, to ride out
# transient infrastructure flakes — most notably Docker Hub registry timeouts
# when pulling the base image (e.g. "dial tcp ...: i/o timeout"). A command that
# fails for a real reason still fails the step, just after exhausting the retries.
#
# Usage:            scripts/ci/retry.sh <command> [args...]
# Tunable via env:  RETRY_ATTEMPTS (default 3), RETRY_DELAY seconds (default 15)
set -uo pipefail

attempts="${RETRY_ATTEMPTS:-3}"
delay="${RETRY_DELAY:-15}"

n=1
while true; do
  if "$@"; then
    exit 0
  fi
  if [ "$n" -ge "$attempts" ]; then
    echo "::error::command failed after ${n} attempt(s): $*"
    exit 1
  fi
  echo "::warning::attempt ${n}/${attempts} failed: $*; retrying in ${delay}s"
  n=$((n + 1))
  sleep "$delay"
done
