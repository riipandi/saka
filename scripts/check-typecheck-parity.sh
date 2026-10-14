#!/usr/bin/env bash
# Pin the Dockerfile's typecheck chain to the vite task it stands in for.
#
# `vp run typecheck` crashes child processes with a native stack overflow in
# Linux containers (issue-20261012_1605: vp's task-cache fspy preload x
# Node 24), so deploy/Dockerfile repeats the root vite.config.ts `typecheck`
# task's tsc commands by hand. Two hand-kept copies drift silently; this
# guard fails when the project lists stop matching, and the pre-commit hook
# runs it on every commit.
#
# The comparison is the ordered project list — the env prefix, the line
# continuations, and the trailing build steps are presentation. A project
# appearing in one list but not the other, or in a different order, is
# exactly the drift the guard exists for.
#
# When a fixed vp ships and the Dockerfile restores `vp run typecheck`,
# delete this script with the workaround (see the issue's next step).
#
# Usage: check-typecheck-parity.sh
set -euo pipefail

extract() {
    grep -oE 'tsc -p [a-zA-Z0-9@._/-]+ --noEmit' "$1" | sed 's/^tsc -p //; s/ --noEmit$//'
}

vite_list=$(extract vite.config.ts)
docker_list=$(extract deploy/Dockerfile)

if [ -z "$vite_list" ]; then
    echo "check-typecheck-parity: no tsc projects found in vite.config.ts" >&2
    exit 1
fi

if [ "$vite_list" = "$docker_list" ]; then
    exit 0
fi

echo "check-typecheck-parity: the Dockerfile typecheck chain and the vite typecheck task disagree" >&2
echo "  vite.config.ts typecheck task:" >&2
echo "$vite_list" | sed 's/^/    /' >&2
echo "  deploy/Dockerfile chain:" >&2
echo "$docker_list" | sed 's/^/    /' >&2
echo "  keep the two in sync until vp run is safe in Linux containers (issue-20261012_1605)" >&2
exit 1
