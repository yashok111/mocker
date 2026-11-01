#!/usr/bin/env bash
# race-shard.sh — run one shard of `make test` (the -race suite), so CI can
# spread the suite over several runners. Usage, from the repository root:
#
#   scripts/race-shard.sh backendmodel:I/N   # the I-th of N slices of internal/backendmodel's tests
#   scripts/race-shard.sh mcp                # internal/mcp
#   scripts/race-shard.sh rest               # every other package under ./cmd ./internal
#
# Why shards (measured on GitHub's 4-core/16 GB runner, 2026-11-01): one job
# running the whole suite at -p 4 inside a 10G scope was OOM-killed after
# ~2.5 min — internal/backendmodel alone peaks at 8.2 GB under -race (two
# admission tests seed 2 × 194 MiB rows through the Store27 migration) while
# internal/mcp peaks at 4.9 GB — and internal/backendmodel alone runs ~29 min
# of tests, past the job's 30-minute timeout by itself. Alone, mcp took 485 s
# and the rest 14 min; backendmodel is cut into N slices of its top-level
# tests, round-robin over their sorted names, so a new test lands in a slice
# without anyone editing a list.
#
# The flags stay the Makefile's: this only chooses TEST_SUITE and, for a
# backendmodel slice, TEST_RACE_RUN. A new package lands in `rest` on its own.
set -euo pipefail

shard="${1:?usage: race-shard.sh backendmodel:I/N | mcp | rest}"
run=.
case "$shard" in
backendmodel:*/*)
	slice="${shard#backendmodel:}"
	i="${slice%/*}" n="${slice#*/}"
	[[ "$i" =~ ^[1-9][0-9]*$ && "$n" =~ ^[1-9][0-9]*$ && "$i" -le "$n" ]] || {
		echo "race-shard: bad slice '$slice' (want I/N with 1 <= I <= N)" >&2
		exit 2
	}
	suite=./internal/backendmodel
	names="$(grep -hoE '^func Test[A-Za-z0-9_]*\(t \*testing\.T\)' internal/backendmodel/*_test.go |
		sed -E 's/^func (Test[A-Za-z0-9_]*).*/\1/' | sort -u |
		awk -v i="$i" -v n="$n" '(NR - 1) % n == i - 1' | paste -sd'|')"
	[ -n "$names" ] || {
		echo "race-shard: slice $slice selects no tests" >&2
		exit 1
	}
	run="^($names)\$"
	;;
mcp)
	suite=./internal/mcp
	;;
rest)
	suite="$(go list ./cmd/... ./internal/... | grep -vxE '.*/internal/(backendmodel|mcp)' | paste -sd' ')"
	;;
*)
	echo "race-shard: unknown shard '$shard'" >&2
	exit 2
	;;
esac

exec make test CAP= TEST_CAP= TEST_P=4 TEST_SUITE="$suite" TEST_RACE_RUN="$run"
