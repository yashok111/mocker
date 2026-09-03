#!/usr/bin/env bash
# migration-check.sh — P6a's A17 (decisions.md mocker-p6a-sse), the one
# acceptance clause that runs OUTSIDE the compose stack: a data directory
# written by a binary built at the slice's PARENT commit, holding at least one
# traffic row and PRAGMA user_version 3, is opened by a binary built from the
# CURRENT tree, and every traffic row is still there with its original id, in
# the same order, with user_version now 4.
#
# Why a script and not a paragraph: three consecutive gate rounds turned a
# shell block inside A17 into a source of defects of its own — a binary path
# make build does not write, an environment the binary needs to start at
# all, a stash that cannot pop on a clean tree, an in-tree checkout that
# left a detached HEAD. §4 states the observation and this file is the
# recipe, where a review reads it as code.
#
# Two requirements that are not history. (1) Nothing is checked out into the
# operator's working tree: the parent commit is built from a `git worktree`
# under a scratch directory, the current tree in place, and BOTH binaries go
# to scratch paths — Makefile's BINARY (bin/mocker) is one fixed path and is
# never written here. (2) The operator's branch and any stash are left
# exactly as found, on every exit path — this script runs no checkout, no
# stash, and its trap removes the worktree and kills both servers whether
# the comparison passed or not.
#
# make smoke starts from an empty volume, so it can only ever prove that ids
# do not repeat among rows the run created itself; a migration that
# recreated `traffic` EMPTY would pass every clause there. This is the check
# for that.
#
# Usage: scripts/migration-check.sh [parent-ref]
# The default parent is the commit BEFORE the one that added the NEWEST
# migration file — HEAD while that migration is still uncommitted, that
# commit's parent once it is — so the script keeps comparing the previous
# schema against the current one after a slice lands rather than a schema
# against itself. The expected versions are read off the migration file
# names, never hard-coded. Run from the repository root, with Go on PATH.
# Prints PASS/FAIL lines and exits non-zero on any FAIL.

set -euo pipefail

REPO=$(git rev-parse --show-toplevel)
cd "$REPO"

NEWEST_MIGRATION=$(find internal/store/migrations -name "*.sql" | sort | tail -n1)
HEAD_VERSION=$((10#$(basename "$NEWEST_MIGRATION" | cut -c1-4)))
PARENT_VERSION=$((HEAD_VERSION - 1))
if [[ -n "${1:-}" ]]; then
	PARENT_REF=$1
else
	added=$(git log -1 --format=%H -- "$NEWEST_MIGRATION")
	if [[ -n "$added" ]]; then
		PARENT_REF="${added}^"
	else
		PARENT_REF=HEAD
	fi
fi

SCRATCH=$(mktemp -d)
WORKTREE="$SCRATCH/parent"
DATA_DIR="$SCRATCH/data"
PARENT_BIN="$SCRATCH/mocker-parent"
HEAD_BIN="$SCRATCH/mocker-head"
PORT=${MIGRATION_CHECK_PORT:-18099}
ADMIN_HOST="mocker.local"
BASE_DOMAIN="mock.local"
PASSWORD="migration-check-$$"
BODY=$(mktemp)
COOKIES=$(mktemp)
SERVER_PID=""
fail_count=0

cleanup() {
	if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
	fi
	git worktree remove --force "$WORKTREE" >/dev/null 2>&1 || true
	rm -rf "$SCRATCH" "$BODY" "$COOKIES"
}
trap cleanup EXIT

pass() { echo "PASS  $*"; }
fail() {
	echo "FAIL  $*"
	fail_count=$((fail_count + 1))
}

echo "== building the parent binary from ${PARENT_REF} in a scratch worktree =="
git worktree add --detach "$WORKTREE" "$PARENT_REF" >/dev/null
(cd "$WORKTREE" && go build -o "$PARENT_BIN" ./cmd/mocker)
echo "== building the current tree =="
go build -o "$HEAD_BIN" ./cmd/mocker

HASH=$("$HEAD_BIN" hash-password "$PASSWORD")
mkdir -p "$DATA_DIR"

# start_server runs one binary over the shared data directory in the
# background and waits for /healthz. The environment is the minimum
# config.Load accepts (the same names .env.example documents), MOCKER_DEV=1
# because the cookie has to work over plain http on localhost.
start_server() {
	local bin=$1 log=$2
	MOCKER_ADDR="127.0.0.1:${PORT}" \
		MOCKER_ADMIN_HOST="$ADMIN_HOST" \
		MOCKER_BASE_DOMAIN="$BASE_DOMAIN" \
		MOCKER_AUTH_MODE=shared-password \
		MOCKER_SHARED_PASSWORD_HASH="$HASH" \
		MOCKER_DATA_DIR="$DATA_DIR" \
		MOCKER_DEV=1 \
		MOCKER_LOG_LEVEL=info \
		"$bin" >"$log" 2>&1 &
	SERVER_PID=$!
	local tries=0
	until curl -s -o /dev/null -H "Host: ${ADMIN_HOST}" "http://127.0.0.1:${PORT}/healthz"; do
		tries=$((tries + 1))
		if ((tries >= 30)); then
			echo "FAIL  $(basename "$bin") never came up on :${PORT}; log:"
			cat "$log"
			exit 1
		fi
		sleep 0.5
	done
}

stop_server() {
	kill "$SERVER_PID"
	wait "$SERVER_PID" 2>/dev/null || true
	SERVER_PID=""
}

admin() {
	local method=$1 path=$2 data=${3:-}
	shift $((($# < 3) ? $# : 3))
	if [[ -n "$data" ]]; then
		curl -s -c "$COOKIES" -b "$COOKIES" -o "$BODY" -w '%{http_code}' -X "$method" \
			-H "Host: ${ADMIN_HOST}" -H 'Content-Type: application/json' -H "Origin: http://${ADMIN_HOST}" \
			"$@" --data "$data" "http://127.0.0.1:${PORT}${path}"
	else
		# Content-Type even without a body: the admin plane answers 415 to a
		# bodyless DELETE that lacks it.
		curl -s -c "$COOKIES" -b "$COOKIES" -o "$BODY" -w '%{http_code}' -X "$method" \
			-H "Host: ${ADMIN_HOST}" -H 'Content-Type: application/json' -H "Origin: http://${ADMIN_HOST}" \
			"$@" "http://127.0.0.1:${PORT}${path}"
	fi
}

user_version() {
	sqlite3 "$DATA_DIR/mocker.db" 'PRAGMA user_version;'
}

echo "== parent: creating a workspace and recording traffic =="
start_server "$PARENT_BIN" "$SCRATCH/parent.log"
status=$(admin POST /api/auth/login "{\"password\":\"${PASSWORD}\",\"name\":\"migration\"}")
[[ "$status" == "200" ]] || {
	echo "FAIL  parent login: status ${status}: $(cat "$BODY")"
	exit 1
}
csrf=$(jq -r '.csrfToken' "$BODY")
status=$(admin POST /api/workspaces '{"name":"migration"}' -H "X-CSRF-Token: ${csrf}")
[[ "$status" == "201" ]] || {
	echo "FAIL  parent create workspace: status ${status}: $(cat "$BODY")"
	exit 1
}
ws_id=$(jq -r '.id' "$BODY")
slug=$(jq -r '.slug' "$BODY")
# Five mock-plane requests, each recorded as a traffic row (a 404 on a
# workspace with no spec is still a recorded request).
for i in 1 2 3 4 5; do
	curl -s -o /dev/null -H "Host: ${slug}.${BASE_DOMAIN}" "http://127.0.0.1:${PORT}/migration/${i}"
done
# The recorder flushes on its 500 ms tick; the DELETE below is not used —
# a clear would remove the rows — so wait one tick and read.
sleep 1.5
status=$(admin GET "/api/workspaces/${ws_id}/traffic?limit=100")
[[ "$status" == "200" ]] || {
	echo "FAIL  parent list traffic: status ${status}: $(cat "$BODY")"
	exit 1
}
parent_rows=$(jq -c '[.rows[] | {id, path}]' "$BODY")
parent_count=$(jq '.rows | length' "$BODY")
if ((parent_count < 1)); then
	fail "the parent-commit run left NO traffic row behind — an empty comparison would pass against an empty table"
	exit 1
fi
pass "parent run recorded ${parent_count} traffic row(s): ${parent_rows}"
# P6b (A4): a custom endpoint the parent wrote must come back under the
# current binary as kind "http" with no stream — the 0005 rebuild carried
# it across with its DEFAULT.
status=$(admin POST "/api/workspaces/${ws_id}/endpoints" '{"method":"GET","path":"/legacy","status":200,"body":{"ok":true},"mediaType":"application/json"}' -H "X-CSRF-Token: ${csrf}")
[[ "$status" == "201" ]] || {
	echo "FAIL  parent create endpoint: status ${status}: $(cat "$BODY")"
	exit 1
}
parent_ep_id=$(jq -r '.id' "$BODY")
stop_server

pv=$(user_version)
if [[ "$pv" == "$PARENT_VERSION" ]]; then
	pass "PRAGMA user_version after the parent run = ${PARENT_VERSION}"
else
	fail "PRAGMA user_version after the parent run = ${pv}, want ${PARENT_VERSION} (is ${PARENT_REF} really the commit before $(basename "$NEWEST_MIGRATION")?)"
fi

echo "== head: opening the same data directory =="
start_server "$HEAD_BIN" "$SCRATCH/head.log"
if grep -q "\"version\":${HEAD_VERSION}" "$SCRATCH/head.log" || grep -q "version=${HEAD_VERSION}" "$SCRATCH/head.log"; then
	pass "head binary logged migration ${HEAD_VERSION} applied"
else
	fail "head binary logged no migration ${HEAD_VERSION}: $(grep -i migration "$SCRATCH/head.log" || echo '(no migration lines)')"
fi
status=$(admin POST /api/auth/login "{\"password\":\"${PASSWORD}\",\"name\":\"migration\"}")
[[ "$status" == "200" ]] || {
	echo "FAIL  head login: status ${status}: $(cat "$BODY")"
	exit 1
}
csrf=$(jq -r '.csrfToken' "$BODY")
status=$(admin GET "/api/workspaces/${ws_id}/traffic?limit=100")
[[ "$status" == "200" ]] || {
	echo "FAIL  head list traffic: status ${status}: $(cat "$BODY")"
	exit 1
}
head_rows=$(jq -c '[.rows[] | {id, path}]' "$BODY")
if [[ "$head_rows" == "$parent_rows" ]]; then
	pass "every traffic row survived the rebuild with its original id, in the same order"
else
	fail "traffic rows differ across the migration: parent ${parent_rows} vs head ${head_rows}"
fi

# And the point of the migration: a new row after a clear lands ABOVE the
# highest id the parent run ever used.
max_id=$(jq '[.rows[].id] | max' "$BODY")
status=$(admin DELETE "/api/workspaces/${ws_id}/traffic" '' -H "X-CSRF-Token: ${csrf}")
[[ "$status" == "200" ]] || fail "head clear traffic: status ${status}: $(cat "$BODY")"
curl -s -o /dev/null -H "Host: ${slug}.${BASE_DOMAIN}" "http://127.0.0.1:${PORT}/after-clear"
sleep 1.5
status=$(admin GET "/api/workspaces/${ws_id}/traffic?limit=100")
new_id=$(jq '[.rows[].id] | max' "$BODY")
if [[ "$status" == "200" && "$new_id" != "null" ]] && ((new_id > max_id)); then
	pass "the first row after a clear carries id ${new_id} > ${max_id}, the parent run's highest — ids are not reissued"
else
	fail "after a clear the next row's id is ${new_id}, want > ${max_id} (status ${status})"
fi

# P6b (A4): the endpoint the parent wrote, read back under the current binary.
status=$(admin GET "/api/workspaces/${ws_id}/endpoints")
head_ep_kind=$(jq -r --argjson id "$parent_ep_id" '.endpoints[] | select(.id == $id) | .kind // "MISSING"' "$BODY")
head_ep_stream=$(jq -r --argjson id "$parent_ep_id" '.endpoints[] | select(.id == $id) | has("stream")' "$BODY")
if [[ "$status" == "200" && "$head_ep_kind" == "http" && "$head_ep_stream" == "false" ]]; then
	pass "the parent's custom endpoint ${parent_ep_id} reports kind http and no stream under the current binary"
else
	fail "the parent's custom endpoint ${parent_ep_id}: status ${status} kind '${head_ep_kind}' has stream ${head_ep_stream}, want 200 / http / false"
fi

stop_server

hv=$(user_version)
if [[ "$hv" == "$HEAD_VERSION" ]]; then
	pass "PRAGMA user_version after the head run = ${HEAD_VERSION}"
else
	fail "PRAGMA user_version after the head run = ${hv}, want ${HEAD_VERSION}"
fi

if ((fail_count > 0)); then
	echo "== ${fail_count} check(s) FAILED =="
	exit 1
fi
echo "== migration check PASSED =="
