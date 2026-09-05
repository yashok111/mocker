#!/usr/bin/env bash
# P0 acceptance check, run without DNS or TLS (DESIGN §19/§16): the dispatcher
# decides on the Host header alone, so a Host header is the whole test.
#
# Brings the compose stack up against a throwaway password and a fresh named
# volume, logs into the admin API to create workspace "alex" (there is no
# seed data — a workspace has to exist before its health endpoint can answer
# 200), then runs the three Host-header checks from README.md, and tears
# everything down again including the volume so reruns are idempotent.
#
# Requires: docker compose, curl, jq, python3 (>= 3.10, standard library only — scripts/wsclient.py, the P6d WebSocket client).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Stop compose from auto-loading .env a SECOND time as the project env file.
# The service already reads it via env_file with format: raw; the project-file
# pass additionally interpolates the file against itself and prints one
# warning per `$` in the argon2id hash. Harmless (docker-compose.yml
# substitutes no MOCKER_* variable) but it reads exactly like the real
# hash-mangling failure, so keep this run's output honest. Set here too, not
# only in the Makefile, since this script is also run directly.
export COMPOSE_ENV_FILES=/dev/null

command -v jq >/dev/null || {
	echo "smoke.sh requires jq (see CLAUDE.md tooling list)" >&2
	exit 1
}

BASE_URL="http://127.0.0.1:8080"
WORKSPACE_HOST_BASE="mock.local"
ADMIN_HOST="mocker.local"
MISSING_HOST="nope.${WORKSPACE_HOST_BASE}"
SMOKE_PASSWORD="mocker-smoke-$$"

ENV_FILE=.env
ENV_BACKUP=""
COOKIE_JAR=$(mktemp)
BODY_FILE=$(mktemp)
# §E observation 3 (pause): a background curl needs its OWN files, never
# COOKIE_JAR/BODY_FILE -- those are written by http_json/check/check_header
# and read by every jq call that follows one of them in the FOREGROUND, and
# a background writer racing that read is exactly the corrupted-body class of
# bug the http_json comment above already warns about for a different reason.
# PAUSE_DONE_FILE must start ABSENT: its mere existence is the "the held
# request finished" signal the foreground polls for, so the name is reserved
# by mktemp (uniqueness) and then rm -f'd right away.
PAUSE_BODY_FILE=$(mktemp)
PAUSE_STATUS_FILE=$(mktemp)
PAUSE_EXIT_FILE=$(mktemp)
PAUSE_DONE_FILE=$(mktemp)
rm -f "$PAUSE_DONE_FILE"
# A6's own response-header capture, removed in cleanup with the rest.
HDR_FILE_A6=$(mktemp)
fail_count=0
# p2c_checks counts every assertion the P2c block below makes, independent
# of fail_count (which only counts FAILURES): make smoke's only failure
# signal is fail_count, and this file already has a silent-skip precedent
# (P1b's own guard around line 386) where a block that never ran prints no
# FAIL at all. Asserting p2c_checks against its known total at the END of
# that block, OUTSIDE any guard the block itself introduces, is what turns
# "the P2c block silently didn't run" into a reported failure instead of an
# indistinguishable clean pass.
p2c_checks=0
# p2d_checks is p2c_checks' own twin for the P2d block below, same
# reasoning: a silent skip or a mid-block short-circuit shows up as a FAIL
# against p2d_want_checks instead of vanishing into a clean run.
p2d_checks=0

cleanup() {
	# The exit status has to be read FIRST, before any command in here
	# overwrites $?. On a failure the container's own logs are the only place
	# a crash, a panic or a refused connection is visible at all: curl reports
	# exit 52 ("empty reply") for a server that died mid-request and says
	# nothing about why, and the teardown below then destroys the evidence.
	local status=$?
	if ((status != 0)); then
		echo "== container logs (last 80 lines), because the smoke failed =="
		docker compose logs --tail 80 2>&1 || true
	fi
	echo "== tearing down =="
	docker compose down -v || true
	if [[ -n "$ENV_BACKUP" ]]; then
		mv -f "$ENV_BACKUP" "$ENV_FILE"
	else
		rm -f "$ENV_FILE"
	fi
	rm -f "$COOKIE_JAR" "$BODY_FILE" "$PAUSE_BODY_FILE" "$PAUSE_STATUS_FILE" "$PAUSE_EXIT_FILE" "$PAUSE_DONE_FILE" "$HDR_FILE_A6"
}
trap cleanup EXIT

# A prior interrupted run may have left the named volume behind with "alex"
# already taken; start from a clean slate so this script is safe to rerun.
docker compose down -v >/dev/null 2>&1 || true

# env_file: .env is required by docker-compose.yml. Build our own from the
# example so the base domain / admin host / MOCKER_DEV match what this script
# assumes, and never touch a developer's real .env: back it up, restore it on
# exit (the trap above).
if [[ -f "$ENV_FILE" ]]; then
	ENV_BACKUP=$(mktemp)
	cp "$ENV_FILE" "$ENV_BACKUP"
fi
cp .env.example "$ENV_FILE"

# P2c retention preamble: rewrite MOCKER_CHECKPOINT_RETENTION to a
# NON-DEFAULT value before the stack ever comes up. Against .env.example's
# default of 20, a server that ignored the variable entirely would still
# pass every retention assertion below -- nothing in this whole script
# writes 20 machine-made checkpoints to one workspace. 3 is deliberately
# small: it forces real pruning while observation 5's own five-rollback
# sequence still fits the "N+1 rows for the ONE rollback whose target falls
# outside the newest N" carve-out C7 grants. A FOURTH `docker compose up` IS
# added later in this file, for P2d's observation 13 alone (its own stack,
# after the path-mode block) -- everything else this script does, P2d's own
# twelve other observations included, still runs under this ONE rewrite,
# because that fourth `up` exists only to change MOCKER_CHECKPOINT_DEBOUNCE,
# not retention.
#
# P2d rides along here rather than earning a rewrite of its own:
# MOCKER_CHECKPOINT_DEBOUNCE=86400 is a NUMBER, not "a large value" -- it
# must outlast this whole run's wall clock (so every workspace below takes
# at most ONE auto checkpoint, on its first labelled call, and never a
# second) and it must differ from .env.example's own default of 300, so
# observation 13's later contrast against a short window means something.
grep -v '^MOCKER_CHECKPOINT_RETENTION=' "$ENV_FILE" >"${ENV_FILE}.tmp"
mv "${ENV_FILE}.tmp" "$ENV_FILE"
printf 'MOCKER_CHECKPOINT_RETENTION=3\n' >>"$ENV_FILE"
grep -v '^MOCKER_CHECKPOINT_DEBOUNCE=' "$ENV_FILE" >"${ENV_FILE}.tmp"
mv "${ENV_FILE}.tmp" "$ENV_FILE"
printf 'MOCKER_CHECKPOINT_DEBOUNCE=86400\n' >>"$ENV_FILE"

# P6a (decisions.md mocker-p6a-sse A8): the three stream variables at
# NON-DEFAULT values -- 7, 3 and 4 are not round, not the defaults (60, 15,
# 5), and not what a developer inventing a constant would pick, which is the
# whole of what one run can say about the config seam. Written here, into
# the .env the stack reads, never exported in the shell: docker-compose.yml
# hands the service this file and nothing else. The P6a block below reads
# the effective values back out of the container rather than trusting these
# lines.
for p6a_var in MOCKER_STREAM_SESSION_RECHECK MOCKER_STREAM_PING MOCKER_STREAM_FRAME_TIMEOUT; do
	grep -v "^${p6a_var}=" "$ENV_FILE" >"${ENV_FILE}.tmp"
	mv "${ENV_FILE}.tmp" "$ENV_FILE"
done
printf 'MOCKER_STREAM_SESSION_RECHECK=7\nMOCKER_STREAM_PING=3\nMOCKER_STREAM_FRAME_TIMEOUT=4\n' >>"$ENV_FILE"
# P6b (decisions.md mocker-p6b-sse-mock A3): the mock plane's three, again
# at non-default values — 3 connections per workspace (default 200), a
# 20-second lifetime (default 900) and first-frame recording (default off).
for p6b_var in MOCKER_STREAM_MAX_CONNS MOCKER_STREAM_MAX_LIFETIME MOCKER_STREAM_TRAFFIC_FRAMES; do
	grep -v "^${p6b_var}=" "$ENV_FILE" >"${ENV_FILE}.tmp"
	mv "${ENV_FILE}.tmp" "$ENV_FILE"
done
printf 'MOCKER_STREAM_MAX_CONNS=3\nMOCKER_STREAM_MAX_LIFETIME=20\nMOCKER_STREAM_TRAFFIC_FRAMES=first\n' >>"$ENV_FILE"
# P6d (decisions.md mocker-p6d-websocket A3): WebSocket's three, again at
# non-default values — a 4kb inbound frame cap (default 64kb), a 1kb reply
# budget (default 256kb) so drops are observable, and an origin allowlist
# (default any).
for p6d_var in MOCKER_STREAM_MAX_FRAME MOCKER_STREAM_SEND_BUDGET MOCKER_STREAM_ORIGINS; do
	grep -v "^${p6d_var}=" "$ENV_FILE" >"${ENV_FILE}.tmp"
	mv "${ENV_FILE}.tmp" "$ENV_FILE"
done
printf 'MOCKER_STREAM_MAX_FRAME=4kb\nMOCKER_STREAM_SEND_BUDGET=1kb\nMOCKER_STREAM_ORIGINS=https://allowed.example\n' >>"$ENV_FILE"
# A6 (decisions.md mocker-a6-assets A11): the two asset caps at non-default
# values — 4kb per file (default 8mb) and 6kb per workspace (default 64mb) —
# small enough that the A6 block at the end can hit both ceilings with
# bodies curl can carry inline.
for a6_var in MOCKER_MAX_ASSET MOCKER_MAX_ASSETS_TOTAL; do
	grep -v "^${a6_var}=" "$ENV_FILE" >"${ENV_FILE}.tmp"
	mv "${ENV_FILE}.tmp" "$ENV_FILE"
done
printf 'MOCKER_MAX_ASSET=4kb\nMOCKER_MAX_ASSETS_TOTAL=6kb\n' >>"$ENV_FILE"

# SMOKE_PREBUILT=1 skips the build and uses a mocker:local that is already
# loaded — CI builds it once per job through buildx with a cross-run layer
# cache (.github/workflows/ci.yml), which `docker compose build` on the
# default builder cannot read; the image must then really be there, since a
# missing one would otherwise be pulled from nowhere and fail later with a
# less telling error.
if [[ "${SMOKE_PREBUILT:-}" == 1 ]]; then
	echo "== using the prebuilt mocker:local image =="
	docker image inspect mocker:local >/dev/null 2>&1 || {
		echo "SMOKE_PREBUILT=1 but no mocker:local image is loaded" >&2
		exit 1
	}
else
	echo "== building the image =="
	docker compose build
fi

echo "== generating a throwaway password hash =="
HASH=$(docker compose run --rm -T mocker hash-password "$SMOKE_PASSWORD")
grep -v '^MOCKER_SHARED_PASSWORD_HASH=' "$ENV_FILE" >"${ENV_FILE}.tmp"
mv "${ENV_FILE}.tmp" "$ENV_FILE"
printf 'MOCKER_SHARED_PASSWORD_HASH=%s\n' "$HASH" >>"$ENV_FILE"

echo "== bringing up the compose stack =="
docker compose up -d

wait_for_port() {
	local tries=0
	until curl -s -o /dev/null -H "Host: ${ADMIN_HOST}" "${BASE_URL}/healthz"; do
		tries=$((tries + 1))
		if ((tries >= 30)); then
			echo "FAIL  server never came up on ${BASE_URL} after 30s"
			docker compose logs mocker || true
			exit 1
		fi
		sleep 1
	done
}

echo "== waiting for ${BASE_URL} =="
wait_for_port

# A5: docker-compose.yml's HEALTHCHECK is the binary's own `healthcheck`
# subcommand (cmd/mocker/healthcheck.go) — distroless has no curl to run
# one with. Two observations, from two sides: the subcommand exits 0 when
# exec'd in the running container (the same environment, the same
# MOCKER_ADMIN_HOST, a real dial of the real listener), and compose's own
# view of the container reaches "healthy" — the interval is 10 s, so this
# waits up to 60 s rather than asserting the state at once. A green
# `go test` on healthcheck_test.go says the subcommand works against an
# httptest server; only this says the compose file actually wires it.
echo "== container healthcheck (mocker healthcheck) =="
if docker compose exec -T mocker /mocker healthcheck; then
	echo "ok    mocker healthcheck exits 0 inside the container"
else
	echo "FAIL  mocker healthcheck exited non-zero inside the container"
	fail_count=$((fail_count + 1))
fi
health_tries=0
health_state=""
until [[ "$health_state" == "healthy" ]]; do
	health_state=$(docker inspect --format '{{.State.Health.Status}}' "$(docker compose ps -q mocker)" 2>/dev/null || true)
	health_tries=$((health_tries + 1))
	if ((health_tries >= 60)); then
		break
	fi
	[[ "$health_state" == "healthy" ]] || sleep 1
done
if [[ "$health_state" == "healthy" ]]; then
	echo "ok    compose reports the container healthy (after ${health_tries} s)"
else
	echo "FAIL  compose never reported the container healthy in 60 s (last state: '${health_state}')"
	fail_count=$((fail_count + 1))
fi

# curl's own status/body split, reused for both the setup calls and the
# checks below: -w prints the status code after the body, then the last line
# is peeled off with a trailing newline as the separator.
http_json() {
	local method=$1 host=$2 path=$3 data=${4:-}
	# data is OPTIONAL ("${4:-}" above), so a bare 3-arg call (a GET with no
	# body and no extra curl flags — the auth-preset preview is the first
	# caller shaped like that) has nothing in $4 to shift away: a flat
	# `shift 4` on 3 positional params fails, and `|| true` used to hide
	# that failure while leaving $1.."$3" (method/host/path) still sitting
	# in "$@" — spliced into the curl call below as three EXTRA bogus URLs
	# curl fetches (and -w-terminates) before the real one, corrupting both
	# the captured body (garbage "000" statuses prepended) and anything
	# that parses it. Shifting min($#, 4) always drains exactly what was
	# actually supplied, so "$@" holds only genuine trailing flags either way.
	shift $((($# < 4) ? $# : 4))
	local out status
	if [[ -n "$data" ]]; then
		out=$(curl -s -c "$COOKIE_JAR" -b "$COOKIE_JAR" -H "Host: ${host}" \
			-H 'Content-Type: application/json' -H "Origin: http://${host}" \
			-w '\n%{http_code}' -X "$method" "$@" --data "$data" "${BASE_URL}${path}")
	else
		out=$(curl -s -c "$COOKIE_JAR" -b "$COOKIE_JAR" -H "Host: ${host}" \
			-H "Origin: http://${host}" \
			-w '\n%{http_code}' -X "$method" "$@" "${BASE_URL}${path}")
	fi
	status=${out##*$'\n'}
	printf '%s' "${out%$'\n'"$status"}" >"$BODY_FILE"
	printf '%s' "$status"
}

# A3 (mocker-a3-cas): editVersion is REQUIRED on every one of the five
# compare-and-swap routes (D10), so a write that used to fire on its own
# now needs the value its OWN preceding read reported first, or it is
# refused outright. These three helpers are that read, one per addressed
# object shape, reused everywhere below rather than hand-rolled at each of
# this script's 33+1 write sites (D13's own counted number) -- csrf, ws_id
# and BODY_FILE are the same shared/global convention every other helper in
# this file already leans on.
#
# op_edit_version GETs .../operations/{opkey} in workspace wid and prints
# the editVersion to send on the next PUT: 0 (D3/D7's "I expect no row")
# when the GET 404s because no override has ever been made yet, the row's
# own token otherwise. Leaves the read's body in $BODY_FILE like http_json
# always does, so a caller that also wants the current document (not just
# the number) can inspect it immediately after calling this.
op_edit_version() {
	local wid=$1 opkey=$2 status
	status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${wid}/operations/${opkey}" '' -H "X-CSRF-Token: ${csrf}")
	if [[ "$status" == "200" ]]; then
		jq -r '.editVersion' "$BODY_FILE"
	else
		echo 0
	fi
}

# ws_edit_version GETs the workspace itself -- every workspace row already
# exists (D3: 0 is refused on the three tables whose rows are never
# created by the guarded route), so this never has a 404 branch to cover.
ws_edit_version() {
	local wid=$1
	http_json GET "$ADMIN_HOST" "/api/workspaces/${wid}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
	jq -r '.editVersion' "$BODY_FILE"
}

# scenario_edit_version GETs one scenario's own detail route
# (GET .../scenarios/{sid}), the read PUT .../scenarios/{sid} (rename)
# actually precedes on a real screen -- never the list, which this script
# also has open elsewhere for unrelated reasons.
scenario_edit_version() {
	local wid=$1 sid=$2
	http_json GET "$ADMIN_HOST" "/api/workspaces/${wid}/scenarios/${sid}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
	jq -r '.editVersion' "$BODY_FILE"
}

echo "== admin login =="
login_status=$(http_json POST "$ADMIN_HOST" /api/auth/login "{\"password\":\"${SMOKE_PASSWORD}\",\"name\":\"smoke\"}")
if [[ "$login_status" != "200" ]]; then
	echo "FAIL  admin login: want status 200, got ${login_status}: $(cat "$BODY_FILE")"
	exit 1
fi
csrf=$(jq -r '.csrfToken' "$BODY_FILE")

echo "== create workspace 'alex' =="
create_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"alex"}' -H "X-CSRF-Token: ${csrf}")
if [[ "$create_status" != "201" ]]; then
	echo "FAIL  create workspace: want status 201, got ${create_status}: $(cat "$BODY_FILE")"
	exit 1
fi
slug=$(jq -r '.slug' "$BODY_FILE")
ws_id=$(jq -r '.id' "$BODY_FILE")
workspace_host="${slug}.${WORKSPACE_HOST_BASE}"
echo "      workspace slug: ${slug} (id ${ws_id})"

check() {
	local desc=$1 want_status=$2 host=$3 path=$4 want_body_substr=${5:-}
	local status

	status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${host}" "${BASE_URL}${path}")

	if [[ "$status" != "$want_status" ]]; then
		echo "FAIL  ${desc}: want status ${want_status}, got ${status}"
		fail_count=$((fail_count + 1))
		return
	fi

	if [[ -n "$want_body_substr" ]] && ! grep -qF "$want_body_substr" "$BODY_FILE"; then
		echo "FAIL  ${desc}: body missing '${want_body_substr}': $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
		return
	fi

	echo "PASS  ${desc} (status ${status})"
}

# check_header is check()'s sibling for the shape check() cannot see: it
# captures only status and body (-o "$BODY_FILE" -w '%{http_code}'), so a
# Content-Type or Cache-Control assertion needs curl's header stream too.
# -D - writes headers to stdout while -o keeps sending the body to
# $BODY_FILE, and the '\n%{http_code}' suffix is peeled off the same way
# http_json above splits its own status line — reusing that split instead of
# a second temp file this script's teardown would then have to know about.
check_header() {
	local desc=$1 want_status=$2 host=$3 path=$4 header_name=$5 want_header_substr=$6
	local out status headers header_line

	out=$(curl -s -o "$BODY_FILE" -D - -w '\n%{http_code}' -H "Host: ${host}" "${BASE_URL}${path}")
	status=${out##*$'\n'}
	headers=${out%$'\n'"$status"}

	if [[ "$status" != "$want_status" ]]; then
		echo "FAIL  ${desc}: want status ${want_status}, got ${status}"
		fail_count=$((fail_count + 1))
		return
	fi

	header_line=$(grep -i "^${header_name}:" <<<"$headers" | head -n1 | tr -d '\r')
	if [[ "$header_line" != *"$want_header_substr"* ]]; then
		echo "FAIL  ${desc}: header ${header_name} want to contain '${want_header_substr}', got '${header_line}'"
		fail_count=$((fail_count + 1))
		return
	fi

	echo "PASS  ${desc} (status ${status}, ${header_line})"
}

# b64url_decode reverses base64.RawURLEncoding (RFC 4648 §5, no padding,
# '-'/'_' in place of '+'/'/') — exactly the alphabet internal/recipes/jwt.go
# encodes a JWT's header/payload with. `base64 -d` wants standard base64, so
# the alphabet is swapped back and '=' padding is restored to a multiple of 4
# before handing it off; this is the "decode it in the shell" step the P1c
# login check exists to prove, not a length check on the token string.
b64url_decode() {
	local input=$1
	input=${input//-/+}
	input=${input//_//}
	local pad=$(((4 - ${#input} % 4) % 4))
	input+=$(printf '%*s' "$pad" '' | tr ' ' '=')
	base64 -d <<<"$input" 2>/dev/null
}

echo "== P0 acceptance checks (Host header, no DNS/TLS) =="
check "known workspace health" 200 "$workspace_host" /__mocker/health "\"workspace\":\"${slug}\""
check "unknown workspace host" 404 "$MISSING_HOST" /__mocker/health
check "admin liveness" 200 "$ADMIN_HOST" /healthz

# DESIGN §14 screen 4's server-side «Проверить»: mocker dials the workspace's
# own external URL itself, by name, exactly as any real machine in the
# contour would (internal/probe — see this script's own header for why the
# whole run happens on Host headers with no DNS at all). That is precisely
# why this check's PASS condition is "network-error", not "ok": inside this
# container "${workspace_host}" resolves nowhere, so a wired-up probe route
# reports exactly the failure a real, DNS-less contour would — "ok" or a
# non-200 from this route itself would both mean the route is not doing what
# DESIGN §14 asks.
probe_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/probe" '{}' -H "X-CSRF-Token: ${csrf}")
probe_kind=$(jq -r '.kind' "$BODY_FILE")
if [[ "$probe_status" != "200" || "$probe_kind" != "network-error" ]]; then
	echo "FAIL  server-side probe: want status 200 kind network-error (no DNS for ${workspace_host} in this environment), got status ${probe_status} kind '${probe_kind}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  server-side probe reports network-error, as a DNS-less contour should (status 200)"
fi

echo "== P1b: import a spec and confirm the generator answers =="

# A small inline OpenAPI 3.0 document — never the 300KB acceptance corpus
# (internal/testspec), which has no business in a smoke script. One list route (GET /widgets, wrapped
# {items,total,limit,offset}) and its detail route (GET /widgets/{id}),
# sharing one Widget schema via $ref so a list row and its detail card are
# generated from literally the same schema node — mirroring the real spec's
# own list/detail shape (DESIGN §9).
if ! widgets_doc=$(jq -n '{
	openapi: "3.0.3",
	info: {title: "Widgets", version: "1.0.0"},
	paths: {
		"/widgets": {
			get: {
				operationId: "listWidgets",
				parameters: [
					{name: "limit", in: "query", schema: {type: "integer"}},
					{name: "offset", in: "query", schema: {type: "integer"}}
				],
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {
									type: "object",
									properties: {
										items: {type: "array", items: {"$ref": "#/components/schemas/Widget"}},
										total: {type: "integer"},
										limit: {type: "integer"},
										offset: {type: "integer"}
									},
									required: ["items", "total", "limit", "offset"]
								}
							}
						}
					}
				}
			},
			# P1c-2 addition: nothing above needs a POST route, but the when[]
			# body-equality check does — GET/HEAD/DELETE never have their body
			# read at all (mockplane/reqbody.go, captureRequestBody), so an
			# in:"body" condition can only ever be proven against a method
			# that actually carries one.
			post: {
				operationId: "createWidget",
				requestBody: {
					content: {
						"application/json": {
							schema: {
								type: "object",
								properties: {
									name: {type: "string"},
									kind: {type: "string"}
								}
							}
						}
					}
				},
				responses: {
					"201": {
						description: "created",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/Widget"}
							}
						}
					}
				}
			}
		},
		"/widgets/{id}": {
			get: {
				operationId: "getWidget",
				parameters: [
					{name: "id", in: "path", required: true, schema: {type: "integer"}}
				],
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/Widget"}
							}
						}
					}
				}
			}
		},
		"/auth/login": {
			post: {
				operationId: "login",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {
									type: "object",
									properties: {
										token: {type: "string"},
										token_expires_at: {type: "integer"}
									},
									required: ["token", "token_expires_at"]
								}
							}
						}
					}
				}
			}
		},
		# --------------------------------------------------------------
		# P2e (G2): three operations used by NO other assertion in this
		# script -- the P1b/P1c/P2b/P2c/P2d checks above select operations
		# by exact method+path, and nothing anywhere counts how many
		# operations the imported spec declares, so adding these breaks no
		# downstream assertion (F35).
		#
		# NOTE for whoever edits this literal next: it is a bash SINGLE-quoted
		# string end to end, so a straight apostrophe anywhere inside it --
		# including an English possessive -- closes the string early. Every
		# comment in this document, old and new, is phrased to avoid one.
		#
		# /example-media: a 200 whose MEDIA TYPE declares "example" (not the
		# schema) -- C2 first half, the branch gen.go contentExample reads
		# before any schema walk.
		#
		# /example-schema: a 200 whose SCHEMA declares "example" -- C2 second
		# half, the branch leafValue reads at the root of the walk (F30, "two
		# short-circuits, not one"). Both are non-list (F30 own reason:
		# listBody runs before the media-type gate, so a list operation would
		# exercise a different branch than the one C2 targets) and both carry
		# a REAL schema with "properties" -- an operation whose 200 has only
		# an example and no schema has an empty SchemaPtr, Body returns
		# (nil, nil) before a patch ever matters, and C2 would be red against
		# correct code (see G2).
		#
		# /status-variants: a 200 AND a "default" response on the SAME
		# operation, schemas carrying DIFFERENT property names
		# (onlyIn200/onlyInDefault) -- C12 tripwire for "the key that looks
		# unique and is not" (D3(2), Risk 7). classifySelector maps BOTH
		# selectors to http_status 200 (internal/specs/index.go:202-209), so a
		# patched-schema cache keyed by HTTPSTATUS rather than by SELECTOR
		# collides here while one keyed by selector does not.
		# --------------------------------------------------------------
		"/example-media": {
			get: {
				operationId: "getExampleMedia",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {
									type: "object",
									properties: {
										title: {type: "string"}
									},
									required: ["title"]
								},
								example: {title: "MEDIA_TYPE_EXAMPLE_MARKER"}
							}
						}
					}
				}
			}
		},
		"/example-schema": {
			get: {
				operationId: "getExampleSchema",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {
									type: "object",
									properties: {
										headline: {type: "string"}
									},
									required: ["headline"],
									example: {headline: "SCHEMA_EXAMPLE_MARKER"}
								}
							}
						}
					}
				}
			}
		},
		"/status-variants": {
			get: {
				operationId: "getStatusVariants",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {
									type: "object",
									properties: {
										onlyIn200: {type: "string"}
									},
									required: ["onlyIn200"]
								}
							}
						}
					},
					"default": {
						description: "fallback",
						content: {
							"application/json": {
								schema: {
									type: "object",
									properties: {
										onlyInDefault: {type: "string"}
									},
									required: ["onlyInDefault"]
								}
							}
						}
					}
				}
			}
		}
	},
	components: {
		schemas: {
			Widget: {
				type: "object",
				properties: {
					id: {type: "integer", format: "uint"},
					name: {type: "string"},
					status: {type: "string"}
				},
				required: ["id", "name"]
			}
		}
	}
}'); then
	echo "SKIP  P1b checks: could not construct the inline OpenAPI document"
elif ! import_body=$(jq -n --arg name "Widgets" --arg source "upload" --arg document "$widgets_doc" \
	'{name: $name, source: $source, document: $document}'); then
	echo "SKIP  P1b checks: could not wrap the inline document into an import request"
else
	echo "== import the spec (POST /api/specs, document as a JSON string) =="
	import_status=$(http_json POST "$ADMIN_HOST" /api/specs "$import_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$import_status" != "201" ]]; then
		echo "FAIL  import spec: want status 201, got ${import_status}: $(cat "$BODY_FILE")"
		exit 1
	fi
	spec_id=$(jq -r '.id' "$BODY_FILE")
	echo "      imported spec id: ${spec_id}"

	echo "== attach the spec to workspace 'alex' (PATCH /api/workspaces/{id}) =="
	# A3: editVersion is REQUIRED on PATCH /api/workspaces/{id} (D10) -- read
	# the workspace's own current token first, exactly as a screen's own
	# preceding GET would.
	alex_ev=$(ws_edit_version "$ws_id")
	attach_status=$(http_json PATCH "$ADMIN_HOST" "/api/workspaces/${ws_id}" \
		"{\"specId\":${spec_id},\"editVersion\":${alex_ev}}" -H "X-CSRF-Token: ${csrf}")
	if [[ "$attach_status" != "200" ]]; then
		echo "FAIL  attach spec to workspace: want status 200, got ${attach_status}: $(cat "$BODY_FILE")"
		exit 1
	fi

	health_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/__mocker/health")
	health_spec=$(jq -r '.spec' "$BODY_FILE")
	health_revision=$(jq -r '.revision' "$BODY_FILE")
	if [[ "$health_status" != "200" || "$health_spec" != "$spec_id" || "$health_revision" -le 1 ]]; then
		echo "FAIL  post-attach health: want status 200, spec ${spec_id}, revision > 1; got status ${health_status}, $(cat "$BODY_FILE")"
		exit 1
	fi
	echo "PASS  spec attached and health reports it (spec ${health_spec}, revision ${health_revision})"

	echo "== P1b acceptance checks (the generator, not 404/501) =="

	# A matched route answering generated data instead of 404/501 is the
	# whole point of this phase, so status, Content-Type, JSON validity and
	# the declared list shape are all checked off ONE response.
	list_out=$(curl -s -o "$BODY_FILE" -w '%{http_code} %{content_type}' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
	list_status=${list_out%% *}
	list_ctype=${list_out#* }
	if [[ "$list_status" != "200" ]]; then
		echo "FAIL  GET /widgets: want status 200, got ${list_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	elif [[ "$list_ctype" != application/json* ]]; then
		echo "FAIL  GET /widgets: want Content-Type application/json, got '${list_ctype}'"
		fail_count=$((fail_count + 1))
	elif ! jq empty "$BODY_FILE" >/dev/null 2>&1; then
		echo "FAIL  GET /widgets: body does not parse as JSON: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	elif ! jq -e 'has("items") and has("total")' "$BODY_FILE" >/dev/null 2>&1; then
		echo "FAIL  GET /widgets: body is not the declared list shape (items/total present): $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  GET /widgets (status 200, application/json, valid JSON, items+total present)"
	fi

	# Determinism is the phase's core promise: the same request twice must
	# answer byte-identical bodies. The cheapest possible end-to-end proof.
	body1=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
	body2=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
	if [[ "$body1" != "$body2" ]]; then
		echo "FAIL  GET /widgets twice: bodies differ, determinism broken"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  GET /widgets twice: byte-identical"
	fi

	# List row == detail card: the half of DESIGN §19's P1 criterion this
	# phase owns. (The other half, "фронт логинится", waits on P1c's auth
	# preset — not asserted here.)
	item_id=$(jq -r '.items[0].id // empty' <<<"$body1")
	if [[ -z "$item_id" ]]; then
		echo "FAIL  GET /widgets: no items in the list body to check a detail route against"
		fail_count=$((fail_count + 1))
	else
		detail_body=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}")
		list_row=$(jq -c --arg id "$item_id" '.items[] | select((.id|tostring) == $id)' <<<"$body1")
		row_name=$(jq -r '.name' <<<"$list_row")
		row_status=$(jq -r '.status' <<<"$list_row")
		detail_name=$(jq -r '.name' <<<"$detail_body" 2>/dev/null)
		detail_status=$(jq -r '.status' <<<"$detail_body" 2>/dev/null)
		if [[ -z "$list_row" || "$row_name" != "$detail_name" || "$row_status" != "$detail_status" ]]; then
			echo "FAIL  GET /widgets/${item_id}: detail fields differ from its list row (name: '${row_name}' vs '${detail_name}', status: '${row_status}' vs '${detail_status}')"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  GET /widgets/${item_id}: detail card matches its list row"
		fi
	fi

	# HEAD matches GET with an empty body (DESIGN §8) — and, correctly, still
	# carries the SAME Content-Length a GET would (RFC 9110 §9.3.2). That is
	# exactly why this does NOT reuse the -o "$BODY_FILE" + file-size pattern
	# the checks above use: curl treats "Content-Length says N, body
	# delivered 0" as a truncated transfer (CURLE_PARTIAL_FILE, exit 18)
	# unless told with -I/--head that no body is coming. So status and
	# downloaded size are read straight off -w with the body sent to
	# /dev/null instead, and the curl exit code itself is discarded
	# (`|| true`) since THIS non-zero exit is the expected shape of a
	# spec-correct HEAD answer, not a real transfer failure.
	# --head, never `-X HEAD`: the latter sends HEAD but leaves curl expecting
	# the body the Content-Length announces, and it sat there until the
	# connection timed out — 120 s of every smoke, found in CI's timeline.
	head_out=$(curl -s -o /dev/null -w '%{http_code} %{size_download}' --head -H "Host: ${workspace_host}" "${BASE_URL}/widgets") || true
	head_status=${head_out%% *}
	head_size=${head_out#* }
	if [[ "$head_status" != "200" ]]; then
		echo "FAIL  HEAD /widgets: want status 200, got ${head_status}"
		fail_count=$((fail_count + 1))
	elif [[ "$head_size" != "0" ]]; then
		echo "FAIL  HEAD /widgets: want an empty body, got ${head_size} bytes"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  HEAD /widgets: status 200, empty body"
	fi

	echo "== P1c: auth preset, per-operation overrides, and the login route =="
	echo "== (DESIGN §19's P1 criterion: \"фронт логинится\") =="

	# GET .../auth-preset must derive a proposal from the document WITHOUT
	# writing anything (authpreset.Derive's own doc comment) — proven by
	# reading the workspace's revision off /__mocker/health before and after,
	# not by trusting the comment.
	revision_before_preset=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/__mocker/health" | jq -r '.revision')

	preset_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/auth-preset")
	if [[ "$preset_status" != "200" ]]; then
		echo "FAIL  GET auth-preset: want status 200, got ${preset_status}: $(cat "$BODY_FILE")"
		exit 1
	fi
	preset_body=$(cat "$BODY_FILE")

	# The /auth/login operation's "token" property is on a §10 trigger-list
	# path -> proposeJWT; "token_expires_at" matches the "_expires_at" suffix
	# -> proposeExpiry. Both should come back named in Bindings.
	login_bindings=$(jq -c '[.bindings[] | select(.path == "/auth/login")]' <<<"$preset_body")
	login_binding_count=$(jq 'length' <<<"$login_bindings")
	if [[ "$login_binding_count" -lt 1 ]]; then
		echo "FAIL  GET auth-preset: no bindings proposed for POST /auth/login: ${preset_body}"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  GET auth-preset: proposes ${login_binding_count} binding(s) for /auth/login"
	fi

	revision_after_get=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/__mocker/health" | jq -r '.revision')
	if [[ "$revision_after_get" != "$revision_before_preset" ]]; then
		echo "FAIL  GET auth-preset wrote something: revision went ${revision_before_preset} -> ${revision_after_get}"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  GET auth-preset writes nothing (revision stays ${revision_before_preset})"
	fi

	echo "== POST .../auth-preset applies the proposal (PutMany: one revision bump total) =="
	# editVersions is A3's REQUIRED expectation on this route (D5/D10): the
	# SAME map GET .../auth-preset just answered above (preset_body), never
	# re-derived -- passing the whole map is safe even though the submitted
	# bindings only touch a subset (handleApplyAuthPreset only checks the
	# opKeys the bindings actually resolve to, override_handlers.go's own
	# comment on "scoped to touchedKeys").
	preset_edit_versions=$(jq -c '.editVersions' <<<"$preset_body")
	apply_body=$(jq -cn --argjson bindings "$login_bindings" --argjson ev "$preset_edit_versions" '{bindings: $bindings, editVersions: $ev}')
	apply_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/auth-preset" "$apply_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$apply_status" != "200" ]]; then
		echo "FAIL  POST auth-preset: want status 200, got ${apply_status}: $(cat "$BODY_FILE")"
		exit 1
	fi
	applied_count=$(jq -r '.applied' "$BODY_FILE")
	revision_after_apply=$(jq -r '.revision' "$BODY_FILE")
	want_revision_after_apply=$((revision_before_preset + 1))
	if [[ "$applied_count" != "$login_binding_count" || "$revision_after_apply" != "$want_revision_after_apply" ]]; then
		echo "FAIL  POST auth-preset: want applied=${login_binding_count} revision=${want_revision_after_apply}, got applied=${applied_count} revision=${revision_after_apply}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  POST auth-preset applies ${applied_count} binding(s), revision ${revision_before_preset} -> ${revision_after_apply} (+1)"
	fi

	echo "== the mock plane's login route answers a token a frontend can decode =="
	login_body=$(curl -s -X POST -H "Host: ${workspace_host}" "${BASE_URL}/auth/login")
	login_token=$(jq -r '.token // empty' <<<"$login_body")
	login_expires=$(jq -r '.token_expires_at // empty' <<<"$login_body")

	IFS='.' read -r jwt_header jwt_payload jwt_sig <<<"$login_token"
	jwt_dot_count=$(tr -cd '.' <<<"$login_token" | wc -c)
	if [[ -z "$login_token" || "$jwt_dot_count" != "2" || -z "$jwt_header" || -z "$jwt_payload" || -z "$jwt_sig" ]]; then
		echo "FAIL  POST /auth/login: token is not three dot-separated segments: '${login_token}' (body: ${login_body})"
		fail_count=$((fail_count + 1))
	else
		jwt_payload_json=$(b64url_decode "$jwt_payload")
		if ! jwt_sub=$(jq -er '.sub' <<<"$jwt_payload_json" 2>/dev/null); then
			echo "FAIL  POST /auth/login: jwt payload has no 'sub' claim after base64url-decoding: ${jwt_payload_json}"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  POST /auth/login: token is 3 dot-separated segments, payload decodes to JSON with sub=${jwt_sub}"
		fi
	fi

	now_epoch=$(date +%s)
	if [[ ! "$login_expires" =~ ^[0-9]{10}$ ]]; then
		echo "FAIL  POST /auth/login: token_expires_at is not a 10-digit integer (want epoch SECONDS, not milliseconds — DESIGN §10): '${login_expires}' (body: ${login_body})"
		fail_count=$((fail_count + 1))
	elif ((login_expires <= now_epoch)); then
		echo "FAIL  POST /auth/login: token_expires_at ${login_expires} is not in the future (now ${now_epoch})"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  POST /auth/login: token_expires_at ${login_expires} is a 10-digit epoch-seconds timestamp in the future"
	fi

	if [[ -z "$item_id" ]]; then
		echo "SKIP  P1c override checks: no item id from the earlier GET /widgets check"
	else
		# opKey is method + relative path, percent-encoded into one URL
		# segment (overrides.OpKey's own doc comment); jq's @uri matches
		# url.PathEscape byte-for-byte for this input — verified directly
		# against the Go function, not assumed, since PathEscape's own table
		# differs from a generic URI-encoder's in ways that don't happen to
		# matter here (no sub-delims in a method+path string).
		detail_opkey=$(jq -rn --arg s "GET /widgets/{id}" '$s | @uri')
		baseline_detail=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}")
		baseline_name=$(jq -r '.name' <<<"$baseline_detail")

		echo "== PUT a const-recipe override on GET /widgets/{id} =="
		# A3: editVersion is REQUIRED on PUT .../operations/{opKey} (D10) --
		# no override exists yet for detail_opkey, so op_edit_version's own
		# 404 branch answers 0, D3/D7's "I expect no row" expectation.
		const_put_ev=$(op_edit_version "$ws_id" "$detail_opkey")
		const_put_body=$(jq -n --argjson ev "$const_put_ev" '{
			overrideOn: true,
			routeOff: false,
			responses: {
				"200": {mode: "generated", recipes: {name: {kind: "const", value: "OVERRIDDEN"}}}
			},
			editVersion: $ev
		}')
		put_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$const_put_body" -H "X-CSRF-Token: ${csrf}")
		if [[ "$put_status" != "200" ]]; then
			echo "FAIL  PUT override (const recipe): want status 200, got ${put_status}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		else
			overridden_body=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}")
			overridden_name=$(jq -r '.name' <<<"$overridden_body")
			if [[ "$overridden_name" != "OVERRIDDEN" ]]; then
				echo "FAIL  GET /widgets/${item_id} after PUT: want name 'OVERRIDDEN', got '${overridden_name}': ${overridden_body}"
				fail_count=$((fail_count + 1))
			else
				echo "PASS  GET /widgets/${item_id} after PUT: const recipe wins (name=OVERRIDDEN)"
			fi

			echo "== DELETE the override; the generated value comes back =="
			delete_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" '{}' -H "X-CSRF-Token: ${csrf}")
			if [[ "$delete_status" != "200" && "$delete_status" != "204" ]]; then
				echo "FAIL  DELETE override: want status 200 or 204, got ${delete_status}: $(cat "$BODY_FILE")"
				fail_count=$((fail_count + 1))
			else
				reverted_body=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}")
				reverted_name=$(jq -r '.name' <<<"$reverted_body")
				if [[ "$reverted_name" != "$baseline_name" ]]; then
					echo "FAIL  GET /widgets/${item_id} after DELETE: want the generated name '${baseline_name}' back, got '${reverted_name}': ${reverted_body}"
					fail_count=$((fail_count + 1))
				else
					echo "PASS  GET /widgets/${item_id} after DELETE: generated value is back (name=${reverted_name})"
				fi
			fi
		fi
	fi

	echo "== PUT a route_off override on GET /widgets =="
	list_opkey=$(jq -rn --arg s "GET /widgets" '$s | @uri')
	routeoff_ev=$(op_edit_version "$ws_id" "$list_opkey")
	routeoff_put_body=$(jq -n --argjson ev "$routeoff_ev" '{overrideOn: true, routeOff: true, responses: {}, editVersion: $ev}')
	routeoff_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${list_opkey}" "$routeoff_put_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$routeoff_status" != "200" ]]; then
		echo "FAIL  PUT override (route_off): want status 200, got ${routeoff_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		routeoff_get_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
		if [[ "$routeoff_get_status" != "404" ]]; then
			echo "FAIL  GET /widgets after route_off: want status 404, got ${routeoff_get_status}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  GET /widgets after route_off: 404"
		fi
	fi
fi

# --------------------------------------------------------------------------
# P1c slice 2: when-conditions, the live-state layer, traffic, and the two
# "make it from what you just saw" conversions.
#
# Guarded on item_id: everything below needs the spec import above to have
# succeeded, and bash keeps that variable in scope past the block that set it.
#
# NOTHING HERE SLEEPS TO WAIT FOR A RESULT. The traffic writer is asynchronous
# by design (DESIGN §18: batched, never an INSERT per request) and Flush() is a
# Go API a shell script cannot reach, so the poll below retries on a stated
# ceiling: 10 tries, 0.5s apart = 5s, against a 500ms default flush interval.
# A bare `sleep` tuned to one machine is what this replaces.
# --------------------------------------------------------------------------
if [[ -n "${item_id:-}" ]]; then
	echo "== P1c-2: restore GET /widgets (the route_off check above left it disabled) =="
	restore_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${list_opkey}" '{}' -H "X-CSRF-Token: ${csrf}")
	if [[ "$restore_status" != "200" && "$restore_status" != "204" ]]; then
		echo "FAIL  DELETE route_off override: want 200 or 204, got ${restore_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  route_off override removed"
	fi

	# poll_until <jq-selector> — polls from the start of the buffer until a row
	# MATCHING the selector shows up, leaving the last poll body in $BODY_FILE.
	# Ceiling: 10 tries, 0.5s apart = 5s against a 500ms default flush interval.
	#
	# Matching, not merely "some rows exist": the checks above have already
	# filled the buffer, so a helper that returns on the first non-empty poll
	# returns instantly with rows that predate the request under test — which is
	# a green check that proves nothing, and a red one that blames the product.
	poll_until() {
		local selector=$1 tries=0 status hits
		while ((tries < 10)); do
			status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/traffic/poll?since=0" '' -H "X-CSRF-Token: ${csrf}")
			if [[ "$status" == "200" ]]; then
				hits=$(jq "[.rows[] | select(${selector})] | length" "$BODY_FILE")
				if ((hits > 0)); then
					return 0
				fi
			fi
			tries=$((tries + 1))
			sleep 0.5
		done
		return 1
	}

	echo "== when[]: a body-equality condition chooses between two statuses =="
	# GET/HEAD/DELETE never have their body read at all (mockplane/reqbody.go's
	# captureRequestBody bails out on method before it bails out on Content-Type),
	# so this has to run against the POST route, not the GET /widgets/{id} route
	# every other override check in this script uses.
	create_opkey=$(jq -rn --arg s "POST /widgets" '$s | @uri')
	when_ev=$(op_edit_version "$ws_id" "$create_opkey")
	when_put_body=$(jq -n --argjson ev "$when_ev" '{
		overrideOn: true,
		responses: {
			"409": {
				mode: "pinned",
				when: [{in: "body", name: "kind", op: "equals", value: "duplicate"}],
				body: {error: "taken"}
			}
		},
		editVersion: $ev
	}')
	when_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${create_opkey}" "$when_put_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$when_status" != "200" ]]; then
		echo "FAIL  PUT override (when): want status 200, got ${when_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		# Two requests that differ ONLY in the "kind" field — same method, same
		# path, same every other byte of the body — so the status split can
		# only be coming from the when[] evaluator reading that one field.
		match_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X POST -H "Host: ${workspace_host}" \
			-H 'Content-Type: application/json' --data '{"name":"Widget A","kind":"duplicate"}' "${BASE_URL}/widgets")
		if [[ "$match_status" != "409" ]] || ! grep -qF '"error":"taken"' "$BODY_FILE"; then
			echo "FAIL  POST /widgets kind=duplicate: want status 409 with the pinned body, got ${match_status}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  POST /widgets kind=duplicate: the when[] variant wins (409, pinned body)"
		fi
		nomatch_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X POST -H "Host: ${workspace_host}" \
			-H 'Content-Type: application/json' --data '{"name":"Widget B","kind":"new"}' "${BASE_URL}/widgets")
		if [[ "$nomatch_status" != "201" ]]; then
			echo "FAIL  POST /widgets kind=new: want status 201 (the document's own status), got ${nomatch_status}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  POST /widgets kind=new: no condition matches, the document's own status wins (201)"
		fi
		when_delete=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${create_opkey}" '{}' -H "X-CSRF-Token: ${csrf}")
		if [[ "$when_delete" != "200" && "$when_delete" != "204" ]]; then
			echo "FAIL  DELETE when override: want 200 or 204, got ${when_delete}"
			fail_count=$((fail_count + 1))
		fi
	fi

	echo "== live state: force a status, and prove it never bumps the revision =="
	rev_before=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null && jq -r '.revision' "$BODY_FILE")
	force_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "status", status: 503}')
	force_status=$(http_json POST "$workspace_host" /__mocker/state "$force_body")
	if [[ "$force_status" != "200" ]]; then
		echo "FAIL  POST /__mocker/state (force 503): want status 200, got ${force_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		check "GET /widgets under a forced status" 503 "$workspace_host" /widgets
		http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
		rev_after=$(jq -r '.revision' "$BODY_FILE")
		if [[ "$rev_before" != "$rev_after" ]]; then
			echo "FAIL  forcing a status moved the revision ${rev_before} -> ${rev_after}; DESIGN §12: session state never touches it"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  forced status left the revision at ${rev_after}"
		fi
		clear_status=$(http_json DELETE "$workspace_host" /__mocker/state)
		if [[ "$clear_status" != "200" ]]; then
			echo "FAIL  DELETE /__mocker/state: want status 200, got ${clear_status}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		else
			check "GET /widgets after clearing the directive" 200 "$workspace_host" /widgets
		fi
	fi

	echo "== live state: fail the next 2 requests, then answer normally =="
	fail_body=$(jq -n '{target: "*", action: "fail", status: 500, n: 2}')
	failset_status=$(http_json POST "$workspace_host" /__mocker/state "$fail_body")
	if [[ "$failset_status" != "200" ]]; then
		echo "FAIL  POST /__mocker/state (fail n=2): want status 200, got ${failset_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		fail_seq=""
		for _ in 1 2 3; do
			fail_seq+=$(curl -s -o /dev/null -w '%{http_code} ' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
		done
		if [[ "$fail_seq" != "500 500 200 " ]]; then
			echo "FAIL  fail n=2 sequence: want '500 500 200 ', got '${fail_seq}'"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  fail n=2: two forced failures, then the counter is spent (500 500 200)"
		fi
		http_json DELETE "$workspace_host" /__mocker/state >/dev/null
	fi

	echo "== traffic: the requests above are recorded, and the Authorization header is not =="
	# The marker is built in a variable, not written inline: it only has to be
	# unmistakable to the grep below, and a literal bearer value committed into
	# a script is the exact shape every secret scanner flags.
	auth_marker="smoke-auth-marker-$$"
	curl -s -o /dev/null -H "Host: ${workspace_host}" -H "Authorization: Bearer ${auth_marker}" "${BASE_URL}/widgets/${item_id}"
	if ! poll_until '.reqHeaders != null and ((.reqHeaders | keys | map(ascii_downcase) | index("authorization")) != null)'; then
		echo "FAIL  GET /traffic/poll: no row carrying an Authorization header after 10 tries over 5s (flush interval is 500ms)"
		fail_count=$((fail_count + 1))
	else
		if grep -qF "$auth_marker" "$BODY_FILE"; then
			echo "FAIL  traffic poll returned the raw Authorization value — DESIGN §15 redacts secrets BEFORE they are stored"
			fail_count=$((fail_count + 1))
		elif ! grep -qF '[redacted]' "$BODY_FILE"; then
			echo "FAIL  traffic poll recorded an Authorization header without redacting it"
			fail_count=$((fail_count + 1))
		else
			traffic_rows=$(jq '.rows | length' "$BODY_FILE")
			echo "PASS  traffic poll: ${traffic_rows} row(s), Authorization stored as [redacted], the sent value absent"
		fi
	fi

	echo "== create an endpoint from a request the spec never had =="
	check "GET /legacy/ping before the conversion" 404 "$workspace_host" /legacy/ping
	if ! poll_until '.method == "GET" and .path == "/legacy/ping" and .matchedKind == "none"'; then
		echo "FAIL  the unmatched GET /legacy/ping never reached the traffic buffer"
		fail_count=$((fail_count + 1))
	else
		ping_tid=$(jq -r '[.rows[] | select(.method == "GET" and .path == "/legacy/ping" and .matchedKind == "none")] | last | .id // empty' "$BODY_FILE")
		if [[ -z "$ping_tid" ]]; then
			echo "FAIL  the unmatched GET /legacy/ping is not in the traffic buffer"
			fail_count=$((fail_count + 1))
		else
			to_ep_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/traffic/${ping_tid}/to-endpoint" '{}' -H "X-CSRF-Token: ${csrf}")
			if [[ "$to_ep_status" != "201" ]]; then
				echo "FAIL  POST .../to-endpoint: want status 201, got ${to_ep_status}: $(cat "$BODY_FILE")"
				fail_count=$((fail_count + 1))
			else
				created_path=$(jq -r '.path' "$BODY_FILE")
				# The pinned rule: an observed 404 becomes a 200. Re-serving the
				# 404 would make the whole conversion a no-op, and a check that
				# only asked "does it answer" would pass on the original 404.
				check "GET /legacy/ping after the conversion (404 -> 200)" 200 "$workspace_host" /legacy/ping
				echo "PASS  endpoint created from a recorded request at ${created_path}"
			fi
		fi
	fi

	# TASK 1's live regression, seeded BEFORE the conversion below runs: an
	# existing variant at responses["200"] carrying recipes/when/headers/
	# mediaType must survive a to-override conversion at that same status —
	# against the code this fixes (a bare overrides.Variant{Mode, Body,
	# BodyEncoding} literal replacing the whole map entry) every one of these
	# would be gone afterward, which is exactly how the auth preset's JWT
	# recipe on a login route died the moment an operator clicked "создать
	# правку из этого ответа". The when[] condition matches the request this
	# script actually sends below (a plain GET with no query string) via a
	# header curl always sends, so it is not a condition PUT will accept but
	# the mock plane can never satisfy.
	echo "== seed responses[\"200\"] on GET /widgets/{id} with recipes/when/headers/mediaType, before converting it =="
	seed_variant_ev=$(op_edit_version "$ws_id" "$detail_opkey")
	seed_variant_body=$(jq -n --argjson ev "$seed_variant_ev" '{
		overrideOn: true,
		routeOff: false,
		responses: {
			"200": {
				mode: "generated",
				recipes: {name: {kind: "const", value: "SEED-BEFORE-CONVERSION"}},
				when: [{in: "header", name: "User-Agent", op: "exists"}],
				headers: {"X-Seed-Preserved": "yes"},
				mediaType: "application/json"
			}
		},
		editVersion: $ev
	}')
	seed_variant_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$seed_variant_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$seed_variant_status" != "200" ]]; then
		echo "FAIL  seed variant with recipes/when/headers/mediaType before to-override: want status 200, got ${seed_variant_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	fi

	echo "== create an edit from a response that was actually served =="
	if ! poll_until ".method == \"GET\" and .path == \"/widgets/${item_id}\" and .status == 200 and .matchedKind == \"operation\""; then
		echo "FAIL  the served GET /widgets/${item_id} never reached the traffic buffer"
		fail_count=$((fail_count + 1))
	else
		detail_tid=$(jq -r --arg p "/widgets/${item_id}" '[.rows[] | select(.method == "GET" and .path == $p and .status == 200 and .matchedKind == "operation")] | last | .id // empty' "$BODY_FILE")
		if [[ -z "$detail_tid" ]]; then
			echo "FAIL  the served GET /widgets/${item_id} is not in the traffic buffer"
			fail_count=$((fail_count + 1))
		else
			to_ov_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/traffic/${detail_tid}/to-override" '{}' -H "X-CSRF-Token: ${csrf}")
			if [[ "$to_ov_status" != "200" ]]; then
				echo "FAIL  POST .../to-override: want status 200, got ${to_ov_status}: $(cat "$BODY_FILE")"
				fail_count=$((fail_count + 1))
			else
				pinned_opkey=$(jq -r '.opKey' "$BODY_FILE")
				# The key must be the operation's TEMPLATE path. A row keyed by
				# the concrete "/widgets/7" is stored, returns 200 here, and is
				# never read by the router again.
				if [[ "$pinned_opkey" != "$detail_opkey" ]]; then
					echo "FAIL  to-override wrote opKey '${pinned_opkey}', want the operation's own '${detail_opkey}' — a concrete-path key is orphaned on arrival"
					fail_count=$((fail_count + 1))
				else
					pinned_body=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}")
					pinned_name=$(jq -r '.name // empty' <<<"$pinned_body")
					if [[ -z "$pinned_name" ]]; then
						echo "FAIL  GET /widgets/${item_id} after to-override: body is not the pinned response: ${pinned_body}"
						fail_count=$((fail_count + 1))
					else
						echo "PASS  edit created from a recorded response, keyed ${pinned_opkey}, serves the pinned body"
					fi

					# The whole point of the seed above: the conversion must change
					# the mode and the body and NOTHING else. Against the old code
					# (a fresh overrides.Variant{} literal replacing the whole map
					# entry) every one of the four fields below would be gone.
					verify_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" '' -H "X-CSRF-Token: ${csrf}")
					if [[ "$verify_status" != "200" ]]; then
						echo "FAIL  GET the converted override to check preserved fields: want status 200, got ${verify_status}: $(cat "$BODY_FILE")"
						fail_count=$((fail_count + 1))
					else
						preserved_mode=$(jq -r '.responses["200"].mode' "$BODY_FILE")
						preserved_recipes=$(jq -r '.responses["200"].recipes | length' "$BODY_FILE")
						preserved_when=$(jq -r '.responses["200"].when | length' "$BODY_FILE")
						preserved_headers=$(jq -r '.responses["200"].headers | length' "$BODY_FILE")
						preserved_mediatype=$(jq -r '.responses["200"].mediaType // empty' "$BODY_FILE")
						if [[ "$preserved_mode" != "pinned" || "$preserved_recipes" == "0" || "$preserved_when" == "0" ||
							"$preserved_headers" == "0" || -z "$preserved_mediatype" ]]; then
							echo "FAIL  to-override discarded a field of the existing variant: mode=${preserved_mode} recipes=${preserved_recipes} when=${preserved_when} headers=${preserved_headers} mediaType='${preserved_mediatype}': $(cat "$BODY_FILE")"
							fail_count=$((fail_count + 1))
						else
							echo "PASS  to-override changed mode to pinned and the body, while recipes/when/headers/mediaType all survived"
						fi
					fi
				fi
			fi
		fi
	fi

	# --------------------------------------------------------------------------
	# P2a section E: session status/fail already have coverage above (force a
	# status, fail n=2). This section is the two remaining wire actions -- delay
	# and pause -- and the composition rules the P2a context file pins: session
	# beats a per-operation row, two distinct delay values so a hard-coded sleep
	# cannot pass both, a real held request released through the SAME background
	# curl that was parked (never a fresh one), a custom endpoint proven
	# separately because custom.go carries its own independent delay chain, the
	# revision-untouched guard extended from status to delay, the scenario
	# carve-out on BOTH planes, and the wire bounds with a positive control.
	# --------------------------------------------------------------------------

	# timed_get issues ONE GET and reports "<status> <elapsed-ms>", elapsed read
	# from curl's own %{time_total} rather than a shell-side before/after pair,
	# so this box's own fork/exec latency can't inflate the measurement.
	# --max-time bounds a runaway delay so a badly broken implementation fails
	# the window assertion below instead of hanging the run -- the harness-traps
	# warning about an unguarded timeout applies here too, hence the "|| true".
	timed_get() {
		local host=$1 path=$2 out status time_s ms
		out=$(curl -s -o "$BODY_FILE" -w '%{http_code} %{time_total}' --max-time 15 \
			-H "Host: ${host}" "${BASE_URL}${path}") || true
		status=${out%% *}
		time_s=${out#* }
		ms=$(awk -v t="$time_s" 'BEGIN{printf "%d", (t == "" ? -1 : t*1000)}')
		printf '%s %s' "$status" "$ms"
	}

	echo "== section E observation 1: session delay beats a 50ms row override (obs 5: revision untouched) =="

	# Pin a 50ms row-level delayMs FIRST, so a build where the row still wins is
	# distinguishable from one where the session wins: 50ms sits below both
	# windows below, so a row-wins bug fails both of them.
	row_delay_ev=$(op_edit_version "$ws_id" "$list_opkey")
	row_delay_body="{\"overrideOn\":true,\"routeOff\":false,\"delayMs\":50,\"responses\":{},\"editVersion\":${row_delay_ev}}"
	row_delay_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${list_opkey}" "$row_delay_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$row_delay_status" != "200" ]]; then
		echo "FAIL  PUT row delayMs=50 on GET /widgets: want status 200, got ${row_delay_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		# obs 5's "before" -- read AFTER the row PUT above, not before it: an
		# operator override is expected to bump the revision; only the SESSION
		# layer this observation is about must not.
		rev_before_delay=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null && jq -r '.revision' "$BODY_FILE")

		delay1_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "delay", ms: 300}')
		delay1_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$delay1_body" -H "X-CSRF-Token: ${csrf}")
		if [[ "$delay1_status" != "200" ]]; then
			echo "FAIL  POST session delay ms=300 on GET /widgets: want status 200, got ${delay1_status}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  POST session delay ms=300 returned 200"

			# obs 5's "after". Checking the POST above returned 200 FIRST is what
			# makes this comparison mean something: against a 501 nothing writes the
			# DB either way and revision-unchanged would pass trivially.
			http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
			rev_after_delay=$(jq -r '.revision' "$BODY_FILE")
			if [[ "$rev_before_delay" != "$rev_after_delay" ]]; then
				echo "FAIL  arming a session delay moved the revision ${rev_before_delay} -> ${rev_after_delay}"
				fail_count=$((fail_count + 1))
			else
				echo "PASS  arming a session delay left the revision at ${rev_after_delay}"
			fi

			timed1=$(timed_get "$workspace_host" /widgets)
			t1_status=${timed1%% *}
			t1_ms=${timed1#* }
			if [[ "$t1_status" != "200" ]]; then
				echo "FAIL  GET /widgets under delay ms=300: want status 200, got ${t1_status}"
				fail_count=$((fail_count + 1))
			elif ((t1_ms < 300 || t1_ms > 800)); then
				echo "FAIL  GET /widgets under delay ms=300: want 300..800ms, got ${t1_ms}ms (the 50ms row would also fail this window)"
				fail_count=$((fail_count + 1))
			else
				echo "PASS  GET /widgets under delay ms=300: ${t1_ms}ms, in 300..800ms -- session beat the 50ms row"
			fi
		fi
	fi

	echo "== section E observation 2: a second delay value separates reading the request from sleeping a constant =="
	delay2_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "delay", ms: 900}')
	delay2_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$delay2_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$delay2_status" != "200" ]]; then
		echo "FAIL  POST session delay ms=900 on GET /widgets: want status 200, got ${delay2_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		timed2=$(timed_get "$workspace_host" /widgets)
		t2_status=${timed2%% *}
		t2_ms=${timed2#* }
		if [[ "$t2_status" != "200" ]]; then
			echo "FAIL  GET /widgets under delay ms=900: want status 200, got ${t2_status}"
			fail_count=$((fail_count + 1))
		elif ((t2_ms < 900 || t2_ms > 1400)); then
			echo "FAIL  GET /widgets under delay ms=900: want 900..1400ms, got ${t2_ms}ms"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  GET /widgets under delay ms=900: ${t2_ms}ms, in 900..1400ms"
		fi
	fi

	echo "== section E observation 3: pause through the unauthenticated mock plane, and the release =="

	# obs 2 left a 900ms delay armed on this exact route; with the pause
	# directive alone a request would return in ~0.9s, and the "still parked
	# after 1s" check below would be a coin flip against that leftover delay.
	clear1_status=$(http_json DELETE "$workspace_host" /__mocker/state)
	if [[ "$clear1_status" != "200" ]]; then
		echo "FAIL  DELETE /__mocker/state (clearing obs 2's delay): want status 200, got ${clear1_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	fi

	pause_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "pause"}')
	pause_status=$(http_json POST "$workspace_host" /__mocker/state "$pause_body")
	if [[ "$pause_status" != "200" ]]; then
		echo "FAIL  POST /__mocker/state (pause GET /widgets): want status 200, got ${pause_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  POST /__mocker/state (pause) returned 200"

		# Its own mktemp files (PAUSE_*, reserved at the top of this script), never
		# http_json/check/check_header -- those all write through the SHARED
		# BODY_FILE/COOKIE_JAR, and a background writer racing this script's own
		# foreground reads of them is exactly the corruption http_json's own comment
		# warns about for a different reason. `set +e` inside the subshell keeps a
		# --max-time timeout (curl exit 28) from tearing down the subshell before
		# its exit code is captured; this script never `wait`s on it, so a nonzero
		# exit from the child never reaches this script's own `set -e`.
		(
			set +e
			curl -s -o "$PAUSE_BODY_FILE" -w '%{http_code}' --max-time 20 \
				-H "Host: ${workspace_host}" "${BASE_URL}/widgets" >"$PAUSE_STATUS_FILE"
			echo $? >"$PAUSE_EXIT_FILE"
			touch "$PAUSE_DONE_FILE"
		) &

		sleep 1
		if [[ -f "$PAUSE_DONE_FILE" ]]; then
			echo "FAIL  the parked GET /widgets already completed after 1s -- it should still be held"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  the parked GET /widgets has not completed after 1s"
		fi

		clear2_status=$(http_json DELETE "$workspace_host" /__mocker/state)
		if [[ "$clear2_status" != "200" ]]; then
			echo "FAIL  DELETE /__mocker/state (releasing the pause): want status 200, got ${clear2_status}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		fi

		# Poll for the SAME background curl's own done-marker -- do NOT `wait` on it
		# (its exit status under set -e would kill the run exactly like an
		# unguarded foreground curl), and a second, fresh curl here would prove
		# nothing: it passes against a plain long sleep and against a dead handler
		# alike.
		pause_tries=0
		while [[ ! -f "$PAUSE_DONE_FILE" ]] && ((pause_tries < 20)); do
			sleep 0.05
			pause_tries=$((pause_tries + 1))
		done
		if [[ ! -f "$PAUSE_DONE_FILE" ]]; then
			echo "FAIL  the parked GET /widgets did not complete within 1s of clearing the pause"
			fail_count=$((fail_count + 1))
		else
			pause_exit=$(cat "$PAUSE_EXIT_FILE")
			pause_recorded_status=$(cat "$PAUSE_STATUS_FILE")
			if [[ "$pause_exit" != "0" || "$pause_recorded_status" != "200" ]]; then
				echo "FAIL  the released request recorded exit=${pause_exit} status=${pause_recorded_status}, want exit=0 status=200"
				fail_count=$((fail_count + 1))
			else
				echo "PASS  clearing the pause released the SAME held request: exit 0, status 200"
			fi
		fi
	fi

	echo "== section E observation 4: a custom endpoint obeys the session layer too =="
	# custom.go has its own independent delay chain from respond.go's -- without
	# this observation, arming a delay on a spec-generated route could pass
	# while a custom-endpoint route ignores the session layer entirely.
	delay4_body=$(jq -n '{target: {method: "GET", path: "/legacy/ping"}, action: "delay", ms: 300}')
	delay4_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$delay4_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$delay4_status" != "200" ]]; then
		echo "FAIL  POST session delay ms=300 on GET /legacy/ping: want status 200, got ${delay4_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		timed4=$(timed_get "$workspace_host" /legacy/ping)
		t4_status=${timed4%% *}
		t4_ms=${timed4#* }
		if [[ "$t4_status" != "200" ]]; then
			echo "FAIL  GET /legacy/ping under delay ms=300: want status 200, got ${t4_status}"
			fail_count=$((fail_count + 1))
		elif ((t4_ms < 300 || t4_ms > 800)); then
			echo "FAIL  GET /legacy/ping under delay ms=300: want 300..800ms, got ${t4_ms}ms"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  GET /legacy/ping under delay ms=300: ${t4_ms}ms, in 300..800ms -- the custom endpoint obeys the session layer"
		fi
		http_json DELETE "$workspace_host" /__mocker/state >/dev/null
	fi

	echo "== section E observation 6: a scenario directive answers 400 on BOTH planes, not the P1c-2 era's 501 =="
	# P2b (A17) deletes the 501 this check used to pin: scenarios shipped their
	# OWN routes (POST .../scenarios/{sid}/activate, .../scenarios/deactivate,
	# and the mock plane's own {"scenario":"<name>"} switch on this same
	# endpoint), so a "scenario" key on the SESSION directive union is now a
	# wrong-shape request, not an unimplemented one. The body below still
	# carries scenario as an OBJECT ({"name":"x"}), which was always the P1c-2
	# era's own illustrative shape, not the real wire contract -- A17's actual
	# contract is a bare JSON STRING (see internal/mockplane/livestate.go's
	# serveScenarioSwitch), so this object shape now fails EARLIER on the mock
	# plane (400, "must be a scenario name as a JSON string") than a real
	# caller would ever see. The P2b block below (before the MCP block) is
	# where the real string-shaped contract gets exercised end to end;
	# this one stays as the regression guard that the OLD 501 is actually
	# gone on both planes, not just documented as gone.
	scenario_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "status", status: 503, scenario: {name: "x"}}')
	scenario_admin_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$scenario_body" -H "X-CSRF-Token: ${csrf}")
	scenario_admin_code=$(jq -r '.error.code // empty' "$BODY_FILE")
	if [[ "$scenario_admin_status" != "400" || "$scenario_admin_code" == "not_implemented_yet" ]]; then
		echo "FAIL  POST .../session with a scenario: want status 400 (code other than not_implemented_yet), got status ${scenario_admin_status} code '${scenario_admin_code}': $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  POST .../session with a scenario: 400, code '${scenario_admin_code}' (admin plane, the 501 is gone)"
	fi
	scenario_mock_status=$(http_json POST "$workspace_host" /__mocker/state "$scenario_body")
	scenario_mock_code=$(jq -r '.error.code // empty' "$BODY_FILE")
	if [[ "$scenario_mock_status" != "400" || "$scenario_mock_code" == "not_implemented_yet" ]]; then
		echo "FAIL  POST /__mocker/state with a scenario: want status 400 (code other than not_implemented_yet), got status ${scenario_mock_status} code '${scenario_mock_code}': $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  POST /__mocker/state with a scenario: 400, code '${scenario_mock_code}' (mock plane, the 501 is gone)"
	fi

	echo "== section E observation 7: the wire bounds hold, with a positive control =="
	# Both bad bodies carry a real target -- a MISSING target is rejected by
	# normalize() on its own, and at HEAD an unknown "ms" field is rejected by
	# the decoder before normalize ever runs; either way a 400 that would mean
	# nothing about the rule actually under test here.
	bad_delay_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "delay", ms: 300, status: 200}')
	bad_delay_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$bad_delay_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$bad_delay_status" != "400" ]]; then
		echo "FAIL  delay with a non-zero status: want status 400, got ${bad_delay_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  delay with a non-zero status: 400"
	fi

	bad_pause_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "pause", ms: 5}')
	bad_pause_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$bad_pause_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$bad_pause_status" != "400" ]]; then
		echo "FAIL  pause with a non-zero ms: want status 400, got ${bad_pause_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  pause with a non-zero ms: 400"
	fi

	# The positive control: the SAME two bodies minus the offending field.
	# Without it, a build that rejects every directive would score two passes
	# above for entirely the wrong reason.
	good_delay_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "delay", ms: 300}')
	good_delay_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$good_delay_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$good_delay_status" != "200" ]]; then
		echo "FAIL  positive control, delay without status: want status 200, got ${good_delay_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  positive control, delay without status: 200"
	fi

	good_pause_body=$(jq -n '{target: {method: "GET", path: "/widgets"}, action: "pause"}')
	good_pause_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$good_pause_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$good_pause_status" != "200" ]]; then
		echo "FAIL  positive control, pause without ms: want status 200, got ${good_pause_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  positive control, pause without ms: 200"
	fi

	# The positive control just above armed a real pause on GET /widgets.
	# Nothing after this point requests that route on this workspace, but leave
	# no directive armed past the observations that needed one.
	http_json DELETE "$workspace_host" /__mocker/state >/dev/null
else
	echo "FAIL  item_id is empty: the P1c-2 checks, the custom-endpoint fixture, and the section-E delay/pause observations all depend on it -- without this explicit failure this whole block is silently skipped and the script still exits 0"
	fail_count=$((fail_count + 1))
fi

# --------------------------------------------------------------------------
# P1d-1: the admin UI ships inside the binary, not as a placeholder go:embed
# would still compile against (.dockerignore excludes /internal/webui/dist/
# from the image build context on purpose — see that file's comment — so
# this is the check that fails if the node stage's COPY ever silently
# didn't land). curl-level only, unconditional: it needs no spec, no
# workspace, nothing the P1b/P1c-2 blocks above may have SKIPped.
# --------------------------------------------------------------------------
echo "== P1d-1: the built UI is served, not a placeholder =="

index_body=$(curl -s -H "Host: ${ADMIN_HOST}" "${BASE_URL}/")
if ! grep -qE '<script[^>]+src=' <<<"$index_body"; then
	echo "FAIL  GET /: no <script src=...> tag in the body — looks like a placeholder, not the built app: ${index_body}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  GET /: body carries a <script src=...> tag"
fi
check_header "GET / is the app shell" 200 "$ADMIN_HOST" / Content-Type text/html
check_header "GET / is never cached (a stale shell would outlive every deploy)" 200 "$ADMIN_HOST" / Cache-Control no-cache

nope_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${ADMIN_HOST}" "${BASE_URL}/api/definitely-nope")
if [[ "$nope_status" != "404" ]]; then
	echo "FAIL  GET /api/definitely-nope: want status 404, got ${nope_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
elif grep -qE '<script[^>]+src=' "$BODY_FILE"; then
	echo "FAIL  GET /api/definitely-nope: body looks like the SPA shell, not net/http's own plain 404: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  GET /api/definitely-nope: 404, net/http's own page (not the SPA, and no /api/ catch-all stole it)"
fi

login_get_status=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${ADMIN_HOST}" "${BASE_URL}/api/auth/login")
if [[ "$login_get_status" != "405" ]]; then
	echo "FAIL  GET /api/auth/login: want status 405 (POST-only route), got ${login_get_status}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  GET /api/auth/login: 405, the UI mount did not swallow the route's method handling"
fi

# The hash is Vite's, decided at build time — grepped out of the served
# shell rather than hard-coded, so this keeps passing across every rebuild.
asset_path=$(grep -oE '/assets/[A-Za-z0-9._-]+\.(js|css)' <<<"$index_body" | head -n1)
if [[ -z "$asset_path" ]]; then
	echo "FAIL  no hashed /assets/... reference found in the served index.html: ${index_body}"
	fail_count=$((fail_count + 1))
else
	check_header "GET ${asset_path} is cached forever (a content hash never means a changed body)" 200 "$ADMIN_HOST" "$asset_path" Cache-Control immutable
fi

# /w/$id is removed this phase (P1e §5): under MOCKER_ROUTING=path, /w/{slug}
# belongs to the mock plane, so the admin SPA's own workspace route moved to
# /workspaces/$id — this deep-link check follows that rename.
deep_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${ADMIN_HOST}" "${BASE_URL}/workspaces/anything")
if [[ "$deep_status" != "200" ]]; then
	echo "FAIL  GET /workspaces/anything: want status 200 (SPA fallback for a client route), got ${deep_status}"
	fail_count=$((fail_count + 1))
elif [[ "$(cat "$BODY_FILE")" != "$index_body" ]]; then
	echo "FAIL  GET /workspaces/anything: body differs from GET / — a reloaded deep link would not get the app shell"
	fail_count=$((fail_count + 1))
else
	echo "PASS  GET /workspaces/anything: 200, byte-identical to / (a reloaded deep link works)"
fi

# --------------------------------------------------------------------------
# P2b: DESIGN §4's Scenario layer -- a NAMED SET saved from a workspace and
# switched on with one click, composed at request-runtime as a per-key
# OVERLAY over the workspace's own settings/overrides (A1: membership decides
# the LAYER, the flag decides the EFFECT -- A2), never a restore written over
# the workspace's own rows. Fifteen observations below, straight from
# p2b-context.md §G; each names the wrong implementation it exists to fail,
# and that reasoning travels with its own check rather than living only here.
#
# PLACEMENT (§G's own instruction, not a free choice): before the MCP block,
# never after it -- from :1694 on, the stack runs under MOCKER_ROUTING=path,
# where every "Host: <slug>.mock.local" + bare-path request below would break
# against a perfectly correct implementation (in path mode /widgets on the
# workspace host no longer reaches the mock plane at all). The MCP block, in
# turn, pins shared state THIS section must not disturb: list_opkey's own
# delayMs=50 row ("MCP observation 6"'s own comment says so explicitly) and
# create_opkey's ABSENCE ("nothing writes to create_opkey again until MCP
# observation 8"). So this section touches ONLY detail_opkey and create_opkey
# among the pre-existing override rows, restores both exactly, and restores
# the workspace's whole settings object too, before the MCP block runs --
# "P2b restore" at the very end of this section is that restoration stated
# as an assertion, not a cleanup nobody checks.
# --------------------------------------------------------------------------
echo "== P2b: the Scenario layer (DESIGN §4), fifteen live observations =="

# set_settings applies a jq FILTER to the workspace's CURRENT full settings
# object (read fresh via GET, never from a stale variable) and PATCHes the
# result back whole, leaving the PATCH response in $BODY_FILE like http_json
# always does. Exists because handlePatchWorkspace assigns `cur.Settings =
# *body.Settings` with NO merge (workspace_handlers.go's own doc comment,
# and observation 2's own warning in p2b-context.md §G): a hand-built partial
# object here -- even one that only names the ONE field a call means to
# change -- would zero every field it doesn't mention, including
# auth.signingKey and the whole identity block, corrupting observation 13's
# own fixture two groups later.
set_settings() {
	local filter=$1
	local cur new ev
	http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" >/dev/null
	cur=$(jq -c '.settings' "$BODY_FILE")
	ev=$(jq -r '.editVersion' "$BODY_FILE")
	new=$(jq -c "$filter" <<<"$cur")
	http_json PATCH "$ADMIN_HOST" "/api/workspaces/${ws_id}" \
		"$(jq -cn --argjson s "$new" --argjson ev "$ev" '{settings: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}"
}

# check_preflight is check_header's sibling for a CORS preflight -- observation
# 7's own note that neither existing helper can express it: check() only ever
# issues a bare GET, and http_json throws every response header away on the
# floor. The -D-/-w split below is check_header's own, copied for the same
# reason: -o keeps the always-empty 204 body out of the header stream curl
# writes to stdout. corsActive (internal/mockplane/cors.go) never fires on a
# request carrying no Origin, which is why this always sends one.
check_preflight() {
	local desc=$1 host=$2 path=$3 origin=$4 want_acao_substr=$5
	local out status headers acao_line

	out=$(curl -s -o /dev/null -D - -X OPTIONS -H "Host: ${host}" -H "Origin: ${origin}" \
		-H 'Access-Control-Request-Method: GET' -w '\n%{http_code}' "${BASE_URL}${path}")
	status=${out##*$'\n'}
	headers=${out%$'\n'"$status"}

	if [[ "$status" != "204" ]]; then
		echo "FAIL  ${desc}: want status 204, got ${status}"
		fail_count=$((fail_count + 1))
		return
	fi

	acao_line=$(grep -i '^Access-Control-Allow-Origin:' <<<"$headers" | head -n1 | tr -d '\r')
	if [[ "$acao_line" != *"$want_acao_substr"* ]]; then
		echo "FAIL  ${desc}: Access-Control-Allow-Origin want to contain '${want_acao_substr}', got '${acao_line}'"
		fail_count=$((fail_count + 1))
		return
	fi

	echo "PASS  ${desc} (status 204, ${acao_line})"
}

# --------------------------------------------------------------------------
# Observation 15: the served entry chunk was built against THIS contract.
# asset_path/index_body are P1d-1's own variables, still in scope. The entry
# chunk carries no /api/... literal at all -- every one of those lives in a
# lazily-loaded feature chunk (measured on the built tree: 51 files under
# internal/webui/dist/assets/, zero /api/workspaces occurrences in the entry
# chunk) -- so the route-MODULE name is what actually distinguishes a build
# that shipped this slice's fifth tab from one that didn't.
# --------------------------------------------------------------------------
if [[ -z "${asset_path:-}" ]]; then
	echo "FAIL  observation 15: no asset_path captured by the P1d-1 block above -- that check must have failed first"
	fail_count=$((fail_count + 1))
else
	entry_body=$(curl -s -H "Host: ${ADMIN_HOST}" "${BASE_URL}${asset_path}")
	if ! grep -qF '_authed.workspaces._id.scenarios' <<<"$entry_body"; then
		echo "FAIL  observation 15: entry chunk ${asset_path} carries no _authed.workspaces._id.scenarios route-module name -- dist looks built against the PREVIOUS contract (34 routes, four workspace tabs), not this slice's 40/five"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  observation 15: entry chunk ${asset_path} names the scenarios route module -- dist matches this contract (a literal can still sit in dead code; routes.test.tsx proves reachability, not this)"
	fi
fi

# --------------------------------------------------------------------------
# Observation 14: the admin session route answers 400 for {scenario}, A17 --
# never the 501 this slice deletes. §14's session body never had a
# "scenario" key at all; it only ever reached this shared decoder because
# the mock plane's own directive type is what handlePostSession decodes into
# too.
# --------------------------------------------------------------------------
obs14_body=$(jq -n '{target: "*", scenario: "x"}')
obs14_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/session" "$obs14_body" -H "X-CSRF-Token: ${csrf}")
obs14_code=$(jq -r '.error.code // empty' "$BODY_FILE")
if [[ "$obs14_status" != "400" || "$obs14_code" == "not_implemented_yet" || -z "$obs14_code" ]]; then
	echo "FAIL  observation 14: want status 400 with a code other than not_implemented_yet, got status ${obs14_status} code '${obs14_code}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 14: POST .../session with a scenario key answers 400 (code '${obs14_code}') -- the 501 this slice exists to delete is actually gone, not just documented as gone"
fi

# --------------------------------------------------------------------------
# Baseline capture, before this block changes anything: the settings object
# to restore to at the end, and detail_opkey's current stored document. The
# P1c-2 to-override conversion above left that opKey "pinned", with
# recipes/when/headers/mediaType all preserved -- nothing downstream ever
# reads it again (grep the file: the last reference is line ~907's own
# verify), but this section's restore still owes it back byte-for-byte, per
# this section's own header comment.
# --------------------------------------------------------------------------
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" >/dev/null
p2b_orig_settings=$(jq -c '.settings' "$BODY_FILE")
n_listsize=$(jq -r '.listSize' <<<"$p2b_orig_settings")
m_listsize=$((n_listsize + 4))
m2_listsize=$((n_listsize + 9))

http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" >/dev/null
p2b_detail_orig=$(jq -c 'del(.method, .path, .opKey, .updatedAt)' "$BODY_FILE")
p2b_detail_orig_ev=$(jq -r '.editVersion' "$BODY_FILE")

echo "== observations 1, 2, 3, 4, 9 (admin surface), 12: scenario p2b-a =="

# Observation 1: PUT an override producing A, snapshot S, PUT a SECOND
# override on the SAME opKey producing B (the divergence), assert B is
# currently served, then activate S and assert A comes back. Both A and B are
# const RECIPES, not pinned bodies -- observation 1's own point, per §G: A4's
# wrong implementation (recipe sets compiled from the workspace's override
# map while the COMPOSED map is served) is invisible against a pinned body,
# because the body lives in the row and a mis-compiled recipe set is never
# consulted to produce it.
# A3: editVersion is p2b_detail_orig_ev, the baseline read captured just
# above -- the first write to detail_opkey in this block, so nothing has
# moved it since.
const_a_body=$(jq -n --argjson ev "$p2b_detail_orig_ev" '{overrideOn: true, routeOff: false, responses: {
	"200": {mode: "generated", recipes: {name: {kind: "const", value: "P2B-SCENARIO-A"}}}
}, editVersion: $ev}')
obs1_put_a=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$const_a_body" -H "X-CSRF-Token: ${csrf}")
const_a_new_ev=$(jq -r '.editVersion' "$BODY_FILE")
if [[ "$obs1_put_a" != "200" ]]; then
	echo "FAIL  observation 1 setup: PUT override A on ${detail_opkey}: want status 200, got ${obs1_put_a}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

scn_a_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" '{"name":"p2b-a"}' -H "X-CSRF-Token: ${csrf}")
scn_a_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$scn_a_status" != "201" || -z "$scn_a_id" || "$scn_a_id" == "null" ]]; then
	echo "FAIL  observation 1 setup: create scenario p2b-a (snapshotting A): want status 201 with an id, got ${scn_a_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

# Observation 3's own setup lives here too, deliberately: the override on
# create_opkey (C) has to exist BEFORE p2b-a is activated -- and, because
# p2b-a was already snapshotted above, absent from S's snapshot at the same
# time. Both conditions at once are what makes the "still served" check
# below (after the activate call) load-bearing: a destructive "replace"
# activation runs its destructive DELETE/INSERT-from-snapshot AT the
# activate call, so a wrong implementation would wipe C right there. If C
# were instead created after activation (as a prior draft of this block
# did), that same wrong implementation's destructive swap already happened
# before C existed, C would survive trivially, and the observation could
# never go red against the exact bug it exists to catch. create_opkey (POST
# /widgets) is free right now -- its only earlier override (the "when[]"
# duplicate-kind check) was DELETEd immediately after use, and nothing else
# touches it until MCP observation 8, far below.
# create_opkey is genuinely un-overridden here (this block's own comment
# above says so), so op_edit_version's 404 branch answers 0.
const_c_ev=$(op_edit_version "$ws_id" "$create_opkey")
const_c_body=$(jq -n --argjson ev "$const_c_ev" '{overrideOn: true, routeOff: false, responses: {
	"201": {mode: "pinned", body: {marker: "P2B-KEY-ABSENT-FROM-SNAPSHOT"}}
}, editVersion: $ev}')
obs3_put=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${create_opkey}" "$const_c_body" -H "X-CSRF-Token: ${csrf}")
if [[ "$obs3_put" != "200" ]]; then
	echo "FAIL  observation 3 setup: PUT override C on ${create_opkey} (before activating p2b-a): want status 200, got ${obs3_put}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

# The second write to detail_opkey in this block -- A3: editVersion is
# const_a_new_ev, the FRESH token A's own write response just returned
# above, never a re-read (D5's "write twice without re-reading" promise).
const_b_body=$(jq -n --argjson ev "$const_a_new_ev" '{overrideOn: true, routeOff: false, responses: {
	"200": {mode: "generated", recipes: {name: {kind: "const", value: "P2B-SCENARIO-B"}}}
}, editVersion: $ev}')
obs1_put_b=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$const_b_body" -H "X-CSRF-Token: ${csrf}")
if [[ "$obs1_put_b" != "200" ]]; then
	echo "FAIL  observation 1 setup: PUT override B on ${detail_opkey}: want status 200, got ${obs1_put_b}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

diverged_name=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}" | jq -r '.name')
if [[ "$diverged_name" != "P2B-SCENARIO-B" ]]; then
	echo "FAIL  observation 1: before activating, GET /widgets/${item_id} should already answer B (the workspace's CURRENT edit), got '${diverged_name}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 1 setup: the workspace has diverged from A to B, scenario p2b-a still holds A"
fi

# Observation 2a's own setup lives here too, deliberately: S must end up with
# a DIFFERENT listSize from what the workspace has -- and the PATCH has to
# run AFTER the snapshot, or the scenario's own listSize would be M too, and
# the two halves of observation 2 below could not tell "composed" from "not".
obs2_setup_status=$(set_settings ".listSize = ${m_listsize}")
if [[ "$obs2_setup_status" != "200" ]]; then
	echo "FAIL  observation 2 setup: PATCH workspace listSize to ${m_listsize}: want status 200, got ${obs2_setup_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

rev_before_activate_a=$(jq -r '.revision' "$BODY_FILE")
activate_a_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_a_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")
activate_a_scnid=$(jq -r '.scenarioId' "$BODY_FILE")
activate_a_rev=$(jq -r '.revision' "$BODY_FILE")
want_rev_a=$((rev_before_activate_a + 1))
active_name=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}" | jq -r '.name')
if [[ "$activate_a_status" != "200" || "$activate_a_scnid" != "$scn_a_id" ||
	"$activate_a_rev" != "$want_rev_a" || "$active_name" != "P2B-SCENARIO-A" ]]; then
	echo "FAIL  observation 1: activate p2b-a: want status 200 scenarioId=${scn_a_id} revision=${want_rev_a} name=P2B-SCENARIO-A, got status ${activate_a_status} scenarioId=${activate_a_scnid} revision=${activate_a_rev} name='${active_name}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 1: activating p2b-a masks B with A, scenarioId=${scn_a_id}, revision ${rev_before_activate_a} -> ${activate_a_rev} (+1 exactly)"
fi

# Observation 2: the WORKSPACE layer is untouched while S is active, read
# through the admin API -- the load-bearing observation of this whole run.
# "Deactivate and the old answer returns" is satisfiable by a destructive
# implementation that saved the old value privately and restored it; reading
# the workspace layer through the admin API WHILE the mock serves the
# scenario is what a destructive implementation cannot fake, and it only
# works because handleGetOperation is NEVER composed (A18).
obs2_ws_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}")
obs2_listsize=$(jq -r '.settings.listSize' "$BODY_FILE")
obs2_op_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}")
obs2_op_name=$(jq -r '.responses["200"].recipes.name.value // empty' "$BODY_FILE")
if [[ "$obs2_ws_status" != "200" || "$obs2_op_status" != "200" ||
	"$obs2_listsize" != "$m_listsize" || "$obs2_op_name" != "P2B-SCENARIO-B" ]]; then
	echo "FAIL  observation 2: while p2b-a is active, GET /api/workspaces/${ws_id} should report the WORKSPACE's own listSize (${m_listsize}) and GET .../operations/${detail_opkey} should report B (never composed, A18) -- got listSize=${obs2_listsize} op-recipe-value='${obs2_op_name}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 2: the admin API reads the WORKSPACE layer straight through activation -- listSize=${obs2_listsize} (not S's ${n_listsize}), operation still reports B (not composed)"
fi

# Observation 3: the override on create_opkey (C) -- created above, BEFORE
# p2b-a was activated, on a key its snapshot does not mention -- survives
# activation. Checked here, AFTER activate_a_status, so a destructive
# "replace" implementation (which would have wiped C at the activate call
# itself, per the setup comment above) cannot pass this by accident.
obs3_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X POST -H "Host: ${workspace_host}" \
	-H 'Content-Type: application/json' --data '{"name":"n","kind":"new"}' "${BASE_URL}/widgets")
obs3_marker=$(jq -r '.marker // empty' "$BODY_FILE")
if [[ "$obs3_status" != "201" || "$obs3_marker" != "P2B-KEY-ABSENT-FROM-SNAPSHOT" ]]; then
	echo "FAIL  observation 3: an override created on create_opkey BEFORE p2b-a was activated (a key its snapshot does not mention) should still be served while p2b-a is active -- got POST status ${obs3_status}, marker '${obs3_marker}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 3: an override on a key p2b-a does not mention, created before activation, survives it -- absent-from-snapshot falls through to the workspace's own row, not to nothing, and a destructive replace would have wiped it at the activate call"
fi
obs3_del=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${create_opkey}" '{}' -H "X-CSRF-Token: ${csrf}")
if [[ "$obs3_del" != "200" && "$obs3_del" != "204" ]]; then
	echo "FAIL  observation 3 cleanup: DELETE the override on create_opkey: want 200 or 204, got ${obs3_del} -- MCP observation 5/6 below depend on this key being absent again"
	fail_count=$((fail_count + 1))
fi

# Observation 9, admin surface: activating the already-active scenario is a
# no-op (A7) -- no write, no bump, no rebuild. "Before" has to be read FRESH
# right here, not reused from activate_a_rev above: observation 3's own PUT
# and DELETE on create_opkey each bump workspaces.revision too (any override
# write invalidates the runtime cache the same way a scenario switch does),
# so the revision has legitimately moved since activate_a_rev for reasons
# that have nothing to do with A7 -- comparing against that stale value would
# make this check red against a perfectly correct implementation.
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" >/dev/null
rev_before_reactivate_a=$(jq -r '.revision' "$BODY_FILE")
reactivate_a_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_a_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")
reactivate_a_scnid=$(jq -r '.scenarioId' "$BODY_FILE")
reactivate_a_rev=$(jq -r '.revision' "$BODY_FILE")
reactivate_a_name=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}" | jq -r '.name')
if [[ "$reactivate_a_status" != "200" || "$reactivate_a_scnid" != "$scn_a_id" ||
	"$reactivate_a_rev" != "$rev_before_reactivate_a" || "$reactivate_a_name" != "P2B-SCENARIO-A" ]]; then
	echo "FAIL  observation 9 (admin surface): re-activating the already-active p2b-a should be a no-op -- want scenarioId=${scn_a_id} revision=${rev_before_reactivate_a} (unchanged), got scenarioId=${reactivate_a_scnid} revision=${reactivate_a_rev}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 9 (admin surface): re-activating p2b-a changed nothing -- scenarioId, revision (${reactivate_a_rev}) and body all identical"
fi

# Observation 4: deactivating restores BOTH levels -- the row and listSize --
# to the workspace's own current values (B and M), not to the scenario's
# frozen ones (A and N) and not to nothing.
deactivate_a_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}")
deactivate_a_scnid=$(jq -r '.scenarioId' "$BODY_FILE")
deactivate_a_name=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}" | jq -r '.name')
deactivate_a_ws_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}")
deactivate_a_listsize=$(jq -r '.settings.listSize' "$BODY_FILE")
if [[ "$deactivate_a_status" != "200" || "$deactivate_a_scnid" != "null" || "$deactivate_a_ws_status" != "200" ||
	"$deactivate_a_name" != "P2B-SCENARIO-B" || "$deactivate_a_listsize" != "$m_listsize" ]]; then
	echo "FAIL  observation 4: deactivating p2b-a should restore BOTH levels -- want name=P2B-SCENARIO-B listSize=${m_listsize} scenarioId=null, got name='${deactivate_a_name}' listSize=${deactivate_a_listsize} scenarioId=${deactivate_a_scnid}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 4: deactivating p2b-a restores both the row (B) and listSize (${m_listsize}) -- the workspace's own truth, not S's frozen one"
fi

# Observation 12: delete the ACTIVE scenario, runtime HOT first. Reactivate,
# issue ONE mock request so the (workspace, revision) runtime entry is
# actually resident, THEN delete. Without the hot request the entry may never
# have been built at all (or may have been evicted by MOCKER_RUNTIME_CACHE's
# LRU bound under pressure from other workspaces this run creates further
# below) -- a revision bump does not evict anything, it changes the KEY, so
# the vacuity here is an ABSENT entry, not an evicted one -- and then the
# first post-delete request would build fresh and answer B whether or not the
# bump ever happened. The bump is the property under test, not the body.
http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_a_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null
curl -s -o /dev/null -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}"

http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" >/dev/null
rev_before_delete_a=$(jq -r '.revision' "$BODY_FILE")
delete_a_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_a_id}" '{}' -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" >/dev/null
rev_after_delete_a=$(jq -r '.revision' "$BODY_FILE")
scnid_after_delete_a=$(jq -r '.scenarioId' "$BODY_FILE")
post_delete_name=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}" | jq -r '.name')
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" >/dev/null
p2b_a_still_listed=$(jq -r '[.scenarios[].name] | any(. == "p2b-a")' "$BODY_FILE")
want_rev_after_delete=$((rev_before_delete_a + 1))
if [[ "$delete_a_status" != "204" || "$rev_after_delete_a" != "$want_rev_after_delete" ||
	"$scnid_after_delete_a" != "null" || "$post_delete_name" != "P2B-SCENARIO-B" ||
	"$p2b_a_still_listed" != "false" ]]; then
	echo "FAIL  observation 12: deleting the ACTIVE p2b-a (runtime hot) -- want DELETE status 204, revision ${rev_before_delete_a} -> ${want_rev_after_delete} (+1 exactly), scenarioId=null, name=P2B-SCENARIO-B, absent from the list; got status ${delete_a_status} revision ${rev_after_delete_a} scenarioId=${scnid_after_delete_a} name='${post_delete_name}' stillListed=${p2b_a_still_listed}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 12: deleting the active scenario bumps revision exactly once (${rev_before_delete_a} -> ${rev_after_delete_a}), reverts to B, and drops off the list"
fi

second_delete_a_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_a_id}" '{}' -H "X-CSRF-Token: ${csrf}")
if [[ "$second_delete_a_status" != "404" ]]; then
	echo "FAIL  observation 12: a second DELETE of the same scenario id: want status 404, got ${second_delete_a_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 12: a second DELETE of the same scenario id: 404"
fi

echo "== observation 5: a scenario row with overrideOn=false masks the workspace's edit (A2) =="

# The row must be PRESENT in the snapshot with overrideOn=false -- membership
# decides the LAYER (A1), the flag decides the EFFECT (A2). Snapshotting a
# workspace where the key is simply ABSENT would re-test observation 3, not
# this: A2 is specifically that membership and flag are two different
# questions, and an implementation that collapses them into one would treat
# "row present, flag off" the same as either "row present, flag on" or "row
# absent" -- this setup is the one shape that tells those apart.
false_ev=$(op_edit_version "$ws_id" "$detail_opkey")
false_body=$(jq -n --argjson ev "$false_ev" '{overrideOn: false, routeOff: false, responses: {}, editVersion: $ev}')
obs5_put_false=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$false_body" -H "X-CSRF-Token: ${csrf}")

scn_b_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" '{"name":"p2b-b"}' -H "X-CSRF-Token: ${csrf}")
scn_b_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$obs5_put_false" != "200" || "$scn_b_status" != "201" || -z "$scn_b_id" || "$scn_b_id" == "null" ]]; then
	echo "FAIL  observation 5 setup: PUT overrideOn=false then create p2b-b: got PUT status ${obs5_put_false}, create status ${scn_b_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

# Now change the WORKSPACE's own row back to overrideOn=true with a THIRD
# value. If a wrong implementation let membership alone (ignoring the flag)
# decide the effect, or fell through to the workspace's CURRENT row instead
# of "the spec says" once the flag reads off, THIS is what it would leak.
const_d_ev=$(op_edit_version "$ws_id" "$detail_opkey")
const_d_body=$(jq -n --argjson ev "$const_d_ev" '{overrideOn: true, routeOff: false, responses: {
	"200": {mode: "generated", recipes: {name: {kind: "const", value: "P2B-SCENARIO-D"}}}
}, editVersion: $ev}')
http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$const_d_body" -H "X-CSRF-Token: ${csrf}" >/dev/null

activate_b_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_b_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")
masked_name=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}" | jq -r '.name')
if [[ "$activate_b_status" != "200" || "$masked_name" != "$baseline_name" ||
	"$masked_name" == "P2B-SCENARIO-B" || "$masked_name" == "P2B-SCENARIO-D" ]]; then
	echo "FAIL  observation 5: a scenario row with overrideOn=false should mask the workspace's edit entirely -- want the spec-generated name '${baseline_name}', got '${masked_name}' (activate status ${activate_b_status})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 5: p2b-b's overrideOn=false row masks the workspace's D edit -- served name is the spec's own generated value, neither B nor D"
fi

http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null

echo "== observation 6: an endpoint deleted after S was saved is not brought back by activating S =="

create_ep_body=$(jq -n '{method: "GET", path: "/p2b-custom", status: 200, body: {marker: "p2b-endpoint-alive"}}')
create_ep_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/endpoints" "$create_ep_body" -H "X-CSRF-Token: ${csrf}")
ep_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$create_ep_status" != "201" || -z "$ep_id" || "$ep_id" == "null" ]]; then
	echo "FAIL  observation 6 setup: create custom endpoint GET /p2b-custom: want status 201, got ${create_ep_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

# S is saved WHILE /p2b-custom still exists -- this ordering is the
# observation (§G): creating the endpoint AFTER saving S would fail nothing,
# because then S's snapshot genuinely never saw it. §0's own rule is that a
# scenario carries `endpoints: []` unconditionally, never the workspace's
# custom endpoints, so it must not matter that S was saved while E existed.
scn_c_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" '{"name":"p2b-c"}' -H "X-CSRF-Token: ${csrf}")
scn_c_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$scn_c_status" != "201" ]]; then
	echo "FAIL  observation 6 setup: create scenario p2b-c while /p2b-custom exists: want status 201, got ${scn_c_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

delete_ep_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${ws_id}/endpoints/${ep_id}" '{}' -H "X-CSRF-Token: ${csrf}")
activate_c_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_c_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")
ep_after_status=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/p2b-custom")
if [[ ("$delete_ep_status" != "200" && "$delete_ep_status" != "204") || "$activate_c_status" != "200" || "$ep_after_status" != "404" ]]; then
	echo "FAIL  observation 6: DELETE endpoint status ${delete_ep_status}, activate p2b-c status ${activate_c_status}, GET /p2b-custom after activation want 404, got ${ep_after_status}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 6: /p2b-custom stays gone after activating a scenario saved while it still existed -- scenarios never carry endpoints (§0), under either composition rule"
fi

http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null

echo "== observation 7: excluded settings (basePath/cors/notFoundBody) ignored, included ones (listSize) not -- in ONE scenario =="

http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" >/dev/null
cur_before_d=$(jq -c '.settings' "$BODY_FILE")

obs7_setup_status=$(set_settings ".basePath = \"/p2b-scenario-prefix\" | .cors.mode = \"off\" | .notFoundBody = {marker: \"p2b-scenario-404\"} | .listSize = ${m2_listsize}")
if [[ "$obs7_setup_status" != "200" ]]; then
	echo "FAIL  observation 7 setup: diverge workspace settings before snapshotting: want status 200, got ${obs7_setup_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

scn_d_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" '{"name":"p2b-d"}' -H "X-CSRF-Token: ${csrf}")
scn_d_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$scn_d_status" != "201" ]]; then
	echo "FAIL  observation 7 setup: create scenario p2b-d with diverged settings: want status 201, got ${scn_d_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

# Put the workspace's settings back to what they were a moment ago (basePath
# "", cors reflect, no notFoundBody, listSize M) -- p2b-d's snapshot already
# has the diverged values frozen in; nothing below should ever see them
# again unless the composition is wrong.
#
# A3: a FRESH read, not cur_before_d's own editVersion -- obs7_setup_status
# above (set_settings) already wrote this workspace once since cur_before_d
# was captured, so that token is already stale by the time this PATCH runs.
obs7_restore_ev=$(ws_edit_version "$ws_id")
obs7_restore_status=$(http_json PATCH "$ADMIN_HOST" "/api/workspaces/${ws_id}" \
	"$(jq -cn --argjson s "$cur_before_d" --argjson ev "$obs7_restore_ev" '{settings: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}")
if [[ "$obs7_restore_status" != "200" ]]; then
	echo "FAIL  observation 7 setup: restore workspace settings after snapshotting p2b-d: want status 200, got ${obs7_restore_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

activate_d_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_d_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")

p2b_list_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
p2b_list_len=$(jq -r '.items | length' "$BODY_FILE")
if [[ "$activate_d_status" != "200" || "$p2b_list_status" != "200" || "$p2b_list_len" != "$m2_listsize" ]]; then
	echo "FAIL  observation 7 (basePath+listSize): activate status ${activate_d_status}; GET /widgets at the workspace's OWN basePath (\"\") want status 200 with ${m2_listsize} items (p2b-d's own listSize -- it IS composed), got status ${p2b_list_status} with ${p2b_list_len}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 7 (basePath+listSize): /widgets still answers at the workspace's own (empty) basePath, and the list is ${p2b_list_len} long -- p2b-d's listSize is composed, its basePath is not"
fi

check_preflight "observation 7 (cors): preflight reflects the WORKSPACE's cors (reflect), not p2b-d's (off)" \
	"$workspace_host" / "http://probe.example" "http://probe.example"

# notFoundBody is a STRUCTURAL guard, not a discriminating one (§G is
# explicit about this): serveNoRoute (routes.go) reads ws.Settings directly
# and no agent in this run owns that file, so no reachable implementation of
# THIS slice could make it composed even by mistake. Kept anyway -- it costs
# one check and it fails the day someone changes that file.
p2b_nope_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/p2b-nope-route")
if [[ "$p2b_nope_status" != "404" ]] || grep -qF 'p2b-scenario-404' "$BODY_FILE"; then
	echo "FAIL  observation 7 (notFoundBody): want status 404 without p2b-d's marker body, got status ${p2b_nope_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 7 (notFoundBody): the unknown-route 404 is the workspace's own (default) shape, not p2b-d's marker"
fi

http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null

echo "== observations 8, 9 (mock-plane surface): the unauthenticated {prefix}/state round trip =="

p1_ev=$(op_edit_version "$ws_id" "$detail_opkey")
p1_body=$(jq -n --argjson ev "$p1_ev" '{overrideOn: true, routeOff: false, responses: {
	"200": {mode: "generated", recipes: {name: {kind: "const", value: "P2B-STATE-P1"}}}
}, editVersion: $ev}')
http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$p1_body" -H "X-CSRF-Token: ${csrf}" >/dev/null

scn_e_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" '{"name":"p2b-e"}' -H "X-CSRF-Token: ${csrf}")
scn_e_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$scn_e_status" != "201" ]]; then
	echo "FAIL  observations 8/9 setup: create scenario p2b-e: want status 201, got ${scn_e_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

p2_ev=$(op_edit_version "$ws_id" "$detail_opkey")
p2_body=$(jq -n --argjson ev "$p2_ev" '{overrideOn: true, routeOff: false, responses: {
	"200": {mode: "generated", recipes: {name: {kind: "const", value: "P2B-STATE-P2"}}}
}, editVersion: $ev}')
http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$p2_body" -H "X-CSRF-Token: ${csrf}" >/dev/null

http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" >/dev/null
rev_before_state_on=$(jq -r '.revision' "$BODY_FILE")
state_on_body=$(jq -n '{scenario: "p2b-e"}')
state_on_status=$(http_json POST "$workspace_host" /__mocker/state "$state_on_body")
state_on_scenario=$(jq -r '.scenario' "$BODY_FILE")
state_on_rev=$(jq -r '.revision' "$BODY_FILE")
state_on_name=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}" | jq -r '.name')
want_rev_state_on=$((rev_before_state_on + 1))
if [[ "$state_on_status" != "200" || "$state_on_scenario" != "p2b-e" ||
	"$state_on_rev" != "$want_rev_state_on" || "$state_on_name" != "P2B-STATE-P1" ]]; then
	echo "FAIL  observation 8 (activate): POST {prefix}/state {\"scenario\":\"p2b-e\"} unauthenticated -- want status 200 scenario=p2b-e revision=${want_rev_state_on} name=P2B-STATE-P1, got status ${state_on_status} scenario=${state_on_scenario} revision=${state_on_rev} name='${state_on_name}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 8 (activate): the mock plane's own scenario switch writes to the workspaces table and takes effect -- revision ${rev_before_state_on} -> ${state_on_rev}"
fi

state_repeat_status=$(http_json POST "$workspace_host" /__mocker/state "$state_on_body")
state_repeat_rev=$(jq -r '.revision' "$BODY_FILE")
if [[ "$state_repeat_status" != "200" || "$state_repeat_rev" != "$state_on_rev" ]]; then
	echo "FAIL  observation 9 (mock-plane surface): re-POSTing the SAME scenario name should cost no write -- want revision ${state_on_rev} unchanged, got status ${state_repeat_status} revision ${state_repeat_rev}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 9 (mock-plane surface): the unauthenticated round trip is idempotent -- revision stays ${state_repeat_rev}, no rebuild loop a caller could drive"
fi

state_off_body=$(jq -n '{scenario: ""}')
state_off_status=$(http_json POST "$workspace_host" /__mocker/state "$state_off_body")
state_off_scenario=$(jq -r '.scenario' "$BODY_FILE")
state_off_rev=$(jq -r '.revision' "$BODY_FILE")
state_off_name=$(curl -s -H "Host: ${workspace_host}" "${BASE_URL}/widgets/${item_id}" | jq -r '.name')
want_rev_state_off=$((state_on_rev + 1))
if [[ "$state_off_status" != "200" || "$state_off_scenario" != "null" ||
	"$state_off_rev" != "$want_rev_state_off" || "$state_off_name" != "P2B-STATE-P2" ]]; then
	echo "FAIL  observation 8 (deactivate): POST {prefix}/state {\"scenario\":\"\"} -- want status 200 scenario=null revision=${want_rev_state_off} name=P2B-STATE-P2, got status ${state_off_status} scenario=${state_off_scenario} revision=${state_off_rev} name='${state_off_name}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 8 (deactivate): the empty-string scenario deactivates through the same unauthenticated route -- revision ${state_on_rev} -> ${state_off_rev}, back to P2B-STATE-P2"
fi

echo "== observation 10: 404s an all-404 handler could not fake, and observation 11: create while active -> 409 =="

reactivate_e_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_e_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")
active_e_rev=$(jq -r '.revision' "$BODY_FILE")
if [[ "$reactivate_e_status" != "200" ]]; then
	echo "FAIL  observation 10/11 setup: (re)activate p2b-e: want status 200, got ${reactivate_e_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

# 10a: an unknown NAME on the mock plane. Anything unknown under the reserved
# prefix already answers 404 with "not_implemented_yet" (plane.go's own
# serveReserved), so the wire CODE is what proves this handler actually
# looked the name up, not just that the status is 404.
unknown_name_body=$(jq -n '{scenario: "p2b-totally-unknown-name"}')
unknown_name_status=$(http_json POST "$workspace_host" /__mocker/state "$unknown_name_body")
unknown_name_code=$(jq -r '.error.code // empty' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" >/dev/null
post_unknown_scnid=$(jq -r '.scenarioId' "$BODY_FILE")
post_unknown_rev=$(jq -r '.revision' "$BODY_FILE")
if [[ "$unknown_name_status" != "404" || "$unknown_name_code" != "not_found" ||
	"$post_unknown_scnid" != "$scn_e_id" || "$post_unknown_rev" != "$active_e_rev" ]]; then
	echo "FAIL  observation 10a: an unknown scenario NAME on the mock plane -- want status 404 code=not_found (never not_implemented_yet) with NOTHING moved (scenarioId ${scn_e_id}, revision ${active_e_rev}), got status ${unknown_name_status} code '${unknown_name_code}' scenarioId=${post_unknown_scnid} revision=${post_unknown_rev}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 10a: an unknown name is a genuine 404 (code not_found) that changes nothing -- distinguishable from an all-404 handler that never looked anything up"
fi

# 10b: another workspace's scenario id (A8). A second workspace, its own
# spec, its own scenario -- then activate it BY ID against the FIRST
# workspace.
ws2_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p2b-bravo"}' -H "X-CSRF-Token: ${csrf}")
ws2_id=$(jq -r '.id' "$BODY_FILE")
ws2_ev=$(jq -r '.editVersion' "$BODY_FILE")
if [[ "$ws2_status" != "201" ]]; then
	echo "FAIL  observation 10b setup: create a second workspace: want status 201, got ${ws2_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi
# A3: a brand-new workspace's own edit_version starts at 1, not 0
# (workspaces.Repo.Create's own comment on why row-bootstrap is the one
# case that does not go through the allocator) -- ws2_ev above, taken
# straight off the CREATE response, is that value.
http_json PATCH "$ADMIN_HOST" "/api/workspaces/${ws2_id}" "$(jq -cn --argjson s "$spec_id" --argjson ev "$ws2_ev" '{specId: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}" >/dev/null
other_scn_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws2_id}/scenarios" '{"name":"p2b-elsewhere"}' -H "X-CSRF-Token: ${csrf}")
other_sid=$(jq -r '.id' "$BODY_FILE")
if [[ "$other_scn_status" != "201" || -z "$other_sid" || "$other_sid" == "null" ]]; then
	echo "FAIL  observation 10b setup: create a scenario in the second workspace: want status 201, got ${other_scn_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

cross_ws_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${other_sid}/activate" '{}' -H "X-CSRF-Token: ${csrf}")
cross_ws_code=$(jq -r '.error.code // empty' "$BODY_FILE")
if [[ "$cross_ws_status" != "404" || "$cross_ws_code" != "not_found" ]]; then
	echo "FAIL  observation 10b: activating another workspace's scenario id (A8): want status 404 code=not_found, got status ${cross_ws_status} code '${cross_ws_code}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 10b: a scenario id belonging to a DIFFERENT workspace 404s here too -- ownership is checked, not just existence"
fi

# Observation 11: creating a scenario with NO `from` field, while one is
# active -> 409, and the observable name list is unchanged. §G is explicit
# that whether a transient row was inserted-then-deleted is NOT observable
# from outside (the table has no AUTOINCREMENT, so even the next id proves
# nothing) -- so this only asserts the 409 and the name list, nothing more.
# P2d adds a SECOND way to create a scenario while one is active -- POST
# .../scenarios WITH `from` -- and that path SUCCEEDS on purpose (P2d's own
# section, observation 1): this body has no `from` key, so it is still the
# CreateFromCurrentState path this observation has always pinned, and A10's
# refusal was never about "any create while active", only that one.
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" >/dev/null
names_before_conflict=$(jq -cS '[.scenarios[].name] | sort' "$BODY_FILE")
create_conflict_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" '{"name":"p2b-blocked"}' -H "X-CSRF-Token: ${csrf}")
create_conflict_code=$(jq -r '.error.code // empty' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" >/dev/null
names_after_conflict=$(jq -cS '[.scenarios[].name] | sort' "$BODY_FILE")
if [[ "$create_conflict_status" != "409" || "$create_conflict_code" != "conflict" ||
	"$names_after_conflict" != "$names_before_conflict" ]]; then
	echo "FAIL  observation 11: creating a scenario while p2b-e is active (A10): want status 409 code=conflict and the name list unchanged, got status ${create_conflict_status} code '${create_conflict_code}', names before=${names_before_conflict} after=${names_after_conflict}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 11: creation with no from= is still refused with 409 while a scenario is active, and the observable name list is exactly what it was"
fi

http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null

echo "== observation 13: the auth layer (identity + signing) is observably the scenario's =="

orig_identity_name=$(jq -r '.identity.name' <<<"$p2b_orig_settings")
orig_identity_id=$(jq -r '.identity.id' <<<"$p2b_orig_settings")

scn_g_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios" '{"name":"p2b-auth"}' -H "X-CSRF-Token: ${csrf}")
scn_g_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$scn_g_status" != "201" ]]; then
	echo "FAIL  observation 13 setup: create scenario p2b-auth: want status 201, got ${scn_g_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

obs13_setup_status=$(set_settings '.identity.name = "P2b Changed Person" | .identity.id = 987654')
if [[ "$obs13_setup_status" != "200" ]]; then
	echo "FAIL  observation 13 setup: change the workspace's identity after snapshotting p2b-auth: want status 200, got ${obs13_setup_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

activate_g_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/${scn_g_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")

under_g_login=$(curl -s -X POST -H "Host: ${workspace_host}" "${BASE_URL}/auth/login")
under_g_token=$(jq -r '.token' <<<"$under_g_login")
IFS='.' read -r _ under_g_payload_seg _ <<<"$under_g_token"
under_g_payload=$(b64url_decode "$under_g_payload_seg")
under_g_name=$(jq -r '.name' <<<"$under_g_payload")
under_g_sub=$(jq -r '.sub' <<<"$under_g_payload")
if [[ "$activate_g_status" != "200" || "$under_g_name" != "$orig_identity_name" || "$under_g_sub" != "$orig_identity_id" ]]; then
	echo "FAIL  observation 13 (active): under p2b-auth, POST /auth/login should mint a token with the SCENARIO's identity (name='${orig_identity_name}' sub=${orig_identity_id}), got name='${under_g_name}' sub='${under_g_sub}' (activate status ${activate_g_status})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13 (active): the minted token's claims are p2b-auth's identity (name='${under_g_name}', sub=${under_g_sub}), not the workspace's changed one"
fi

http_json POST "$ADMIN_HOST" "/api/workspaces/${ws_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null
after_g_login=$(curl -s -X POST -H "Host: ${workspace_host}" "${BASE_URL}/auth/login")
after_g_token=$(jq -r '.token' <<<"$after_g_login")
IFS='.' read -r _ after_g_payload_seg _ <<<"$after_g_token"
after_g_payload=$(b64url_decode "$after_g_payload_seg")
after_g_name=$(jq -r '.name' <<<"$after_g_payload")
after_g_sub=$(jq -r '.sub' <<<"$after_g_payload")
if [[ "$after_g_name" != "P2b Changed Person" || "$after_g_sub" != "987654" ]]; then
	echo "FAIL  observation 13 (deactivated): back on the workspace's own layer, POST /auth/login should mint the CHANGED identity (name='P2b Changed Person' sub=987654), got name='${after_g_name}' sub='${after_g_sub}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13 (deactivated): deactivating flips the minted identity back to the workspace's own (now-changed) one -- the scenario really did replace the whole auth layer, not just some rows"
fi

# --------------------------------------------------------------------------
# P2b restore: put the shared workspace back exactly as this block found it
# -- the full settings object, detail_opkey's override document, and
# create_opkey's absence -- stated as its own assertion rather than a
# cleanup nobody checks (§G is explicit that a block silently leaving a
# scenario active turns the rest of this file into a different test).
# --------------------------------------------------------------------------
echo "== P2b restore: settings, detail_opkey, create_opkey, no scenario active =="

restore_settings_ev=$(ws_edit_version "$ws_id")
restore_settings_status=$(http_json PATCH "$ADMIN_HOST" "/api/workspaces/${ws_id}" \
	"$(jq -cn --argjson s "$p2b_orig_settings" --argjson ev "$restore_settings_ev" '{settings: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}")
restore_settings_now=$(jq -cS '.settings' "$BODY_FILE")
want_settings_now=$(jq -cS '.' <<<"$p2b_orig_settings")
if [[ "$restore_settings_status" != "200" || "$restore_settings_now" != "$want_settings_now" ]]; then
	echo "FAIL  P2b restore: settings do not match the pre-block baseline after PATCHing it back (status ${restore_settings_status})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2b restore: the workspace's full settings object is back to what it was before this block ran"
fi

# A3: p2b_detail_orig's OWN editVersion field is the baseline read from
# before this whole block wrote to detail_opkey five separate times (A, B,
# D, P1, P2) -- long stale by now. Read fresh and splice the CURRENT token
# into the resend, rather than the number this document happened to be
# captured with; the CONTENT comparison below then has to ignore editVersion
# on both sides too, since a write is expected to mint a new one even when
# every other field round-trips unchanged.
restore_detail_ev=$(op_edit_version "$ws_id" "$detail_opkey")
restore_detail_body=$(jq -c --argjson ev "$restore_detail_ev" '.editVersion = $ev' <<<"$p2b_detail_orig")
restore_detail_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" "$restore_detail_body" -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${detail_opkey}" >/dev/null
restore_detail_now=$(jq -cS 'del(.method, .path, .opKey, .updatedAt, .editVersion)' "$BODY_FILE")
want_detail_now=$(jq -cS 'del(.editVersion)' <<<"$p2b_detail_orig")
if [[ "$restore_detail_status" != "200" || "$restore_detail_now" != "$want_detail_now" ]]; then
	echo "FAIL  P2b restore: ${detail_opkey}'s override document does not match its pre-block state (PUT status ${restore_detail_status})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2b restore: ${detail_opkey} is back to the pinned-mode document the P1c-2 to-override conversion left it in"
fi

create_opkey_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}/operations/${create_opkey}")
if [[ "$create_opkey_status" != "404" ]]; then
	echo "FAIL  P2b restore: ${create_opkey} should be absent again (observation 3's own cleanup), got status ${create_opkey_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2b restore: ${create_opkey} is absent again -- the MCP block below still finds it un-overridden"
fi

final_ws_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}")
final_scnid=$(jq -r '.scenarioId' "$BODY_FILE")
if [[ "$final_ws_status" != "200" || "$final_scnid" != "null" ]]; then
	echo "FAIL  P2b restore: no scenario should be active at the end of this block, got status ${final_ws_status} scenarioId=${final_scnid}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2b restore: no scenario is active -- the shared workspace is exactly as this block found it"
fi

# --------------------------------------------------------------------------
# P2c: history and undo for the workspace layer (checkpoints, rollback,
# retention, reset-overrides) -- fourteen live observations against the
# real container, per p2c-context.md §G. This block creates and uses its
# OWN workspaces and never touches the shared $ws_id, $slug or
# $workspace_host: those three are read 13/3/6 more times below (the MCP
# block's own calls, then the path-mode tail), so writing any one of them
# here would silently redirect everything after this block at a workspace
# it knows nothing about. Every variable this block introduces is prefixed
# p2c_ for exactly that reason.
#
# Retention is MOCKER_CHECKPOINT_RETENTION=3 for this whole run (set in the
# preamble, before the stack ever came up) -- so, per §G's own standing
# rule, no observation below asserts a checkpoint COUNT delta except 5 and
# 12, each on its own fresh workspace where the trace is fully known; every
# other observation proves "a new, larger id appeared" instead. Before P2d
# that held because a correct implementation inserted exactly one
# machine-made (pre-destructive) row per rollback/reset-overrides call and
# pruned one once the cap was reached, so the total never moved. Since P2d
# it is no longer that clean: this block's own first labelled admin call
# against each workspace it creates (the spec-attaching PATCH, or the first
# PUT a p2c_pin_status call makes) ALSO writes one "auto" row -- the huge
# MOCKER_CHECKPOINT_DEBOUNCE this run's preamble sets is long enough that
# NO labelled call ever waits out the window on the wall clock, but it is
# NOT long enough to survive retention: the debounce's own memory IS that
# auto row (`autoWindowSuppressed` reads MAX(created_at) WHERE kind =
# 'auto' for the workspace, SIG-AUTO), and that row competes for the SAME
# three retention slots the pre-destructive rows do (the prune filter is
# `kind <> 'manual'`, not `kind = 'pre-destructive'`). Once enough
# pre-destructive churn on ONE workspace prunes that auto row away, the
# NEXT labelled call finds SQL NULL where it expects a timestamp and
# re-arms -- not because the window elapsed, but because its own marker
# was evicted by a policy that has nothing to do with time. Both fresh
# workspaces below churn enough pre-destructive rows to hit this exactly
# once: at $p2c_ws5_id, the first labelled call is the spec-attaching PATCH
# (observation 5's own setup), writing auto row A before M, O, P or Q
# exist; Q's insert prunes the non-manual population down to three and A --
# the lowest id of the four -- is the one that goes. The pin_status call
# right after the C7-exempt rollback to O is ALSO labelled, and by then A
# is already gone -- it re-arms and writes a second auto row, X, which
# prunes O and P at once and later gives up one more slot (this time Q's)
# to the final rollback's own pre-destructive row. $p2c_ws12_id repeats the
# same shape one script-loop later: its first auto row dies at reset 3, and
# reset 4's own pin_status re-arms and writes a second one that takes a
# retention slot away from what would otherwise have been reset 4's own
# clean pre-destructive row. Observations 5 and 12's own EXACT-population
# assertions price BOTH auto rows in, not just the first; nothing here is
# loosened to compensate for it -- if the arithmetic ever stops holding,
# the fix belongs in P2d's own section, never in a loosened assertion here.
# --------------------------------------------------------------------------
echo "== P2c: history and undo for the workspace layer, fourteen live observations =="

# p2c_pin_status PUTs an activeStatus-ONLY override onto opkey in workspace
# wid -- every observation below pins a distinct HTTP status code and reads
# it back off the mock host with a bare curl -w '%{http_code}', never a
# body compare, so a const-recipe override (P1c/P2b's own shape, used
# throughout the rest of this file) would be more machinery than any
# observation here needs. Echoes http_json's own status line, exactly like
# set_settings below does, so a caller can capture it the same way.
p2c_pin_status() {
	local wid=$1 opkey=$2 status_code=$3
	local body ev
	# A3: read this row's own editVersion first (0 via op_edit_version's own
	# 404 branch, the first time this opkey is ever touched) -- p2c_pin_status
	# is called repeatedly against the SAME opkey across this whole block, so
	# doing the read INSIDE the helper, every call, is what keeps every one
	# of those calls a genuine read-then-write instead of only the first.
	ev=$(op_edit_version "$wid" "$opkey")
	body=$(jq -n --argjson s "$status_code" --argjson ev "$ev" '{overrideOn:true, routeOff:false, activeStatus:$s, responses:{}, editVersion:$ev}')
	http_json PUT "$ADMIN_HOST" "/api/workspaces/${wid}/operations/${opkey}" "$body" -H "X-CSRF-Token: ${csrf}"
}

# p2c_set_settings is set_settings' (smoke.sh:1313) workspace-id-parameterised
# twin. That helper is hard-wired to the shared $ws_id, and this block's
# whole point is never touching it -- observation 8 needs the SAME
# read-current/patch-whole shape (handlePatchWorkspace replaces .Settings
# wholesale, no merge) against a workspace THIS block owns instead.
p2c_set_settings() {
	local wid=$1 filter=$2
	local cur new ev
	http_json GET "$ADMIN_HOST" "/api/workspaces/${wid}" >/dev/null
	cur=$(jq -c '.settings' "$BODY_FILE")
	ev=$(jq -r '.editVersion' "$BODY_FILE")
	new=$(jq -c "$filter" <<<"$cur")
	http_json PATCH "$ADMIN_HOST" "/api/workspaces/${wid}" \
		"$(jq -cn --argjson s "$new" --argjson ev "$ev" '{settings: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}"
}

# --------------------------------------------------------------------------
# Setup (unnumbered -- §G is explicit this is P1c behaviour, already pinned
# above line 1215, and passes with all of P2c deleted): a workspace this
# block owns end to end, on the fixture spec, with one operation pinned so
# every rollback below has something distinctive to restore. A failure here
# means the ground P2c stands on is broken, not P2c itself, so it exits
# immediately the same way the top-of-file login/import/attach steps do,
# rather than cascading into fourteen confusing downstream failures.
# --------------------------------------------------------------------------
p2c_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p2c-main"}' -H "X-CSRF-Token: ${csrf}")
if [[ "$p2c_ws_status" != "201" ]]; then
	echo "FAIL  P2c setup: create workspace: want status 201, got ${p2c_ws_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p2c_ws_id=$(jq -r '.id' "$BODY_FILE")
p2c_slug=$(jq -r '.slug' "$BODY_FILE")
p2c_ws_ev=$(jq -r '.editVersion' "$BODY_FILE")
p2c_host="${p2c_slug}.${WORKSPACE_HOST_BASE}"

# A3: a fresh workspace's own edit_version starts at 1 (ws2_ev's own
# comment above), taken straight off the CREATE response.
p2c_attach_status=$(http_json PATCH "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}" "$(jq -cn --argjson s "$spec_id" --argjson ev "$p2c_ws_ev" '{specId: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}")
if [[ "$p2c_attach_status" != "200" ]]; then
	echo "FAIL  P2c setup: attach the fixture spec to ${p2c_slug}: want status 200, got ${p2c_attach_status}: $(cat "$BODY_FILE")"
	exit 1
fi

p2c_pin_status "$p2c_ws_id" "$detail_opkey" 418 >/dev/null
p2c_setup_mock=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/widgets/${item_id}")
if [[ "$p2c_setup_mock" != "418" ]]; then
	echo "FAIL  P2c setup: pin ${detail_opkey} to 418 and observe it on the mock host: want 418, got ${p2c_setup_mock}"
	exit 1
fi
echo "PASS  P2c setup: fresh workspace ${p2c_slug} (id ${p2c_ws_id}), ${detail_opkey} pinned to 418"

# --------------------------------------------------------------------------
# Observation 1: a manual checkpoint is written, and revision does not move.
# --------------------------------------------------------------------------
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}" >/dev/null
p2c_rev_before_m1=$(jq -r '.revision' "$BODY_FILE")

p2c_m1_label="ручная точка M1"
p2c_create_m1_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" \
	"$(jq -cn --arg l "$p2c_m1_label" '{label: $l}')" -H "X-CSRF-Token: ${csrf}")
p2c_m1=$(jq -r '.id' "$BODY_FILE")
p2c_m1_kind=$(jq -r '.kind' "$BODY_FILE")
p2c_m1_label_got=$(jq -r '.label' "$BODY_FILE")
p2c_m1_created_by=$(jq -r '.createdBy' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_create_m1_status" != "201" || "$p2c_m1_kind" != "manual" || "$p2c_m1_label_got" != "$p2c_m1_label" ||
	-z "$p2c_m1" || "$p2c_m1" == "null" || -z "$p2c_m1_created_by" || "$p2c_m1_created_by" == "null" ]]; then
	echo "FAIL  observation 1: POST /checkpoints: want status 201 kind=manual label='${p2c_m1_label}' createdBy not null, got status ${p2c_create_m1_status} kind='${p2c_m1_kind}' label='${p2c_m1_label_got}' createdBy=${p2c_m1_created_by}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 1: manual checkpoint M1=${p2c_m1} created (kind manual, label echoed, createdBy=${p2c_m1_created_by})"
fi

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" >/dev/null
p2c_m1_listed=$(jq --argjson id "$p2c_m1" '[.checkpoints[] | select(.id == $id and .kind == "manual")] | length' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_m1_listed" != "1" ]]; then
	echo "FAIL  observation 1: GET /checkpoints does not list M1=${p2c_m1} as kind manual: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 1: GET /checkpoints lists M1=${p2c_m1}"
fi

p2c_health_after_m1=$(curl -s -H "Host: ${p2c_host}" "${BASE_URL}/__mocker/health" | jq -r '.revision')
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_health_after_m1" != "$p2c_rev_before_m1" ]]; then
	echo "FAIL  observation 1: a manual checkpoint bumped revision (C12) -- want ${p2c_rev_before_m1}, got ${p2c_health_after_m1}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 1: revision unchanged by a manual checkpoint (${p2c_health_after_m1})"
fi

p2c_put_402_status=$(p2c_pin_status "$p2c_ws_id" "$detail_opkey" 402)
p2c_mock_402=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/widgets/${item_id}")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_put_402_status" != "200" || "$p2c_mock_402" != "402" ]]; then
	echo "FAIL  observation 1: change the override to 402 AFTER M1 -- want PUT 200 and mock 402, got PUT ${p2c_put_402_status} mock ${p2c_mock_402}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 1: override changed to 402 after M1 (mock ${p2c_mock_402}) -- nothing below can distinguish the restored state without this"
fi

# --------------------------------------------------------------------------
# Observation 2: rollback allocates exactly max+1.
# --------------------------------------------------------------------------
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}" >/dev/null
p2c_rev_before_rollback1=$(jq -r '.revision' "$BODY_FILE")

p2c_rollback1_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/rollback/${p2c_m1}" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_rollback1_rev=$(jq -r '.revision' "$BODY_FILE")
p2c_want_rev1=$((p2c_rev_before_rollback1 + 1))
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_rollback1_status" != "200" || "$p2c_rollback1_rev" != "$p2c_want_rev1" ]]; then
	echo "FAIL  observation 2: POST /rollback/${p2c_m1}: want status 200 revision ${p2c_rev_before_rollback1} -> ${p2c_want_rev1} (+1 exactly), got status ${p2c_rollback1_status} revision ${p2c_rollback1_rev}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 2: rollback to M1 bumps revision exactly once (${p2c_rev_before_rollback1} -> ${p2c_rollback1_rev})"
fi

# --------------------------------------------------------------------------
# Observation 3: the mock serves the restored answer.
# --------------------------------------------------------------------------
p2c_mock_after_rollback1=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/widgets/${item_id}")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_mock_after_rollback1" != "418" ]]; then
	echo "FAIL  observation 3: mock host after rollback to M1: want 418, got ${p2c_mock_after_rollback1}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 3: mock host serves 418 again after rollback to M1"
fi

# --------------------------------------------------------------------------
# Observation 4: the rollback protected what it destroyed -- a
# pre-destructive row with a NEW, LARGER id appeared, and M1 is still
# listed. (No count assertion here: §G's standing rule reserves those for
# observations 5 and 12.)
# --------------------------------------------------------------------------
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" >/dev/null
p2c_p1=$(jq -r --argjson m1 "$p2c_m1" '[.checkpoints[] | select(.kind == "pre-destructive" and .id > $m1)] | max_by(.id) | .id // empty' "$BODY_FILE")
p2c_m1_still=$(jq --argjson m1 "$p2c_m1" '[.checkpoints[] | select(.id == $m1)] | length' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ -z "$p2c_p1" || "$p2c_m1_still" != "1" ]]; then
	echo "FAIL  observation 4: after rollback to M1=${p2c_m1}, want a pre-destructive row with id > M1 and M1 still listed, got pre-destructive candidate '${p2c_p1}' M1-listed=${p2c_m1_still}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 4: pre-destructive checkpoint P1=${p2c_p1} appeared (id > M1=${p2c_m1}), M1 still listed"
fi

# --------------------------------------------------------------------------
# Observation 5 (FRESH workspace -- retention exemption trace, C7). Pin a
# DISTINCT status at every step so each snapshot holds different bytes.
# --------------------------------------------------------------------------
p2c_ws5_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p2c-obs5"}' -H "X-CSRF-Token: ${csrf}")
p2c_ws5_id=$(jq -r '.id' "$BODY_FILE")
p2c_ws5_slug=$(jq -r '.slug' "$BODY_FILE")
p2c_ws5_ev=$(jq -r '.editVersion' "$BODY_FILE")
p2c_ws5_host="${p2c_ws5_slug}.${WORKSPACE_HOST_BASE}"
http_json PATCH "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}" "$(jq -cn --argjson s "$spec_id" --argjson ev "$p2c_ws5_ev" '{specId: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}" >/dev/null
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_ws5_status" != "201" || -z "$p2c_ws5_id" || "$p2c_ws5_id" == "null" ]]; then
	echo "FAIL  observation 5 setup: create the fresh workspace: want status 201, got ${p2c_ws5_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 5 setup: fresh workspace ${p2c_ws5_slug} (id ${p2c_ws5_id})"
fi

# p2c_mm_ids reads the machine-made (pre-destructive) checkpoint ids off
# whatever GET .../checkpoints response is currently sitting in $BODY_FILE,
# sorted and JSON-compact. Comparing two such strings is exact-set equality
# without a single grep/tr/wc pipeline for `set -o pipefail` to trip on --
# `grep -c` on zero matches exits 1, and this script never gets to fold
# that into a bare command-substitution assignment.
p2c_mm_ids() {
	jq -cS '[.checkpoints[] | select(.kind == "pre-destructive") | .id] | sort' "$BODY_FILE"
}

p2c_pin_status "$p2c_ws5_id" "$detail_opkey" 401 >/dev/null
http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/checkpoints" '{"label":"ручная точка M"}' -H "X-CSRF-Token: ${csrf}" >/dev/null
p2c_m5=$(jq -r '.id' "$BODY_FILE")

p2c_pin_status "$p2c_ws5_id" "$detail_opkey" 402 >/dev/null
p2c_rollback_m_1=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/rollback/${p2c_m5}" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_mock_after_o=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_ws5_host}" "${BASE_URL}/widgets/${item_id}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/checkpoints" >/dev/null
p2c_o=$(jq -r '[.checkpoints[] | select(.kind == "pre-destructive")] | max_by(.id) | .id' "$BODY_FILE")

p2c_pin_status "$p2c_ws5_id" "$detail_opkey" 403 >/dev/null
p2c_rollback_m_2=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/rollback/${p2c_m5}" '{}' -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/checkpoints" >/dev/null
p2c_p=$(jq -r --argjson o "$p2c_o" '[.checkpoints[] | select(.kind == "pre-destructive" and .id != $o)] | max_by(.id) | .id' "$BODY_FILE")

p2c_pin_status "$p2c_ws5_id" "$detail_opkey" 404 >/dev/null
p2c_rollback_m_3=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/rollback/${p2c_m5}" '{}' -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/checkpoints" >/dev/null
p2c_q=$(jq -r --argjson o "$p2c_o" --argjson p "$p2c_p" '[.checkpoints[] | select(.kind == "pre-destructive" and .id != $o and .id != $p)] | max_by(.id) | .id' "$BODY_FILE")

p2c_mm_opq=$(p2c_mm_ids)
p2c_want_opq=$(jq -cn --argjson o "$p2c_o" --argjson p "$p2c_p" --argjson q "$p2c_q" '[$o,$p,$q] | sort')
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_rollback_m_1" != "200" || "$p2c_rollback_m_2" != "200" || "$p2c_rollback_m_3" != "200" ||
	"$p2c_mock_after_o" != "401" || "$p2c_mm_opq" != "$p2c_want_opq" ]]; then
	echo "FAIL  observation 5: pin 402/rollback, pin 403/rollback, pin 404/rollback -- all three to M=${p2c_m5} -- want three 200s, mock 401 right after the first (M's own captured state), and the machine-made population exactly {O,P,Q}=${p2c_want_opq}, got rollback statuses ${p2c_rollback_m_1}/${p2c_rollback_m_2}/${p2c_rollback_m_3}, mock ${p2c_mock_after_o}, population ${p2c_mm_opq}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 5: three rollbacks to M each wrote a fresh pre-destructive row; retention holds the population at exactly {O,P,Q}=${p2c_want_opq}"
fi

p2c_rollback_o_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/rollback/${p2c_o}" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_mock_after_rollback_o=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_ws5_host}" "${BASE_URL}/widgets/${item_id}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/checkpoints" >/dev/null
p2c_r=$(jq -r --argjson o "$p2c_o" --argjson p "$p2c_p" --argjson q "$p2c_q" \
	'[.checkpoints[] | select(.kind == "pre-destructive" and .id != $o and .id != $p and .id != $q)] | max_by(.id) | .id' "$BODY_FILE")
p2c_mm_opqr=$(p2c_mm_ids)
p2c_want_opqr=$(jq -cn --argjson o "$p2c_o" --argjson p "$p2c_p" --argjson q "$p2c_q" --argjson r "$p2c_r" '[$o,$p,$q,$r] | sort')
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_rollback_o_status" != "200" || "$p2c_mock_after_rollback_o" != "402" || "$p2c_mm_opqr" != "$p2c_want_opqr" ]]; then
	echo "FAIL  observation 5: rolling back to the OLDEST machine-made row O=${p2c_o} -- want status 200, mock 402 (O's own captured state), and population {O,P,Q,R}=${p2c_want_opqr} (C7's target exemption, N+1 rows for this one rollback), got status ${p2c_rollback_o_status} mock ${p2c_mock_after_rollback_o} population ${p2c_mm_opqr}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 5: rollback to the oldest machine-made row O serves 402 and C7 grants it an exemption -- population is {O,P,Q,R}=${p2c_want_opqr}"
fi

# This fifth p2c_pin_status is ALSO a labelled call (PUT .../operations),
# and by now the ORIGINAL auto row A is long gone -- pruned at Q's insert
# above. [checkpoints.Repo.Auto]'s debounce memory is [checkpoints.Repo]'s
# OWN row, not a separate clock: with no kind='auto' row left for this
# workspace, `autoWindowSuppressed` reads SQL NULL from its own MAX(...)
# query and reports "never suppressed" regardless of how young the window
# still is (SIG-AUTO, autoWindowSuppressed's own doc comment) -- so THIS
# call re-arms and writes a SECOND auto row, X, which was never priced into
# the "exactly one per workspace" comment above this whole block. X
# competes for the SAME three retention slots O/P/Q/R do (the prune filter
# is `kind <> 'manual'`): inserting X immediately prunes the population of
# five ({O,P,Q,R,X}) down to the newest three, {Q,R,X} -- O AND P both go
# at once, not one at a time.
p2c_pin_status "$p2c_ws5_id" "$detail_opkey" 405 >/dev/null
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/checkpoints" >/dev/null
p2c_x=$(jq -r --argjson o "$p2c_o" --argjson p "$p2c_p" --argjson q "$p2c_q" --argjson r "$p2c_r" \
	'[.checkpoints[] | select(.kind == "auto" and .id != $o and .id != $p and .id != $q and .id != $r)] | max_by(.id) | .id // empty' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ -z "$p2c_x" ]]; then
	echo "FAIL  observation 5: the fifth pin_status (a labelled PUT) should re-arm the debounce and write a fresh auto row X, now that A was pruned at Q's insert -- found none"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 5: pin_status re-armed the debounce and wrote auto row X=${p2c_x} -- A's eviction, not the wall clock, is what let it back in"
fi

# X's insert already pruned O and P (above). This final rollback to the
# MANUAL target M grants no C7 exemption and writes its own pre-destructive
# row, S -- population before S is {Q,R,X}=3, exactly at the cap, so S's
# insert prunes ONE more: Q, the oldest of the four. Final population is
# {R,X,S} -- kind = pre-destructive only among those is {R,S}, TWO rows,
# not three: X occupies one of the three retention slots without being
# pre-destructive itself, which is exactly why this count moved from what
# a build with no debounce trigger would have left behind.
p2c_rollback_m_final=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/rollback/${p2c_m5}" '{}' -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws5_id}/checkpoints" >/dev/null
p2c_mm_final=$(p2c_mm_ids)
p2c_mm_final_count=$(jq '[.checkpoints[] | select(.kind == "pre-destructive")] | length' "$BODY_FILE")
p2c_o_gone=$(jq --argjson o "$p2c_o" '[.checkpoints[] | select(.id == $o)] | length' "$BODY_FILE")
p2c_p_gone=$(jq --argjson p "$p2c_p" '[.checkpoints[] | select(.id == $p)] | length' "$BODY_FILE")
p2c_q_gone=$(jq --argjson q "$p2c_q" '[.checkpoints[] | select(.id == $q)] | length' "$BODY_FILE")
p2c_r_present=$(jq --argjson r "$p2c_r" '[.checkpoints[] | select(.id == $r)] | length' "$BODY_FILE")
# Guarded, unlike the reads above it: $p2c_x comes from a `.id // empty`
# query, so it is bash-empty exactly when the re-arm this block checks did
# NOT happen -- and `jq --argjson x ""` exits non-zero on invalid JSON,
# which under this script's `set -euo pipefail` aborts the WHOLE run at this
# line. A regression in the debounce re-arm would then take every later
# observation with it instead of printing one FAIL. Every other `// empty`
# variable in this file is only ever used in a `-z` test or interpolated
# into a message; this is the one place that feeds one to a later jq.
p2c_x_present=0
if [[ -n "$p2c_x" ]]; then
	p2c_x_present=$(jq --argjson x "$p2c_x" '[.checkpoints[] | select(.id == $x and .kind == "auto")] | length' "$BODY_FILE")
fi
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_rollback_m_final" != "200" || "$p2c_mm_final_count" != "2" || "$p2c_o_gone" != "0" ||
	"$p2c_p_gone" != "0" || "$p2c_q_gone" != "0" || "$p2c_r_present" != "1" || "$p2c_x_present" != "1" ]]; then
	echo "FAIL  observation 5: rolling back to the MANUAL target M=${p2c_m5} grants NO exemption -- want status 200, exactly 2 pre-destructive rows with O=${p2c_o}, P=${p2c_p} and Q=${p2c_q} all gone, R=${p2c_r} spared, auto row X=${p2c_x} still present, got status ${p2c_rollback_m_final} population ${p2c_mm_final}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 5: rollback to the manual target M prunes back to exactly 2 pre-destructive rows -- O, P and Q are all gone, the auto row X survives alongside them, population is now ${p2c_mm_final}"
fi

# --------------------------------------------------------------------------
# Observation 6 (main workspace): a custom endpoint present on both sides of
# a rollback keeps its id -- C1's delete-then-upsert, never
# truncate-and-reinsert. X is created LAST, on a SECOND workspace, per §G's
# own warning: custom_endpoints.id has no AUTOINCREMENT, so without a row
# holding a HIGHER rowid sitting outside this restore's delete set, SQLite
# hands a re-inserted E back the very id it already had and this
# observation passes against the implementation it exists to fail.
# --------------------------------------------------------------------------
p2c_ep_e_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/endpoints" \
	'{"method":"GET","path":"/p2c-e","status":200,"body":{"marker":"p2c-e"}}' -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/endpoints" >/dev/null
p2c_ep_e=$(jq -r '.endpoints[] | select(.path == "/p2c-e") | .id' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_ep_e_status" != "201" || -z "$p2c_ep_e" || "$p2c_ep_e" == "null" ]]; then
	echo "FAIL  observation 6 setup: create custom endpoint E (/p2c-e): want status 201, got ${p2c_ep_e_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 6 setup: custom endpoint E created, id ${p2c_ep_e} (GET /endpoints' own report)"
fi

p2c_m2_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" '{"label":"ручная точка M2, E ещё жив"}' -H "X-CSRF-Token: ${csrf}")
p2c_m2=$(jq -r '.id' "$BODY_FILE")

p2c_ep_f_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/endpoints" \
	'{"method":"GET","path":"/p2c-f","status":200,"body":{"marker":"p2c-f"}}' -H "X-CSRF-Token: ${csrf}")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_m2_status" != "201" || -z "$p2c_m2" || "$p2c_m2" == "null" || "$p2c_ep_f_status" != "201" ]]; then
	echo "FAIL  observation 6 setup: take M2 (E present) then create F (/p2c-f): want checkpoint status 201 and endpoint status 201, got checkpoint ${p2c_m2_status} endpoint ${p2c_ep_f_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 6 setup: M2=${p2c_m2} taken with E present, then F created"
fi

p2c_ws6_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p2c-obs6-x"}' -H "X-CSRF-Token: ${csrf}")
p2c_ws6_id=$(jq -r '.id' "$BODY_FILE")
p2c_ep_x_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws6_id}/endpoints" \
	'{"method":"GET","path":"/p2c-x","status":200,"body":{"marker":"p2c-x"}}' -H "X-CSRF-Token: ${csrf}")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_ws6_status" != "201" || "$p2c_ep_x_status" != "201" ]]; then
	echo "FAIL  observation 6 setup: create X on a SECOND workspace (after E, M2, F -- order matters): want workspace status 201 and endpoint status 201, got ${p2c_ws6_status} / ${p2c_ep_x_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 6 setup: X created on a second workspace (id ${p2c_ws6_id}), holding a rowid above E's and F's"
fi

http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/rollback/${p2c_m2}" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null
p2c_f_after_status=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/p2c-f")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/endpoints" >/dev/null
p2c_e_id_after=$(jq -r '.endpoints[] | select(.path == "/p2c-e") | .id' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_f_after_status" != "404" || "$p2c_e_id_after" != "$p2c_ep_e" ]]; then
	echo "FAIL  observation 6: rollback to M2 -- want F (/p2c-f) gone (404) and E's id unchanged (${p2c_ep_e}), got F status ${p2c_f_after_status} E id '${p2c_e_id_after}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 6: F is gone, E is listed with its ORIGINAL id ${p2c_ep_e} unchanged -- restore is delete-then-upsert on the natural key, not truncate-and-reinsert"
fi

# --------------------------------------------------------------------------
# Observation 7: a custom endpoint deleted then restored by rollback comes
# back serving.
# --------------------------------------------------------------------------
p2c_ep_g_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/endpoints" \
	'{"method":"GET","path":"/p2c-g","status":200,"body":{"marker":"p2c-g-alive"}}' -H "X-CSRF-Token: ${csrf}")
p2c_ep_g=$(jq -r '.id' "$BODY_FILE")
p2c_g_before=$(curl -s -H "Host: ${p2c_host}" "${BASE_URL}/p2c-g" | jq -r '.marker // empty')
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_ep_g_status" != "201" || "$p2c_g_before" != "p2c-g-alive" ]]; then
	echo "FAIL  observation 7 setup: create custom endpoint G (/p2c-g) and observe it: want status 201 marker p2c-g-alive, got status ${p2c_ep_g_status} marker '${p2c_g_before}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 7 setup: G created (id ${p2c_ep_g}) and serving"
fi

p2c_m3_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" '{"label":"ручная точка M3, G ещё жив"}' -H "X-CSRF-Token: ${csrf}")
p2c_m3=$(jq -r '.id' "$BODY_FILE")

p2c_del_g_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/endpoints/${p2c_ep_g}" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_g_after_delete=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/p2c-g")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_m3_status" != "201" || ("$p2c_del_g_status" != "200" && "$p2c_del_g_status" != "204") || "$p2c_g_after_delete" != "404" ]]; then
	echo "FAIL  observation 7: take M3=${p2c_m3} then DELETE G -- want checkpoint 201, delete 200/204, mock 404, got checkpoint ${p2c_m3_status} delete ${p2c_del_g_status} mock ${p2c_g_after_delete}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 7: M3=${p2c_m3} taken, G deleted, mock 404s it"
fi

http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/rollback/${p2c_m3}" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null
p2c_g_after_rollback=$(curl -s -H "Host: ${p2c_host}" "${BASE_URL}/p2c-g" | jq -r '.marker // empty')
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_g_after_rollback" != "p2c-g-alive" ]]; then
	echo "FAIL  observation 7: rollback to M3 should restore G serving -- want marker p2c-g-alive, got '${p2c_g_after_rollback}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 7: rollback to M3 brings G back serving -- a restore that only snapshotted overrides would leave it 404"
fi

# --------------------------------------------------------------------------
# Observation 8: settings are restored WHOLESALE, not merged. notFoundBody
# is the one top-level settings field that is ABSENT from the JSON when
# unset (omitempty) -- the only field whose snapshot value can differ from
# its live value by being absent, so it is what catches a merge that a
# listSize/signingKey compare alone would not.
# --------------------------------------------------------------------------
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}" >/dev/null
p2c_base_listsize=$(jq -r '.settings.listSize' "$BODY_FILE")
p2c_listsize_1=$((p2c_base_listsize + 3))
p2c_listsize_2=$((p2c_base_listsize + 7))

p2c_set8a_status=$(p2c_set_settings "$p2c_ws_id" ".listSize = ${p2c_listsize_1}")
p2c_k1=$(jq -r '.settings.auth.signingKey' "$BODY_FILE")
p2c_list_len_1=$(curl -s -H "Host: ${p2c_host}" "${BASE_URL}/widgets" | jq -r '.items | length')
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_set8a_status" != "200" || "$p2c_list_len_1" != "$p2c_listsize_1" || -z "$p2c_k1" || "$p2c_k1" == "null" ]]; then
	echo "FAIL  observation 8 setup: set listSize=${p2c_listsize_1} and read K1/observe the generated list -- want PATCH 200 length ${p2c_listsize_1} K1 non-empty, got status ${p2c_set8a_status} length ${p2c_list_len_1} K1='${p2c_k1}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 8 setup: listSize=${p2c_listsize_1}, K1 captured, generated list length ${p2c_list_len_1}"
fi

p2c_m4_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" '{"label":"ручная точка M4"}' -H "X-CSRF-Token: ${csrf}")
p2c_m4=$(jq -r '.id' "$BODY_FILE")

p2c_set8b_status=$(p2c_set_settings "$p2c_ws_id" \
	".listSize = ${p2c_listsize_2} | .auth.signingKey = \"p2c-k2-signing-key-marker\" | .notFoundBody = {marker: \"p2c-404-marker\"}")
p2c_list_len_2=$(curl -s -H "Host: ${p2c_host}" "${BASE_URL}/widgets" | jq -r '.items | length')
p2c_404_marked=$(curl -s -H "Host: ${p2c_host}" "${BASE_URL}/p2c-totally-unrouted" | jq -r '.marker // empty')
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_m4_status" != "201" || "$p2c_set8b_status" != "200" || "$p2c_list_len_2" != "$p2c_listsize_2" || "$p2c_404_marked" != "p2c-404-marker" ]]; then
	echo "FAIL  observation 8 setup: M4=${p2c_m4} then change listSize/signingKey/notFoundBody -- want M4 201, PATCH 200, list length ${p2c_listsize_2}, marker 404 body, got M4 ${p2c_m4_status} PATCH ${p2c_set8b_status} length ${p2c_list_len_2} marker '${p2c_404_marked}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 8 setup: M4=${p2c_m4} taken, then listSize/signingKey/notFoundBody all diverged (K2, marker 404 body active)"
fi

http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/rollback/${p2c_m4}" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null
p2c_list_len_after=$(curl -s -H "Host: ${p2c_host}" "${BASE_URL}/widgets" | jq -r '.items | length')
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}" >/dev/null
p2c_signingkey_after=$(jq -r '.settings.auth.signingKey' "$BODY_FILE")
p2c_404_after=$(curl -s -H "Host: ${p2c_host}" "${BASE_URL}/p2c-totally-unrouted")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_list_len_after" != "$p2c_listsize_1" || "$p2c_signingkey_after" != "$p2c_k1" || "$p2c_404_after" == *"p2c-404-marker"* ]]; then
	echo "FAIL  observation 8: rollback to M4 -- want list length ${p2c_listsize_1}, signingKey back to K1, DEFAULT 404 body (no marker), got length ${p2c_list_len_after} signingKey='${p2c_signingkey_after}' body='${p2c_404_after}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 8: rollback restores settings WHOLESALE -- list length back to ${p2c_listsize_1}, signingKey back to K1, notFoundBody back to the default (never merged with the marker)"
fi

# --------------------------------------------------------------------------
# Observation 9: reset-overrides removes BOTH kinds of edit, protected by a
# NEW, LARGER pre-destructive checkpoint id (no count assertion -- §G's
# standing rule).
# --------------------------------------------------------------------------
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" >/dev/null
p2c_newest_before_reset1=$(jq -r '[.checkpoints[].id] | max' "$BODY_FILE")

p2c_reset1_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/reset-overrides" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_reset1_changed=$(jq -r '.changed' "$BODY_FILE")
p2c_mock_after_reset1=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/widgets/${item_id}")
p2c_g_after_reset1=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/p2c-g")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" >/dev/null
p2c_pd_after_reset1=$(jq --argjson n "$p2c_newest_before_reset1" '[.checkpoints[] | select(.kind == "pre-destructive" and .id > $n)] | length' "$BODY_FILE")
p2c_reset1_cp=$(jq -r --argjson n "$p2c_newest_before_reset1" '[.checkpoints[] | select(.kind == "pre-destructive" and .id > $n)] | max_by(.id) | .id // empty' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_reset1_status" != "200" || "$p2c_reset1_changed" != "true" || "$p2c_mock_after_reset1" != "200" ||
	"$p2c_g_after_reset1" != "404" || "$p2c_pd_after_reset1" != "1" ]]; then
	echo "FAIL  observation 9: POST /reset-overrides with a pinned override and a live custom endpoint -- want status 200 changed=true, the pinned status gone (200, spec-generated), G 404, exactly one new pre-destructive row (id > ${p2c_newest_before_reset1}); got status ${p2c_reset1_status} changed=${p2c_reset1_changed} mock ${p2c_mock_after_reset1} endpoint ${p2c_g_after_reset1} new-pd-rows ${p2c_pd_after_reset1}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 9: reset-overrides cleared the pinned status AND the custom endpoint, protected by new pre-destructive checkpoint ${p2c_reset1_cp}"
fi

# --------------------------------------------------------------------------
# Observation 10: that reset is undoable.
# --------------------------------------------------------------------------
http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/rollback/${p2c_reset1_cp}" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null
p2c_mock_after_undo_reset1=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/widgets/${item_id}")
p2c_g_after_undo_reset1=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/p2c-g")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_mock_after_undo_reset1" != "418" || "$p2c_g_after_undo_reset1" != "200" ]]; then
	echo "FAIL  observation 10: rollback to the pre-reset checkpoint ${p2c_reset1_cp} -- want the pinned status back (418) and G (/p2c-g) serving again (200), got mock ${p2c_mock_after_undo_reset1} endpoint ${p2c_g_after_undo_reset1}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 10: rollback to the pre-reset checkpoint restores BOTH the pinned status and the custom endpoint"
fi

# --------------------------------------------------------------------------
# Observation 11: a reset that would delete nothing changes nothing.
# --------------------------------------------------------------------------
p2c_reset11a_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/reset-overrides" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_reset11a_changed=$(jq -r '.changed' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_reset11a_status" != "200" || "$p2c_reset11a_changed" != "true" ]]; then
	echo "FAIL  observation 11 setup: the workspace was restored by observation 10, so this reset must be genuinely destructive -- want status 200 changed=true, got status ${p2c_reset11a_status} changed=${p2c_reset11a_changed}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 11 setup: the first reset is destructive again (changed=true) -- the immediate second call below is the actual no-op under test"
fi

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" >/dev/null
p2c_newest_before_noop=$(jq -r '[.checkpoints[].id] | max' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}" >/dev/null
p2c_rev_before_noop=$(jq -r '.revision' "$BODY_FILE")

p2c_reset11b_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/reset-overrides" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_reset11b_changed=$(jq -r '.changed' "$BODY_FILE")
p2c_reset11b_rev=$(jq -r '.revision' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" >/dev/null
p2c_newest_after_noop=$(jq -r '[.checkpoints[].id] | max' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_reset11b_status" != "200" || "$p2c_reset11b_changed" != "false" ||
	"$p2c_reset11b_rev" != "$p2c_rev_before_noop" || "$p2c_newest_after_noop" != "$p2c_newest_before_noop" ]]; then
	echo "FAIL  observation 11: an immediate second reset-overrides -- want status 200 changed=false, revision unchanged (${p2c_rev_before_noop}), newest checkpoint unchanged (${p2c_newest_before_noop}), got status ${p2c_reset11b_status} changed=${p2c_reset11b_changed} revision ${p2c_reset11b_rev} newest ${p2c_newest_after_noop}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 11: the no-op reset answers changed=false and touches neither the revision nor the checkpoint history"
fi

# --------------------------------------------------------------------------
# Observation 12 (FRESH workspace -- retention prunes the machine-made kind
# only, spares the manual row, and reads MOCKER_CHECKPOINT_RETENTION=3).
# --------------------------------------------------------------------------
p2c_ws12_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p2c-obs12"}' -H "X-CSRF-Token: ${csrf}")
p2c_ws12_id=$(jq -r '.id' "$BODY_FILE")
p2c_ws12_ev=$(jq -r '.editVersion' "$BODY_FILE")
http_json PATCH "$ADMIN_HOST" "/api/workspaces/${p2c_ws12_id}" "$(jq -cn --argjson s "$spec_id" --argjson ev "$p2c_ws12_ev" '{specId: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}" >/dev/null

p2c_manual12_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws12_id}/checkpoints" '{"label":"именованный, ретеншн не трогает"}' -H "X-CSRF-Token: ${csrf}")
p2c_manual12=$(jq -r '.id' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_ws12_status" != "201" || "$p2c_manual12_status" != "201" || -z "$p2c_manual12" || "$p2c_manual12" == "null" ]]; then
	echo "FAIL  observation 12 setup: fresh workspace and a manual checkpoint -- want workspace 201 checkpoint 201, got ${p2c_ws12_status} / ${p2c_manual12_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 12 setup: fresh workspace ${p2c_ws12_id}, manual checkpoint ${p2c_manual12}"
fi

# Four genuinely destructive resets: each needs something to delete, so the
# operation is pinned again before every call -- that pin is exactly what
# makes the reset destructive (changed=true) rather than C9's no-op.
declare -a p2c_reset12_statuses=() p2c_reset12_changes=() p2c_reset12_ids=()
for p2c_status_code in 411 412 413 414; do
	p2c_pin_status "$p2c_ws12_id" "$detail_opkey" "$p2c_status_code" >/dev/null
	p2c_reset12_statuses+=("$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws12_id}/reset-overrides" '{}' -H "X-CSRF-Token: ${csrf}")")
	p2c_reset12_changes+=("$(jq -r '.changed' "$BODY_FILE")")
	http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws12_id}/checkpoints" >/dev/null
	p2c_reset12_ids+=("$(jq -r '[.checkpoints[] | select(.kind == "pre-destructive") | .id] | max' "$BODY_FILE")")
done
p2c_checks=$((p2c_checks + 1))
if [[ "${p2c_reset12_statuses[*]}" != "200 200 200 200" || "${p2c_reset12_changes[*]}" != "true true true true" ]]; then
	echo "FAIL  observation 12 setup: four genuinely destructive resets -- want all four status 200 changed=true, got statuses '${p2c_reset12_statuses[*]}' changed '${p2c_reset12_changes[*]}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 12 setup: four destructive resets ran, each writing a pre-destructive checkpoint (${p2c_reset12_ids[*]})"
fi

# The workspace's own first labelled call (the spec-attaching PATCH above)
# wrote one auto row, and it rides out the first two resets unpruned (the
# non-manual population only reaches 3 at reset 2). Reset 3 is what pushes
# it to 4 and prunes that auto row (the oldest of the four, same shape as
# observation 5's A). That leaves reset 4's own pin_status -- itself a
# labelled call -- with no auto row left to find: `autoWindowSuppressed`
# reads SQL NULL for this workspace and reports "never suppressed" (same
# mechanism as observation 5's X), so it writes a FRESH auto row that takes
# one of the three retention slots reset 4's own pre-destructive insert
# would otherwise have kept. The pre-destructive-only count therefore lands
# on two, not three -- the third slot is occupied by that re-armed auto row.
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws12_id}/checkpoints" >/dev/null
p2c_pd12_count=$(jq '[.checkpoints[] | select(.kind == "pre-destructive")] | length' "$BODY_FILE")
p2c_oldest12_listed=$(jq --argjson id "${p2c_reset12_ids[0]}" '[.checkpoints[] | select(.id == $id)] | length' "$BODY_FILE")
p2c_manual12_listed=$(jq --argjson id "$p2c_manual12" '[.checkpoints[] | select(.id == $id and .kind == "manual")] | length' "$BODY_FILE")
p2c_auto12_count=$(jq '[.checkpoints[] | select(.kind == "auto")] | length' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_pd12_count" != "2" || "$p2c_oldest12_listed" != "0" || "$p2c_manual12_listed" != "1" || "$p2c_auto12_count" != "1" ]]; then
	echo "FAIL  observation 12: after four destructive resets at MOCKER_CHECKPOINT_RETENTION=3 -- want exactly 2 pre-destructive rows (the debounce re-arms on reset 4's pin_status once its first auto row is pruned, and that fresh auto row takes the third retention slot), the OLDEST of the four (id ${p2c_reset12_ids[0]}) pruned, manual checkpoint still listed, exactly one auto row surviving; got ${p2c_pd12_count} pre-destructive rows, oldest still listed=${p2c_oldest12_listed}, manual listed=${p2c_manual12_listed}, auto rows ${p2c_auto12_count}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 12: retention pruned to exactly 2 pre-destructive rows plus the re-armed auto row -- the oldest (id ${p2c_reset12_ids[0]}) is gone, and the manual checkpoint survives untouched"
fi

# --------------------------------------------------------------------------
# Observation 13 (main workspace -- state has no overrides left, per
# observation 11's own ending): rollback (and reset) under an active
# scenario succeed, are partly visible, and say so via scenarioActive.
# L moves AFTER C is taken so a rollback writing nothing at all would still
# be caught by the L=402 assertion alone.
# --------------------------------------------------------------------------
p2c_pin_status "$p2c_ws_id" "$detail_opkey" 418 >/dev/null
p2c_scn_s_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/scenarios" '{"name":"p2c-s"}' -H "X-CSRF-Token: ${csrf}")
p2c_scn_s_id=$(jq -r '.id' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_scn_s_status" != "201" || -z "$p2c_scn_s_id" || "$p2c_scn_s_id" == "null" ]]; then
	echo "FAIL  observation 13 setup: pin K=418 then create scenario S -- want status 201, got ${p2c_scn_s_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13 setup: K pinned to 418, scenario S=${p2c_scn_s_id} saved holding K and nothing else"
fi

p2c_pin_status "$p2c_ws_id" "$list_opkey" 402 >/dev/null
p2c_pin_status "$p2c_ws_id" "$detail_opkey" 409 >/dev/null
p2c_c_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/checkpoints" '{"label":"ручная точка C"}' -H "X-CSRF-Token: ${csrf}")
p2c_c=$(jq -r '.id' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_c_status" != "201" || -z "$p2c_c" || "$p2c_c" == "null" ]]; then
	echo "FAIL  observation 13 setup: pin L=402, change K to 409, take checkpoint C -- want status 201, got ${p2c_c_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13 setup: L=402, K=409, checkpoint C=${p2c_c} taken"
fi

p2c_pin_status "$p2c_ws_id" "$detail_opkey" 410 >/dev/null
p2c_pin_status "$p2c_ws_id" "$list_opkey" 403 >/dev/null
http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/scenarios/${p2c_scn_s_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null

p2c_rollback_c_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/rollback/${p2c_c}" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_rollback_c_active=$(jq -r '.scenarioActive' "$BODY_FILE")
p2c_mock_k_after_c=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/widgets/${item_id}")
p2c_mock_l_after_c=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p2c_host}" "${BASE_URL}/widgets")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}" >/dev/null
p2c_scnid_after_c=$(jq -r '.scenarioId' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_rollback_c_status" != "200" || "$p2c_rollback_c_active" != "true" ||
	"$p2c_mock_k_after_c" != "418" || "$p2c_mock_l_after_c" != "402" ||
	"$p2c_scnid_after_c" != "$p2c_scn_s_id" ]]; then
	echo "FAIL  observation 13: rollback to C=${p2c_c} while S is active -- want status 200 scenarioActive=true, K served as S's 418 (masking the restored 409), L served as C's restored 402, S still active; got status ${p2c_rollback_c_status} scenarioActive=${p2c_rollback_c_active} K=${p2c_mock_k_after_c} L=${p2c_mock_l_after_c} scenarioId=${p2c_scnid_after_c}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13: rollback under an active scenario succeeds (200, scenarioActive=true), S still masks K at 418, and C's restored L=402 shows through where S names nothing"
fi

p2c_reset13_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/reset-overrides" '{}' -H "X-CSRF-Token: ${csrf}")
p2c_reset13_active=$(jq -r '.scenarioActive' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_reset13_status" != "200" || "$p2c_reset13_active" != "true" ]]; then
	echo "FAIL  observation 13: reset-overrides while S is STILL active -- want status 200 scenarioActive=true, got status ${p2c_reset13_status} scenarioActive=${p2c_reset13_active}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13: reset-overrides under an active scenario also reports scenarioActive=true -- the reset half of C8's flag is exercised too"
fi

# --------------------------------------------------------------------------
# Observation 14: restoreData is declared and interpreted (C11), against
# the SAME checkpoint C. P3d makes restoreData:true a real code path, not a
# 400 refusal any more -- captureEntitiesTx runs on EVERY checkpoint write,
# workspace or not, and encodes a zero-family document (`[]`, not a NULL
# blob) when the workspace confirms nothing, exactly as
# internal/checkpoints' own "declared but empty" rule for a confirmed
# family with zero rows extends to a workspace with zero confirmed
# families -- so checkpoint C's data_snap is NOT NULL and `no_data_snapshot`
# is the WRONG refusal to expect here (that code is reserved for the
# probe/compress degrade bands, not for "nothing to restore"). This
# workspace's spec has no confirmed resource family at all (P2c's fixture
# spec has no DELETE at all, see the P3a block below for the one that
# does), so the correct answer is 200 with dataRestored:false: the restore
# RAN and found nothing to carry over, given WITH the workspace's own
# confirmSlug since it is required whenever restoreData is true regardless
# of whether there is anything to restore.
# --------------------------------------------------------------------------
p2c_restoretrue_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/rollback/${p2c_c}" "$(jq -n --arg s "$p2c_slug" '{restoreData:true,confirmSlug:$s}')" -H "X-CSRF-Token: ${csrf}")
p2c_restoretrue_restored=$(jq -r '.dataRestored' "$BODY_FILE")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_restoretrue_status" != "200" || "$p2c_restoretrue_restored" != "false" ]]; then
	echo "FAIL  observation 14: POST /rollback/${p2c_c} {restoreData:true, confirmSlug:${p2c_slug}} -- want status 200 dataRestored=false (no confirmed family in this workspace, nothing to restore), got status ${p2c_restoretrue_status} dataRestored=${p2c_restoretrue_restored}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 14: restoreData:true against a workspace with no confirmed family answers 200 dataRestored=false, not the pre-P3d 400"
fi

p2c_restorefalse_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2c_ws_id}/rollback/${p2c_c}" '{"restoreData":false}' -H "X-CSRF-Token: ${csrf}")
p2c_checks=$((p2c_checks + 1))
if [[ "$p2c_restorefalse_status" != "200" ]]; then
	echo "FAIL  observation 14: POST /rollback/${p2c_c} {restoreData:false} -- want status 200, got ${p2c_restorefalse_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 14: restoreData:false takes the normal rollback path (200)"
fi

# --------------------------------------------------------------------------
# The block's own total: OUTSIDE any guard the block introduces (there is
# only the hard `exit 1` in Setup, which -- by leaving the process entirely
# -- can never reach this line with a partial count in the first place), so
# a P2c block that silently never ran, or that short-circuited partway
# through, shows up as a FAIL here instead of vanishing into "all checks
# PASSED" the way the P1b silent-skip precedent (smoke.sh's own guard
# around line 386) would.
# --------------------------------------------------------------------------
p2c_want_checks=35
if [[ "$p2c_checks" != "$p2c_want_checks" ]]; then
	echo "FAIL  P2c acceptance: expected exactly ${p2c_want_checks} checks to have run, ${p2c_checks} actually ran -- the block was short-circuited somewhere before reaching here"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2c acceptance: all ${p2c_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# P2d: two of the four deferred tails this slice ships against the workspace
# and scenario layers -- scenario cloning (POST .../scenarios' new `from`
# field) and scenario rename (PUT .../scenarios/{sid}) -- plus checkpoint
# deletion (DELETE .../checkpoints/{cid}) and the debounce ("auto")
# checkpoint trigger in front of the eight labelled routes SIG-LABELS names.
# Thirteen live observations total: twelve here, observation 13 on its own
# stack after the P1e path-mode block below, because it needs its own
# MOCKER_CHECKPOINT_DEBOUNCE and its own `docker compose up
# --force-recreate` (see that block's own header for why).
#
# PLACEMENT: after P2c, before the MCP block -- same two reasons P2b's own
# header gives for sitting where it does. Observation 5 below drives the
# mock plane by Host, which breaks once MOCKER_ROUTING=path lands; and that
# same observation's POST {prefix}/state writes workspaces.scenario_id
# PERSISTENTLY, on a workspace of ITS OWN this time, so it does not disturb
# anything the MCP block reads off $ws_id.
#
# Every variable this block introduces is prefixed p2d_, exactly as P2c
# prefixes p2c_. It creates its OWN workspaces -- p2d_ws for observations
# 1-7, 11 and 12, and a second, p2d_ws2, for 8-10 -- and never touches
# $ws_id. p2d_scn_s is the section's own source scenario, saved from p2d_ws
# BEFORE observation 1 activates it.
# --------------------------------------------------------------------------
echo "== P2d: scenario clone/rename and the checkpoint debounce/delete tails, thirteen live observations (12 here, 1 on its own stack after path mode) =="

p2d_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces "$(jq -cn --arg n "p2d-main" --argjson s "$spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p2d_ws_id=$(jq -r '.id' "$BODY_FILE")
p2d_slug=$(jq -r '.slug' "$BODY_FILE")
p2d_host="${p2d_slug}.${WORKSPACE_HOST_BASE}"
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_ws_status" != "201" || -z "$p2d_ws_id" || "$p2d_ws_id" == "null" ]]; then
	echo "FAIL  P2d setup: create p2d_ws with specId in the SAME POST (the shape observations 8-10 need below, exercised here too rather than only once) -- want status 201, got ${p2d_ws_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2d setup: p2d_ws ${p2d_slug} (id ${p2d_ws_id}) created with its spec already attached, one request"
fi

# p2d_scn_s is S: saved from p2d_ws's current (freshly-attached, override-
# free) state before anything else touches it. Its snapshot is captured
# NOW, from the 201 body, and reused by observations 1 and 2 below --
# neither observation re-fetches it, so a later edit to p2d_ws (observation
# 2's own override) can never leak into what this section believes S holds.
p2d_scn_s_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios" '{"name":"p2d-s"}' -H "X-CSRF-Token: ${csrf}")
p2d_scn_s_id=$(jq -r '.id' "$BODY_FILE")
p2d_s_snapshot=$(jq -Sc '{settings,basePath,spec,overrides}' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_scn_s_status" != "201" || -z "$p2d_scn_s_id" || "$p2d_scn_s_id" == "null" ]]; then
	echo "FAIL  P2d setup: save S (p2d-s) from p2d_ws's current state: want status 201, got ${p2d_scn_s_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2d setup: S=${p2d_scn_s_id} (p2d-s) saved from p2d_ws's current state"
fi

# --------------------------------------------------------------------------
# Observation 1: clone while active. Activating S first and cloning it
# second is the point -- an implementation that left A10's active-scenario
# refusal in front of the `from` path would answer 409 here instead of 201,
# because a scenario IS active by the time this request lands.
# --------------------------------------------------------------------------
p2d_activate_s_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios/${p2d_scn_s_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")

p2d_clone1_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios" \
	"$(jq -cn --argjson f "$p2d_scn_s_id" --arg n "p2d-clone-1" '{from:$f,name:$n}')" -H "X-CSRF-Token: ${csrf}")
p2d_clone1_id=$(jq -r '.id' "$BODY_FILE")
p2d_clone1_name=$(jq -r '.name' "$BODY_FILE")
p2d_clone1_snapshot=$(jq -Sc '{settings,basePath,spec,overrides}' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_activate_s_status" != "200" || "$p2d_clone1_status" != "201" ||
	"$p2d_clone1_snapshot" != "$p2d_s_snapshot" ||
	"$p2d_clone1_id" == "$p2d_scn_s_id" || "$p2d_clone1_name" == "p2d-s" ]]; then
	echo "FAIL  observation 1: clone S (from=${p2d_scn_s_id}) WHILE it is active -- want activate 200, clone 201, snapshot {settings,basePath,spec,overrides} equal to S's, id and name DIFFERENT from S's; got activate ${p2d_activate_s_status} clone ${p2d_clone1_status} id=${p2d_clone1_id} name=${p2d_clone1_name}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 1: cloning S while it is active succeeds (201) -- the active-scenario refusal does not sit in front of the from= path -- snapshot matches S, id/name differ"
fi

# --------------------------------------------------------------------------
# Observation 2: the divergence is created BEFORE the clone. p2d_s_snapshot
# was captured at S's own save time, above -- untouched since. Pinning an
# override on p2d_ws NOW, then cloning S a second time, proves CloneFrom
# reads S's STORED snapshot and not the workspace's current layer: an
# implementation that cloned by re-snapshotting the workspace would produce
# a clone whose overrides array carries this pin, differing from S.
# --------------------------------------------------------------------------
p2c_pin_status "$p2d_ws_id" "$detail_opkey" 494 >/dev/null

p2d_clone2_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios" \
	"$(jq -cn --argjson f "$p2d_scn_s_id" --arg n "p2d-clone-2" '{from:$f,name:$n}')" -H "X-CSRF-Token: ${csrf}")
p2d_clone2_snapshot=$(jq -Sc '{settings,basePath,spec,overrides}' "$BODY_FILE")

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/operations/${detail_opkey}" >/dev/null
p2d_live_status_after_pin=$(jq -r '.activeStatus' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_clone2_status" != "201" || "$p2d_clone2_snapshot" != "$p2d_s_snapshot" ||
	"$p2d_live_status_after_pin" != "494" ]]; then
	echo "FAIL  observation 2: pin ${detail_opkey}=494 on p2d_ws, THEN clone S again -- want clone 201 with S's ORIGINAL snapshot (no 494 pin), and the live workspace to actually carry 494 (so the two states genuinely differ); got clone ${p2d_clone2_status} live activeStatus=${p2d_live_status_after_pin} clone snapshot=${p2d_clone2_snapshot}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 2: the clone still matches S's snapshot taken at save time, even though p2d_ws's live state (activeStatus=494) has since diverged from it -- CloneFrom did not re-snapshot the workspace"
fi

# --------------------------------------------------------------------------
# Observation 3: clone name collision -> 409.
# --------------------------------------------------------------------------
p2d_clone3_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios" \
	"$(jq -cn --argjson f "$p2d_scn_s_id" --arg n "p2d-clone-1" '{from:$f,name:$n}')" -H "X-CSRF-Token: ${csrf}")
p2d_clone3_code=$(jq -r '.error.code // empty' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_clone3_status" != "409" || "$p2d_clone3_code" != "conflict" ]]; then
	echo "FAIL  observation 3: cloning S into the already-taken name p2d-clone-1 -- want status 409 code=conflict, got status ${p2d_clone3_status} code '${p2d_clone3_code}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 3: cloning into a taken name answers 409 code=conflict"
fi

# --------------------------------------------------------------------------
# Observation 4: clone of a foreign id -> 404, and no row is created. Needs
# a scenario belonging to a DIFFERENT workspace -- a small workspace of its
# own, p2d_ws_foreign, so this section still owns everything it reads.
# Fails an implementation whose `INSERT ... SELECT` omitted
# `AND workspace_id = ?`: that would find the foreign row across workspaces
# and happily clone it (201), where this wants 404 and an unchanged list.
# --------------------------------------------------------------------------
p2d_ws_foreign_status=$(http_json POST "$ADMIN_HOST" /api/workspaces "$(jq -cn --arg n "p2d-foreign" --argjson s "$spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p2d_ws_foreign_id=$(jq -r '.id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_ws_foreign_status" != "201" || -z "$p2d_ws_foreign_id" || "$p2d_ws_foreign_id" == "null" ]]; then
	echo "FAIL  observation 4 setup: create p2d_ws_foreign: want status 201, got ${p2d_ws_foreign_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 4 setup: p2d_ws_foreign (id ${p2d_ws_foreign_id}) created"
fi

p2d_scn_foreign_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_foreign_id}/scenarios" '{"name":"p2d-foreign-s"}' -H "X-CSRF-Token: ${csrf}")
p2d_scn_foreign_id=$(jq -r '.id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_scn_foreign_status" != "201" || -z "$p2d_scn_foreign_id" || "$p2d_scn_foreign_id" == "null" ]]; then
	echo "FAIL  observation 4 setup: create a scenario in p2d_ws_foreign: want status 201, got ${p2d_scn_foreign_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 4 setup: foreign scenario ${p2d_scn_foreign_id} (p2d-foreign-s) saved in p2d_ws_foreign"
fi

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios" >/dev/null
p2d_names_before_foreign=$(jq -cS '[.scenarios[].name] | sort' "$BODY_FILE")
p2d_clone4_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios" \
	"$(jq -cn --argjson f "$p2d_scn_foreign_id" --arg n "p2d-clone-4" '{from:$f,name:$n}')" -H "X-CSRF-Token: ${csrf}")
p2d_clone4_code=$(jq -r '.error.code // empty' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios" >/dev/null
p2d_names_after_foreign=$(jq -cS '[.scenarios[].name] | sort' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_clone4_status" != "404" || "$p2d_clone4_code" != "not_found" ||
	"$p2d_names_after_foreign" != "$p2d_names_before_foreign" ]]; then
	echo "FAIL  observation 4: cloning FROM a scenario id belonging to a different workspace -- want status 404 code=not_found and p2d_ws's scenario name list unchanged, got status ${p2d_clone4_status} code '${p2d_clone4_code}', names before=${p2d_names_before_foreign} after=${p2d_names_after_foreign}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 4: a foreign scenario id 404s here (ownership checked, not just existence) and no row was created"
fi

# p2d_ws is done being active for now -- deactivate S so creating p2d_scn_r
# below (CreateFromCurrentState, no `from`) does not hit A10's refusal.
http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null

p2d_scn_r_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios" '{"name":"p2d-r"}' -H "X-CSRF-Token: ${csrf}")
p2d_scn_r_id=$(jq -r '.id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_scn_r_status" != "201" || -z "$p2d_scn_r_id" || "$p2d_scn_r_id" == "null" ]]; then
	echo "FAIL  observations 5-7 setup: save p2d-r for renaming: want status 201, got ${p2d_scn_r_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observations 5-7 setup: p2d-r=${p2d_scn_r_id} saved for renaming"
fi

# --------------------------------------------------------------------------
# Observation 5: rename actually renames the row the mock plane addresses
# BY NAME, not just what the admin list shows. Fails an implementation that
# renamed only in the admin list: the new name would then 404 on the mock
# plane instead of activating, because ByName still resolves the old one.
# --------------------------------------------------------------------------
# A3: editVersion is REQUIRED on PUT .../scenarios/{sid} (D10) -- read the
# scenario's own detail route first, the same read a rename screen would
# hold open before submitting.
p2d_rename1_ev=$(scenario_edit_version "$p2d_ws_id" "$p2d_scn_r_id")
p2d_rename1_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios/${p2d_scn_r_id}" \
	"$(jq -cn --argjson ev "$p2d_rename1_ev" '{name:"p2d-r-renamed", editVersion:$ev}')" -H "X-CSRF-Token: ${csrf}")

p2d_state_new_body=$(jq -n '{scenario: "p2d-r-renamed"}')
p2d_state_new_status=$(http_json POST "$p2d_host" /__mocker/state "$p2d_state_new_body")
p2d_state_new_scenario=$(jq -r '.scenario' "$BODY_FILE")

p2d_state_old_body=$(jq -n '{scenario: "p2d-r"}')
p2d_state_old_status=$(http_json POST "$p2d_host" /__mocker/state "$p2d_state_old_body")
p2d_state_old_code=$(jq -r '.error.code // empty' "$BODY_FILE")

p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_rename1_status" != "200" || "$p2d_state_new_status" != "200" ||
	"$p2d_state_new_scenario" != "p2d-r-renamed" ||
	"$p2d_state_old_status" != "404" || "$p2d_state_old_code" != "not_found" ]]; then
	echo "FAIL  observation 5: PUT rename p2d-r -> p2d-r-renamed, then activate by the NEW name on the mock plane, then look up the OLD name there -- want PUT 200, new-name activate 200 scenario=p2d-r-renamed, old-name lookup 404 code=not_found; got PUT ${p2d_rename1_status} new-name status ${p2d_state_new_status} scenario=${p2d_state_new_scenario} old-name status ${p2d_state_old_status} code '${p2d_state_old_code}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 5: the renamed scenario activates by its NEW name on the mock plane, and the OLD name is a genuine 404 there -- the rename reached the row ByName resolves, not just the admin list"
fi

# --------------------------------------------------------------------------
# Observation 6: rename bumps no revision, and the rename demonstrably
# HAPPENED. Both middle steps (PUT 200, re-read .name) matter: an
# unconditional before/after revision comparison alone would pass with the
# whole PUT route deleted too (a 404 bumps nothing either), which is why
# this asserts the rename actually took effect in the SAME bracket as the
# revision comparison. Both revision reads are GET /api/workspaces/{id} --
# never the scenario detail route, which carries no revision at all.
# --------------------------------------------------------------------------
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}" >/dev/null
p2d_rev_before_rename2=$(jq -r '.revision' "$BODY_FILE")

p2d_rename2_ev=$(scenario_edit_version "$p2d_ws_id" "$p2d_scn_r_id")
p2d_rename2_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios/${p2d_scn_r_id}" \
	"$(jq -cn --argjson ev "$p2d_rename2_ev" '{name:"p2d-r-renamed-2", editVersion:$ev}')" -H "X-CSRF-Token: ${csrf}")

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios/${p2d_scn_r_id}" >/dev/null
p2d_rename2_name=$(jq -r '.name' "$BODY_FILE")

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}" >/dev/null
p2d_rev_after_rename2=$(jq -r '.revision' "$BODY_FILE")

p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_rename2_status" != "200" || "$p2d_rename2_name" != "p2d-r-renamed-2" ||
	"$p2d_rev_after_rename2" != "$p2d_rev_before_rename2" ]]; then
	echo "FAIL  observation 6: rename p2d-r-renamed -> p2d-r-renamed-2 between two GET /api/workspaces/${p2d_ws_id} reads -- want PUT 200, GET .../scenarios/${p2d_scn_r_id} .name=p2d-r-renamed-2, revision unchanged (${p2d_rev_before_rename2}); got PUT ${p2d_rename2_status} name='${p2d_rename2_name}' revision ${p2d_rev_before_rename2} -> ${p2d_rev_after_rename2}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 6: the rename happened (200, name is now ${p2d_rename2_name}) and left revision at ${p2d_rev_after_rename2} -- unmoved"
fi

# --------------------------------------------------------------------------
# Observation 7: rename collision -> 409. p2d-s (S, still on record even
# though deactivated) is the taken name.
# --------------------------------------------------------------------------
# A3: editVersion must be CURRENT here, not stale -- a stale token would
# make the UPDATE's own `AND edit_version = ?` clause match zero rows
# before the UNIQUE constraint is ever reached, turning this into a 409
# edit_conflict instead of the 409 conflict (duplicate name) this
# observation exists to prove.
p2d_rename3_ev=$(scenario_edit_version "$p2d_ws_id" "$p2d_scn_r_id")
p2d_rename3_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios/${p2d_scn_r_id}" \
	"$(jq -cn --argjson ev "$p2d_rename3_ev" '{name:"p2d-s", editVersion:$ev}')" -H "X-CSRF-Token: ${csrf}")
p2d_rename3_code=$(jq -r '.error.code // empty' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_rename3_status" != "409" || "$p2d_rename3_code" != "conflict" ]]; then
	echo "FAIL  observation 7: renaming into the already-taken name p2d-s -- want status 409 code=conflict, got status ${p2d_rename3_status} code '${p2d_rename3_code}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 7: renaming into a taken name answers 409 code=conflict"
fi

# --------------------------------------------------------------------------
# Observations 8, 9, 10 run on a workspace of their own, p2d_ws2, in THIS
# order, and that workspace is created by a SINGLE POST /api/workspaces
# carrying specId -- not the two-step POST-then-PATCH form every other
# direct attach site in this script uses. That is deliberate, not a house-
# pattern lapse: PATCH IS one of the eight labelled routes, so the two-step
# form would write auto row #1 before observation 8 ever reads the list,
# and observation 8 would go red against a CORRECT build. No labelled route
# runs on p2d_ws2 before observation 8's read -- creating the scenario
# below is POST .../scenarios, in the exclusion group, and activating it in
# observation 8 itself is POST .../activate, also excluded.
# --------------------------------------------------------------------------
p2d_ws2_status=$(http_json POST "$ADMIN_HOST" /api/workspaces "$(jq -cn --arg n "p2d-810" --argjson s "$spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p2d_ws2_id=$(jq -r '.id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_ws2_status" != "201" || -z "$p2d_ws2_id" || "$p2d_ws2_id" == "null" ]]; then
	echo "FAIL  observations 8-10 setup: create p2d_ws2 with specId in ONE request: want status 201, got ${p2d_ws2_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observations 8-10 setup: p2d_ws2 (id ${p2d_ws2_id}) created, spec attached, no labelled route touched yet"
fi

p2d_scn2_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws2_id}/scenarios" '{"name":"p2d-810-s"}' -H "X-CSRF-Token: ${csrf}")
p2d_scn2_id=$(jq -r '.id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_scn2_status" != "201" || -z "$p2d_scn2_id" || "$p2d_scn2_id" == "null" ]]; then
	echo "FAIL  observations 8-10 setup: save a scenario on p2d_ws2 to activate: want status 201, got ${p2d_scn2_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observations 8-10 setup: scenario ${p2d_scn2_id} (p2d-810-s) saved on p2d_ws2, still no labelled route touched"
fi

# --------------------------------------------------------------------------
# Observation 8: an excluded route writes nothing, on a workspace with no
# window open yet. Activating a scenario bumps revision but is deliberately
# UNLABELLED (§4's five-route group). This is the ONLY point in the run
# this can be caught: an implementation that wrongly labelled every
# revision-bumping route would write an auto row HERE, before the window
# opens -- once the window is open (observation 10) a correct build and a
# wrongly-labelled one both write nothing, so this check would go green
# against either one there.
# --------------------------------------------------------------------------
p2d_activate2_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws2_id}/scenarios/${p2d_scn2_id}/activate" '{}' -H "X-CSRF-Token: ${csrf}")

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws2_id}/checkpoints" >/dev/null
p2d_auto_count8=$(jq '[.checkpoints[] | select(.kind == "auto")] | length' "$BODY_FILE")

p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_activate2_status" != "200" || "$p2d_auto_count8" != "0" ]]; then
	echo "FAIL  observation 8: activating a scenario (unlabelled, revision-bumping) on a fresh p2d_ws2 -- want activate 200 and ZERO auto checkpoints, got activate ${p2d_activate2_status} auto count ${p2d_auto_count8}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 8: an excluded, revision-bumping route writes no auto checkpoint -- the window is still fully closed"
fi

# --------------------------------------------------------------------------
# Observation 9: the FIRST labelled call on p2d_ws2 -- a settings PATCH,
# reusing p2c_set_settings (already workspace-id-parameterised, not
# hard-wired to $ws_id or $p2c_ws_id) -- writes exactly one auto checkpoint,
# carrying that route's exact label from SIG-LABELS (§4's table, copied
# verbatim: neither this script nor server.go invents the string). Fails an
# implementation wired only in its own tests (no row ever appears here) and
# one whose label is empty (validateLabel rejects it, so again no row).
# --------------------------------------------------------------------------
p2d_patch9_status=$(p2c_set_settings "$p2d_ws2_id" '.identity.name = "P2d Auto 1"')

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws2_id}/checkpoints" >/dev/null
p2d_auto_count9=$(jq '[.checkpoints[] | select(.kind == "auto")] | length' "$BODY_FILE")
p2d_auto9_id=$(jq -r '[.checkpoints[] | select(.kind == "auto")][0].id // empty' "$BODY_FILE")
p2d_auto9_label=$(jq -r '[.checkpoints[] | select(.kind == "auto")][0].label // empty' "$BODY_FILE")
p2d_want_label9="правка настроек воркспейса"

p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_patch9_status" != "200" || "$p2d_auto_count9" != "1" ||
	"$p2d_auto9_label" != "$p2d_want_label9" ]]; then
	echo "FAIL  observation 9: the first labelled call (PATCH settings) on p2d_ws2 -- want PATCH 200, exactly one auto checkpoint, labelled '${p2d_want_label9}'; got PATCH ${p2d_patch9_status} auto count ${p2d_auto_count9} label '${p2d_auto9_label}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 9: the first labelled call writes exactly one auto checkpoint (id ${p2d_auto9_id}), labelled '${p2d_auto9_label}'"
fi

# --------------------------------------------------------------------------
# Observation 10: a SECOND labelled call inside the (86400s, per this run's
# preamble) window writes NONE -- suppression observed directly, as an
# unchanged population and an unchanged id, not by waiting the window out
# (that is observation 13's own job, on its own stack).
# --------------------------------------------------------------------------
p2d_patch10_status=$(p2c_set_settings "$p2d_ws2_id" '.identity.name = "P2d Auto 2"')

http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws2_id}/checkpoints" >/dev/null
p2d_auto_count10=$(jq '[.checkpoints[] | select(.kind == "auto")] | length' "$BODY_FILE")
p2d_auto10_id=$(jq -r '[.checkpoints[] | select(.kind == "auto")][0].id // empty' "$BODY_FILE")

p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_patch10_status" != "200" || "$p2d_auto_count10" != "1" || "$p2d_auto10_id" != "$p2d_auto9_id" ]]; then
	echo "FAIL  observation 10: a second labelled call inside the debounce window -- want PATCH 200, still exactly one auto checkpoint, the SAME row (id ${p2d_auto9_id}); got PATCH ${p2d_patch10_status} auto count ${p2d_auto_count10} id ${p2d_auto10_id}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 10: the second labelled call inside the window writes nothing -- still one auto row, id ${p2d_auto10_id} unchanged"
fi

# --------------------------------------------------------------------------
# Observation 11: delete a checkpoint -> 204, gone from the list; deleting
# it again -> 404. Runs on p2d_ws (back from p2d_ws2), on a manual
# checkpoint made for exactly this -- manual rows are never pruned by
# retention (the prune filter is `kind <> 'manual'`), so this cannot be
# lost to MOCKER_CHECKPOINT_RETENTION=3 between creation and deletion.
# --------------------------------------------------------------------------
p2d_cp11_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/checkpoints" '{"label":"p2d manual 11"}' -H "X-CSRF-Token: ${csrf}")
p2d_cp11_id=$(jq -r '.id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_cp11_status" != "201" || -z "$p2d_cp11_id" || "$p2d_cp11_id" == "null" ]]; then
	echo "FAIL  observation 11 setup: create a manual checkpoint on p2d_ws to delete: want status 201, got ${p2d_cp11_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 11 setup: manual checkpoint ${p2d_cp11_id} created on p2d_ws"
fi

p2d_del1_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/checkpoints/${p2d_cp11_id}" '{}' -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/checkpoints" >/dev/null
p2d_cp11_still_listed=$(jq --argjson id "$p2d_cp11_id" '[.checkpoints[] | select(.id == $id)] | length' "$BODY_FILE")
p2d_del2_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/checkpoints/${p2d_cp11_id}" '{}' -H "X-CSRF-Token: ${csrf}")

p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_del1_status" != "204" || "$p2d_cp11_still_listed" != "0" || "$p2d_del2_status" != "404" ]]; then
	echo "FAIL  observation 11: DELETE checkpoint ${p2d_cp11_id} -- want first DELETE 204 and gone from the list, second DELETE 404; got first ${p2d_del1_status} stillListed=${p2d_cp11_still_listed} second ${p2d_del2_status}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 11: DELETE removes checkpoint ${p2d_cp11_id} (204, gone from the list), and deleting it again answers 404"
fi

# --------------------------------------------------------------------------
# Observation 12: delete is scoped to the workspace. A checkpoint id
# belonging to p2d_ws2 (already in scope from observations 8-10), deleted
# through p2d_ws's own route -> 404, and the row survives on p2d_ws2. Fails
# an implementation whose DELETE lacks `AND workspace_id = ?`.
# --------------------------------------------------------------------------
p2d_cp12_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws2_id}/checkpoints" '{"label":"p2d manual 12 (other workspace)"}' -H "X-CSRF-Token: ${csrf}")
p2d_cp12_id=$(jq -r '.id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_cp12_status" != "201" || -z "$p2d_cp12_id" || "$p2d_cp12_id" == "null" ]]; then
	echo "FAIL  observation 12 setup: create a manual checkpoint on p2d_ws2: want status 201, got ${p2d_cp12_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 12 setup: manual checkpoint ${p2d_cp12_id} created on p2d_ws2"
fi

p2d_cross_del_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/checkpoints/${p2d_cp12_id}" '{}' -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_ws2_id}/checkpoints" >/dev/null
p2d_cp12_still_listed=$(jq --argjson id "$p2d_cp12_id" '[.checkpoints[] | select(.id == $id)] | length' "$BODY_FILE")

p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_cross_del_status" != "404" || "$p2d_cp12_still_listed" != "1" ]]; then
	echo "FAIL  observation 12: DELETE p2d_ws2's checkpoint ${p2d_cp12_id} through p2d_ws's own route -- want status 404 and the row still present on p2d_ws2, got status ${p2d_cross_del_status} stillListed=${p2d_cp12_still_listed}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 12: a checkpoint id belonging to a DIFFERENT workspace 404s here too, and the row survives untouched on p2d_ws2"
fi

# The section deactivates any scenario it activated, on both workspaces it
# leaves running scenarios on (p2d_ws by observation 5's rename-and-
# activate, p2d_ws2 by observation 8's activate) -- observations 1-12's own
# cleanup, independent of observation 13's later, unrelated workspace.
http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null
http_json POST "$ADMIN_HOST" "/api/workspaces/${p2d_ws2_id}/scenarios/deactivate" '{}' -H "X-CSRF-Token: ${csrf}" >/dev/null

# --------------------------------------------------------------------------
# The block's own total, OUTSIDE any guard it introduces -- same reasoning
# as P2c's own identical closing block (smoke.sh, just above the MCP
# header): a short-circuited or silently-skipped run of observations 1-12
# shows up as a FAIL here instead of vanishing into a clean "all PASSED".
# --------------------------------------------------------------------------
p2d_want_checks=21
if [[ "$p2d_checks" != "$p2d_want_checks" ]]; then
	echo "FAIL  P2d acceptance (observations 1-12): expected exactly ${p2d_want_checks} checks to have run, ${p2d_checks} actually ran -- the block was short-circuited somewhere before reaching here"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2d acceptance (observations 1-12): all ${p2d_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# MCP: POST /mcp on the admin host (MOCKER_MCP_KEY). This block runs BEFORE
# the P1e MOCKER_ROUTING=path switch just below for the same reason P1e's
# own comment gives for itself: that switch never reverts, and in path mode
# a workspace-host GET /widgets no longer reaches the mock plane at all --
# an observation placed after it would fail against a perfectly correct
# implementation. Only the path-mode half of the last observation below
# lives inside that switch's own block, where it belongs.
# --------------------------------------------------------------------------

# mcp_call issues one raw /mcp request carrying exactly the four things every
# such call needs and nothing an admin-plane call would add:
#   - Accept: application/json, text/event-stream -- BOTH types, on every
#     method, or the SDK answers 400 with a plain-text complaint before it
#     ever looks at the body (measured against v1.7.0);
#   - Origin: http://<host> -- NOT optional. The very first observation below
#     runs BEFORE the key exists at all, so /mcp is not mounted and the
#     request falls through to enforceCSRF (internal/admin/security.go),
#     which 403s a state-changing request carrying no Origin/Referer BEFORE
#     the mux ever gets a chance to answer its own 404 -- the same trap
#     internal/server/server.go:96 already documents for a different route.
#     Without this header that observation would read 403 and blame a
#     feature it never actually reached;
#   - Authorization: Bearer <bearer>, only when bearer is non-empty -- an
#     absent or wrong bearer is itself the point of the first two
#     observations, so this must stay optional rather than always-on;
#   - Content-Type: application/json, only when a body is being sent -- a
#     GET (the "method not allowed" observation) has none.
# Deliberately NOT using $COOKIE_JAR: /mcp is cookie-free by design, and
# reusing the operator's own session cookie here would contradict the exact
# property most of the observations below exist to prove.
mcp_call() {
	local method=$1 host=$2 path=$3 bearer=$4 data=${5:-}
	local auth=()
	if [[ -n "$bearer" ]]; then
		auth=(-H "Authorization: Bearer ${bearer}")
	fi
	if [[ -n "$data" ]]; then
		curl -s -o "$BODY_FILE" -w '%{http_code}' -X "$method" \
			-H "Host: ${host}" -H 'Content-Type: application/json' \
			-H 'Accept: application/json, text/event-stream' \
			-H "Origin: http://${host}" \
			"${auth[@]}" --data "$data" "${BASE_URL}${path}"
	else
		curl -s -o "$BODY_FILE" -w '%{http_code}' -X "$method" \
			-H "Host: ${host}" \
			-H 'Accept: application/json, text/event-stream' \
			-H "Origin: http://${host}" \
			"${auth[@]}" "${BASE_URL}${path}"
	fi
}

# mcp_tool is mcp_call's own layer for one tools/call round trip: it builds
# the JSON-RPC envelope around a tool name and its already-JSON-encoded
# arguments through jq, not string interpolation -- more than one argument
# below is a percent-encoded opKey, and a hand-built string would have to
# re-derive jq's own escaping to get that right. id is always 1: every call
# in this block runs to completion before the next one starts, so nothing
# here ever needs to correlate two in-flight requests.
mcp_tool() {
	local host=$1 bearer=$2 name=$3 args=$4
	local req
	req=$(jq -n --arg n "$name" --argjson a "$args" '{jsonrpc: "2.0", id: 1, method: "tools/call", params: {name: $n, arguments: $a}}')
	mcp_call POST "$host" /mcp "$bearer" "$req"
}

mcp_tools_list_req='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'

echo "== MCP observation 1: no key -> the route does not exist =="
# .env.example ships MOCKER_MCP_KEY empty, and the stack has been running on
# an unmodified .env since the top of this script, so this runs against a
# build where /mcp is not mounted at all. Plain 404 from net/http's own mux
# -- not 403 (that would mean the Origin header above is being ignored, see
# mcp_call's own comment) and not mocker's HTML shell (that would mean this
# path reached webui.Handler instead of the mux's own not-found).
mcp_status=$(mcp_call POST "$ADMIN_HOST" /mcp "" "$mcp_tools_list_req")
if [[ "$mcp_status" != "404" ]]; then
	echo "FAIL  POST /mcp with no MOCKER_MCP_KEY set: want status 404, got ${mcp_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  POST /mcp with no MOCKER_MCP_KEY set: 404, the route does not exist"
fi

echo "== MCP: mounting the endpoint (MOCKER_MCP_KEY set, stack recreated) =="
# The format below is chosen to clear config.Load's own 32-byte floor
# (internal/config's minMCPKeyLen) by construction rather than by luck:
# the fixed prefix alone is 22 bytes, %s%N is a 19-digit nanosecond epoch,
# and $$ adds the PID -- comfortably over 32 bytes on every run, with no
# length check needed here to prove it.
MCP_KEY="mocker-smoke-mcp-key-$(date +%s%N)-$$"
grep -v '^MOCKER_MCP_KEY=' "$ENV_FILE" >"${ENV_FILE}.tmp"
mv "${ENV_FILE}.tmp" "$ENV_FILE"
printf 'MOCKER_MCP_KEY=%s\n' "$MCP_KEY" >>"$ENV_FILE"

echo "== recreating the compose stack with MOCKER_MCP_KEY set =="
docker compose up -d --force-recreate

echo "== waiting for ${BASE_URL} (MCP key set) =="
wait_for_port

echo "== MCP observation 2: a wrong key -> 401 =="
MCP_WRONG_KEY="not-the-real-mcp-key-$$"
mcp_status=$(mcp_call POST "$ADMIN_HOST" /mcp "$MCP_WRONG_KEY" "$mcp_tools_list_req")
if [[ "$mcp_status" != "401" ]]; then
	echo "FAIL  POST /mcp with a wrong bearer: want status 401, got ${mcp_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
elif grep -qE '<script[^>]+src=' "$BODY_FILE"; then
	echo "FAIL  POST /mcp with a wrong bearer: body looks like the SPA shell, not the guard's own plain rejection"
	fail_count=$((fail_count + 1))
elif grep -qF "$MCP_KEY" "$BODY_FILE"; then
	echo "FAIL  POST /mcp with a wrong bearer: the rejection body leaked the real key"
	fail_count=$((fail_count + 1))
else
	echo "PASS  POST /mcp with a wrong bearer: 401, plain body, real key not echoed"
fi

echo "== MCP observation 3: initialize succeeds =="
mcp_init_req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0.0.1"}}}'
mcp_status=$(mcp_call POST "$ADMIN_HOST" /mcp "$MCP_KEY" "$mcp_init_req")
mcp_server_name=$(jq -r '.result.serverInfo.name // empty' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_server_name" != "mocker" ]]; then
	echo "FAIL  initialize: want status 200 result.serverInfo.name=mocker, got status ${mcp_status} name '${mcp_server_name}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  initialize: 200, serverInfo.name=mocker"
fi

echo "== MCP observation 4: tools/list returns exactly 63 tools =="
# No initialize sent first -- measured against v1.7.0: tools/list works
# without one in stateless mode, so this is a complete, standalone observation.
# 43 -> 44: P4a's get_workspace_drift (the block below, "P4a: the MCP tool
# get_workspace_drift names the same override") is registered like every
# other tool and so raises this count too. 44 -> 46: A4's probe_workspace
# and list_resource_entities -- A4 moved the Go count and left this line at
# 44, and the suite was red on this one check from A4 to P6a. 46 -> 47:
# P6a's get_stream_stats (decisions.md mocker-p6a-sse D16, D25). 47 -> 48:
# P6b's preview_endpoint (decisions.md mocker-p6b-sse-mock D13). The same
# 48 the Go tests carry (internal/mcp/a3_editversion_test.go,
# internal/mcp/mcp_resources_test.go).
mcp_status=$(mcp_call POST "$ADMIN_HOST" /mcp "$MCP_KEY" "$mcp_tools_list_req")
mcp_tool_count=$(jq '.result.tools | length' "$BODY_FILE")
mcp_has_lw=$(jq '[.result.tools[].name] | index("list_workspaces") != null' "$BODY_FILE")
mcp_has_ssd=$(jq '[.result.tools[].name] | index("set_session_directive") != null' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_tool_count" != "63" || "$mcp_has_lw" != "true" || "$mcp_has_ssd" != "true" ]]; then
	echo "FAIL  tools/list: want status 200, 63 tools, list_workspaces and set_session_directive both present; got status ${mcp_status} count ${mcp_tool_count} list_workspaces=${mcp_has_lw} set_session_directive=${mcp_has_ssd}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  tools/list: 200, exactly 63 tools, list_workspaces and set_session_directive both present"
fi

echo "== MCP observation 5: create_workspace works live, and so do list_specs and list_workspaces =="
# list_specs FIRST: create_workspace only takes a specId, and list_specs is
# the only tool that discovers one -- the same reason §0 gives for why this
# slice ships list_specs instead of an import_spec tool.
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" list_specs '{}')
mcp_spec_name=$(jq -r --arg id "$spec_id" '.result.structuredContent.specs[] | select((.id | tostring) == $id) | .name' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_spec_name" != "Widgets" ]]; then
	echo "FAIL  list_specs: want status 200 naming spec ${spec_id} 'Widgets', got status ${mcp_status} name '${mcp_spec_name}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  list_specs: 200, names the spec the harness imported (id ${spec_id})"
fi

mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_workspace "$(jq -n --arg n "mcp-smoke-$$" --argjson sid "$spec_id" '{name: $n, specId: $sid}')")
mcp_new_ws_id=$(jq -r '.result.structuredContent.id // empty' "$BODY_FILE")
mcp_new_ws_slug=$(jq -r '.result.structuredContent.slug // empty' "$BODY_FILE")
mcp_new_ws_url=$(jq -r '.result.structuredContent.url // empty' "$BODY_FILE")
if [[ "$mcp_status" != "200" || -z "$mcp_new_ws_id" || -z "$mcp_new_ws_slug" || "$mcp_new_ws_url" != *"$mcp_new_ws_slug"* ]]; then
	echo "FAIL  create_workspace: want status 200 with id/slug/url (url containing slug), got status ${mcp_status} id '${mcp_new_ws_id}' slug '${mcp_new_ws_slug}' url '${mcp_new_ws_url}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  create_workspace: 200, workspace ${mcp_new_ws_slug} (id ${mcp_new_ws_id}) created against workspaces.owner_id's real FK row"
fi

mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" list_workspaces '{}')
mcp_found_slug=$(jq -r --arg id "$mcp_new_ws_id" '.result.structuredContent.workspaces[] | select((.id | tostring) == $id) | .slug' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_found_slug" != "$mcp_new_ws_slug" ]]; then
	echo "FAIL  list_workspaces: want status 200 listing workspace ${mcp_new_ws_id} as slug ${mcp_new_ws_slug}, got status ${mcp_status} slug '${mcp_found_slug}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  list_workspaces: 200, the workspace create_workspace just made is listed back"
fi

echo "== MCP observation 6: an override made through a tool changes what the mock plane serves =="
# Against the harness's OWN workspace (ws_id / workspace_host), never the one
# observation 5 just created -- that one has no traffic and is not the
# workspace observation 9's revision check reads either.
mcp_before_status=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")

mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" find_operations "$(jq -n --argjson wid "$ws_id" --arg q "widgets" '{workspaceId: $wid, query: $q}')")
mcp_widgets_opkey=$(jq -r '.result.structuredContent.operations[] | select(.method == "GET" and .path == "/widgets") | .opKey' "$BODY_FILE")
if [[ "$mcp_status" != "200" || -z "$mcp_widgets_opkey" ]]; then
	echo "FAIL  find_operations: want status 200 with an opKey for GET /widgets, got status ${mcp_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  find_operations: 200, found GET /widgets as opKey ${mcp_widgets_opkey}"
fi

# get_operation on the opKey find_operations just returned. §C-a's own point
# is that GET .../operations/{opKey} answers 404 for an operation nobody has
# pinned a status on yet, and this tool must report that as "no override",
# never as a tool error -- unit-tested directly against a fake Caller in
# internal/mcp/tools_ops_test.go, which controls the 404 precisely. Live,
# this exact route already carries an earlier block's row-level delayMs
# override (section E observation 1, above), so THIS call exercises the 200
# branch -- and it still earns its place: it is what proves the opKey
# survives into the set_operation_response check right below (opKey built by
# a tool, matched against a stored override, overrideOn/routeOff set by §C-b).
# It does NOT touch the 404 branch, though -- that needs an operation that is
# genuinely un-overridden right now, which this opKey is not. See the next
# call.
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" get_operation "$(jq -n --argjson wid "$ws_id" --arg ok "$mcp_widgets_opkey" '{workspaceId: $wid, opKey: $ok}')")
mcp_get_op_err=$(jq -r '.result.isError // false' "$BODY_FILE")
mcp_get_op_path=$(jq -r '.result.structuredContent.operation.path // empty' "$BODY_FILE")
# A3: this row's own editVersion, off the read the caller's own next write
# is required to have preceded (D11) -- never a version this script reads
# some OTHER way, which would make the write below no test of the tool
# actually carrying the CALLER's expectation through.
mcp_widgets_ev=$(jq -r '.result.structuredContent.operation.override.editVersion' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_get_op_err" != "false" || "$mcp_get_op_path" != "/widgets" ]]; then
	echo "FAIL  get_operation: want status 200 isError=false operation.path=/widgets, got status ${mcp_status} isError=${mcp_get_op_err} path '${mcp_get_op_path}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  get_operation: 200, reports the operation without turning either a 404-no-override or a 200-with-override into a tool error"
fi

# The 404-no-override half of §C-a, live, against an operation that can
# actually reach that state: POST /widgets. Its only override -- the when[]
# pinned-409 variant from "when[]: a body-equality condition..." above -- was
# DELETEd right after ("when_delete"); nothing writes to create_opkey again
# until MCP observation 8's override_from_traffic, which runs after this
# block. So GET .../operations/{create_opkey} genuinely answers 404 here, and
# this is the call that exercises handleGetOperation's
# `case http.StatusNotFound` in internal/mcp/tools_ops.go: delete that case
# (folding 404 into the `default: toolErr` branch) and this tool call starts
# returning a tool error instead of a reported operation, and the check below
# goes red.
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" get_operation "$(jq -n --argjson wid "$ws_id" --arg ok "$create_opkey" '{workspaceId: $wid, opKey: $ok}')")
mcp_get_op2_err=$(jq -r '.result.isError // false' "$BODY_FILE")
mcp_get_op2_method=$(jq -r '.result.structuredContent.operation.method // empty' "$BODY_FILE")
mcp_get_op2_path=$(jq -r '.result.structuredContent.operation.path // empty' "$BODY_FILE")
mcp_get_op2_override=$(jq -r '.result.structuredContent.operation.override // empty' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_get_op2_err" != "false" || "$mcp_get_op2_method" != "POST" ||
	"$mcp_get_op2_path" != "/widgets" || -n "$mcp_get_op2_override" ]]; then
	echo "FAIL  get_operation (POST /widgets, un-overridden): want status 200 isError=false method=POST path=/widgets with no override, got status ${mcp_status} isError=${mcp_get_op2_err} method '${mcp_get_op2_method}' path '${mcp_get_op2_path}' override '${mcp_get_op2_override}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  get_operation (POST /widgets, un-overridden): 200, isError=false, override absent -- the live 404-no-override branch of §C-a"
fi

# A distinctive status, and NOT 503: observation 7 forces exactly 503
# through the session layer, which sits ABOVE this one. If this override
# pinned 503 too, both halves of observation 7 would pass with the whole
# session-directive feature deleted.
mcp_forced_status=299
# A3: editVersion is REQUIRED on this tool (D11) -- mcp_widgets_ev is the
# CALLER's own expectation from the get_operation call just above, never a
# version this call reads for itself (set_operation_response's own
# Description: forwarding its internal GET's version would make the check
# vacuous).
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" set_operation_response "$(jq -n --argjson wid "$ws_id" --arg ok "$mcp_widgets_opkey" --argjson st "$mcp_forced_status" --argjson ev "$mcp_widgets_ev" '{workspaceId: $wid, opKey: $ok, status: $st, editVersion: $ev}')")
mcp_new_active=$(jq -r '.result.structuredContent.activeStatus // empty' "$BODY_FILE")
mcp_widgets_ev_after=$(jq -r '.result.structuredContent.editVersion // empty' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_new_active" != "$mcp_forced_status" || -z "$mcp_widgets_ev_after" || "$mcp_widgets_ev_after" == "$mcp_widgets_ev" ]]; then
	echo "FAIL  set_operation_response: want status 200 activeStatus=${mcp_forced_status} with a FRESH editVersion (not ${mcp_widgets_ev}), got status ${mcp_status} activeStatus '${mcp_new_active}' editVersion '${mcp_widgets_ev_after}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  set_operation_response: 200, activeStatus now ${mcp_new_active}, editVersion ${mcp_widgets_ev} -> ${mcp_widgets_ev_after}"
fi

mcp_after_status=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
if [[ "$mcp_after_status" != "$mcp_forced_status" || "$mcp_after_status" == "$mcp_before_status" ]]; then
	echo "FAIL  GET /widgets after set_operation_response: want ${mcp_forced_status} and different from the ${mcp_before_status} baseline, got ${mcp_after_status}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  GET /widgets after set_operation_response: ${mcp_before_status} -> ${mcp_after_status} -- a tool-built opKey survived CallAsMCP and matched a stored override"
fi

# --------------------------------------------------------------------------
# A3, MCP hop: property 8 (D13) is that a stale editVersion answers as a
# TYPED CONFLICT the model can read, never a bare tool error -- this is
# THE assertion for that property; D11's own reasoning is why it has to be
# reused, not fresh: mcp_widgets_ev is now the OLD token (the write just
# above already moved it to mcp_widgets_ev_after), so resending it is
# exactly the lost-update shape a model would produce by writing twice off
# one stale read. isError must read false (a conflict is DATA, an
# `internal/mcp` write that let a 409 fall through toolErr would turn this
# into a tool error instead, and this is the line that would go red).
# --------------------------------------------------------------------------
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" set_operation_response "$(jq -n --argjson wid "$ws_id" --arg ok "$mcp_widgets_opkey" --argjson st 599 --argjson ev "$mcp_widgets_ev" '{workspaceId: $wid, opKey: $ok, status: $st, editVersion: $ev}')")
mcp_conflict_err=$(jq -r '.result.isError // false' "$BODY_FILE")
mcp_conflict_ev=$(jq -r '.result.structuredContent.conflict.document.editVersion // empty' "$BODY_FILE")
mcp_conflict_status_after=$(jq -r '.result.structuredContent.conflict.document.activeStatus // empty' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_conflict_err" != "false" ||
	"$mcp_conflict_ev" != "$mcp_widgets_ev_after" || "$mcp_conflict_status_after" != "$mcp_forced_status" ]]; then
	echo "FAIL  set_operation_response with a STALE editVersion (${mcp_widgets_ev}): want a 200 tools/call carrying isError=false and a conflict.document naming the CURRENT editVersion (${mcp_widgets_ev_after}) and activeStatus (${mcp_forced_status}), got status ${mcp_status} isError=${mcp_conflict_err} conflict.document.editVersion='${mcp_conflict_ev}' conflict.document.activeStatus='${mcp_conflict_status_after}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  set_operation_response with a stale editVersion comes back as a typed CONFLICT (isError=false), carrying the CURRENT document (editVersion=${mcp_conflict_ev}, activeStatus=${mcp_conflict_status_after}) -- never a bare tool error"
fi

mcp_status_still=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
if [[ "$mcp_status_still" != "$mcp_forced_status" ]]; then
	echo "FAIL  after the stale-editVersion MCP write: GET /widgets want ${mcp_forced_status} (unmoved), got ${mcp_status_still} -- the refused write leaked through"
	fail_count=$((fail_count + 1))
else
	echo "PASS  after the stale-editVersion MCP write: GET /widgets is still ${mcp_status_still} -- the refusal changed nothing"
fi

echo "== MCP observation 7: a session directive takes effect and can be taken back =="
# Bracket revision and the traffic high-water mark around EXACTLY this
# observation and nothing else (observation 9's own rule): observation 6
# just wrote a real override (expected to move the revision), and
# observation 8 below writes another one on purpose (override_from_traffic).
# Reading either boundary anywhere else would make the "untouched" claim
# vacuous or wrong.
mcp_rev_before_directive=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null && jq -r '.revision' "$BODY_FILE")

mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" list_traffic "$(jq -n --argjson wid "$ws_id" '{workspaceId: $wid, limit: 1}')")
mcp_traffic_max_before=$(jq -r '.result.structuredContent.rows[0].id // 0' "$BODY_FILE")

mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" set_session_directive "$(jq -n --argjson wid "$ws_id" '{workspaceId: $wid, method: "GET", path: "/widgets", action: "status", status: 503}')")
mcp_directive_count=$(jq -r '.result.structuredContent.directives | length // 0' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$mcp_directive_count" -lt 1 ]]; then
	echo "FAIL  set_session_directive (force 503): want status 200 with >=1 directive listed, got status ${mcp_status} count ${mcp_directive_count}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
	mcp_obs7_ok=0
else
	mcp_forced_get_status=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
	if [[ "$mcp_forced_get_status" != "503" ]]; then
		echo "FAIL  GET /widgets under the session directive: want status 503, got ${mcp_forced_get_status}"
		fail_count=$((fail_count + 1))
		mcp_obs7_ok=0
	else
		echo "PASS  set_session_directive: session beats the workspace-level override (503)"

		mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" set_session_directive "$(jq -n --argjson wid "$ws_id" '{workspaceId: $wid, clearAll: true}')")
		mcp_cleared=$(jq -r '.result.structuredContent.cleared // 0' "$BODY_FILE")
		mcp_after_clear_status=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${workspace_host}" "${BASE_URL}/widgets")
		if [[ "$mcp_status" != "200" || "$mcp_cleared" -lt 1 || "$mcp_after_clear_status" != "$mcp_forced_status" ]]; then
			echo "FAIL  set_session_directive (clearAll): want status 200 cleared>=1 and GET /widgets back to ${mcp_forced_status}, got status ${mcp_status} cleared ${mcp_cleared} GET-status ${mcp_after_clear_status}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
			mcp_obs7_ok=0
		else
			echo "PASS  set_session_directive (clearAll): 503 -> ${mcp_after_clear_status}, the workspace-level override from observation 6 is back"
			mcp_obs7_ok=1
		fi
	fi
fi

echo "== MCP observation 9 (revision half): a session directive never bumps workspaces.revision =="
# Read immediately after observation 7 and BEFORE observation 8, which
# writes a real override on purpose (override_from_traffic) and is
# EXPECTED to move the revision -- run this comparison after that and it
# goes red against a perfectly correct implementation. Conditional on
# observation 7 having actually succeeded: a failed tool call also leaves
# the revision untouched, so an unconditional pass here would be vacuous.
http_json GET "$ADMIN_HOST" "/api/workspaces/${ws_id}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
mcp_rev_after_directive=$(jq -r '.revision' "$BODY_FILE")
if [[ "${mcp_obs7_ok:-0}" != "1" ]]; then
	echo "FAIL  revision-untouched check: observation 7 itself did not complete, so this comparison cannot mean anything"
	fail_count=$((fail_count + 1))
elif [[ "$mcp_rev_before_directive" != "$mcp_rev_after_directive" ]]; then
	echo "FAIL  the session directive moved workspace ${ws_id}'s revision ${mcp_rev_before_directive} -> ${mcp_rev_after_directive}; session state is RAM-only and must never touch it"
	fail_count=$((fail_count + 1))
else
	echo "PASS  workspace ${ws_id}'s revision stayed at ${mcp_rev_after_directive} across the whole session-directive round trip"
fi

echo "== MCP observation 8: list_traffic carries no bodies, and override_from_traffic converts a row =="
# A fresh POST /widgets, not reused from any earlier check: the when[]
# override an earlier P1c-2 block put on this exact route was already
# deleted there, so this is a clean, freshly generated 201 with a real
# Widget body -- read internal/admin/from_traffic.go's own conflict
# conditions before picking a row, and this one trips none of them (it
# matched a spec operation belonging to this workspace's own spec, and its
# body is neither truncated, redacted nor a no-body status).
mcp_obs8_marker="mcp-obs8-marker-$$"
curl -s -o /dev/null -X POST -H "Host: ${workspace_host}" -H 'Content-Type: application/json' \
	--data "{\"name\":\"MCP obs8\",\"kind\":\"${mcp_obs8_marker}\"}" "${BASE_URL}/widgets"

# Traffic recording flushes asynchronously (DESIGN §18: batched, never an
# INSERT per request), so this polls with the same ceiling the harness
# already uses elsewhere for the same reason: 10 tries, 0.5s apart = 5s
# against a 500ms default flush interval. Matching against
# mcp_traffic_max_before, not merely "some rows exist" -- the buffer is
# already full of earlier checks' own traffic by this point in the run.
mcp_obs8_found=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
	mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" list_traffic "$(jq -n --argjson wid "$ws_id" '{workspaceId: $wid, limit: 50}')")
	if [[ "$mcp_status" == "200" ]]; then
		mcp_has_503=$(jq --argjson max "$mcp_traffic_max_before" \
			'[.result.structuredContent.rows[] | select(.method == "GET" and .path == "/widgets" and .status == 503 and .id > $max)] | length > 0' "$BODY_FILE")
		mcp_has_post=$(jq --argjson max "$mcp_traffic_max_before" \
			'[.result.structuredContent.rows[] | select(.method == "POST" and .path == "/widgets" and .status == 201 and .id > $max)] | length > 0' "$BODY_FILE")
		if [[ "$mcp_has_503" == "true" && "$mcp_has_post" == "true" ]]; then
			mcp_obs8_found=1
			break
		fi
	fi
	sleep 0.5
done

if [[ "$mcp_obs8_found" != "1" ]]; then
	echo "FAIL  list_traffic: observation 7's forced-503 row and the fresh POST /widgets row never both appeared after 10 tries over 5s"
	fail_count=$((fail_count + 1))
else
	if grep -qF '"reqBody"' "$BODY_FILE" || grep -qF '"respBody"' "$BODY_FILE" || grep -qF "$mcp_obs8_marker" "$BODY_FILE"; then
		echo "FAIL  list_traffic: a plain list call leaked a body or the request marker -- §C's compaction rule says no bodies by default"
		fail_count=$((fail_count + 1))
	else
		echo "PASS  list_traffic: no bodies on a plain list, while observation 7's 503 call and a fresh POST are both visible by method/path/status"
	fi

	mcp_post_tid=$(jq -r --argjson max "$mcp_traffic_max_before" \
		'[.result.structuredContent.rows[] | select(.method == "POST" and .path == "/widgets" and .status == 201 and .id > $max)] | first | .id // empty' "$BODY_FILE")
	if [[ -z "$mcp_post_tid" ]]; then
		echo "FAIL  override_from_traffic: no POST /widgets traffic id to convert"
		fail_count=$((fail_count + 1))
	else
		mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" override_from_traffic "$(jq -n --argjson wid "$ws_id" --argjson tid "$mcp_post_tid" '{workspaceId: $wid, trafficId: $tid}')")
		mcp_ofr_err=$(jq -r '.result.isError // false' "$BODY_FILE")
		mcp_ofr_opkey=$(jq -r '.result.structuredContent.opKey // empty' "$BODY_FILE")
		mcp_create_opkey=$(jq -rn --arg s "POST /widgets" '$s | @uri')
		if [[ "$mcp_status" != "200" || "$mcp_ofr_err" != "false" || "$mcp_ofr_opkey" != "$mcp_create_opkey" ]]; then
			echo "FAIL  override_from_traffic: want status 200 isError=false opKey=${mcp_create_opkey}, got status ${mcp_status} isError=${mcp_ofr_err} opKey '${mcp_ofr_opkey}': $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		else
			echo "PASS  override_from_traffic: converted traffic row ${mcp_post_tid} into opKey ${mcp_ofr_opkey}"
		fi
	fi
fi

echo "== MCP observation 10: GET /mcp is 405, not the SPA =="
mcp_status=$(mcp_call GET "$ADMIN_HOST" /mcp "$MCP_KEY")
if [[ "$mcp_status" != "405" ]]; then
	echo "FAIL  GET /mcp: want status 405, got ${mcp_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
elif grep -qE '<script[^>]+src=' "$BODY_FILE"; then
	echo "FAIL  GET /mcp: body looks like the SPA shell -- webui.Handler's isAdminPath does not know /mcp, and this is the trap that would mean it swallowed it anyway"
	fail_count=$((fail_count + 1))
else
	echo "PASS  GET /mcp: 405, the SDK's own transport answered, not the SPA"
fi

echo "== MCP observation 11 (host mode half): the mock plane never serves /mcp =="
mcp_status=$(mcp_call POST "$workspace_host" /mcp "$MCP_KEY" "$mcp_tools_list_req")
if [[ "$mcp_status" != "404" ]] || grep -qF '"jsonrpc"' "$BODY_FILE"; then
	echo "FAIL  POST /mcp on the workspace host: want status 404 from the mock plane (not an MCP response), got ${mcp_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  POST /mcp on the workspace host: 404 from the mock plane, not an MCP response"
fi

echo "== MCP: measured, not claimed -- binary size and go.mod module trees =="
mcp_bin_tmp=$(mktemp)
if docker compose cp mocker:/mocker "$mcp_bin_tmp" >/dev/null 2>&1; then
	mcp_bin_size=$(stat -c%s "$mcp_bin_tmp" 2>/dev/null || wc -c <"$mcp_bin_tmp")
	echo "MEASURED  /mocker in the built image: ${mcp_bin_size} bytes"
else
	echo "SKIP  could not copy /mocker out of the running container to measure its size"
fi
rm -f "$mcp_bin_tmp"

# b261440 is this slice's own starting HEAD (its context document's §H, the
# same commit a prior probe measured six new module trees against:
# jsonschema-go, uritemplate/v3, segmentio/asm, segmentio/encoding, x/oauth2,
# x/time). Counting new "// indirect" lines go.mod gained since then is that
# same methodology, re-run rather than repeated from memory.
if git cat-file -e b261440 2>/dev/null; then
	mcp_module_trees=$(git diff b261440 -- go.mod | grep -c '^+.*// indirect$' || true)
	echo "MEASURED  go.mod gained ${mcp_module_trees} new indirect module tree(s) since b261440"
else
	echo "SKIP  commit b261440 (this slice's starting HEAD) is not reachable in this clone's history -- cannot count new go.mod module trees"
fi

# --------------------------------------------------------------------------
# P2e: internal/jsonpatch's schema patch, applied once per runtime build and
# never per request (A13), plus three internal/recipes kinds the engine did
# not have before this slice -- faker, template, sequence. Criterion checks
# C1-C4 and C12 (context.md §J) land here; C5-C11 are Go tests owned by
# other sections of this same slice.
#
# PLACEMENT: after the MCP block, before the P1e MOCKER_ROUTING=path switch
# just below -- every check here drives the mock plane by Host plus a bare
# path ("Host: <slug>.mock.local" + "/widgets", "/status-variants", ...),
# exactly the shape P1e's own comment already says breaks once path mode
# lands (a check going red against correct code, not a real defect).
#
# ITS OWN WORKSPACE (§G1): p2e_ws_id, never $ws_id. C4 PATCHes listSize to
# 25 -- run that against $ws_id and it invalidates P2b's own exact
# list-length assertions earlier in this file, the identical reason P2d's
# own p2d_ws_id note gives for creating a workspace of its own instead of
# reusing the shared one. p2e_checks is this block's silent-skip guard,
# p2c_checks/p2d_checks' own twin -- asserted against a hard total at the
# very end, outside any guard this block introduces.
# --------------------------------------------------------------------------
echo "== P2e: schema patch (internal/jsonpatch) and three recipe kinds, criterion checks C1-C4 and C12 =="

p2e_checks=0

p2e_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces "$(jq -cn --arg n "p2e-main" --argjson s "$spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p2e_ws_id=$(jq -r '.id' "$BODY_FILE")
p2e_slug=$(jq -r '.slug' "$BODY_FILE")
p2e_host="${p2e_slug}.${WORKSPACE_HOST_BASE}"
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_ws_status" != "201" || -z "$p2e_ws_id" || "$p2e_ws_id" == "null" ]]; then
	echo "FAIL  P2e setup: create p2e_ws with specId already attached -- want status 201, got ${p2e_ws_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2e setup: p2e_ws ${p2e_slug} (id ${p2e_ws_id}) created with its spec already attached"
fi

# opKey is method + relative path, percent-encoded (overrides.OpKey), the
# same @uri-against-PathEscape equivalence detail_opkey/list_opkey already
# rely on above.
p2e_create_opkey=$(jq -rn --arg s "POST /widgets" '$s | @uri')
p2e_list_opkey=$(jq -rn --arg s "GET /widgets" '$s | @uri')
p2e_media_opkey=$(jq -rn --arg s "GET /example-media" '$s | @uri')
p2e_schema_opkey=$(jq -rn --arg s "GET /example-schema" '$s | @uri')
p2e_statusvar_opkey=$(jq -rn --arg s "GET /status-variants" '$s | @uri')

# --------------------------------------------------------------------------
# C1 and C3 share one PUT: POST /widgets (201) and GET /widgets/{id} (200)
# both $ref #/components/schemas/Widget -- the SAME resolved map
# (internal/specs/index.go:282-294, internal/openapi/resolver.go:57-59; F35
# verified they resolve identically). Patching the FIRST operation and
# requesting the SECOND in this same process, same cached runtime (no admin
# write happens between the two mock-plane requests below, so the runtime
# built for p2e_ws's current revision is reused, never rebuilt) is what C3
# needs to prove the shared node was deep-copied, not patched in place; C1
# is the cheap presence control riding the same override, on the one branch
# with no example at all, so a C2 failure can be localised to the example
# gate rather than to the apply itself.
# --------------------------------------------------------------------------
p2e_c1_ev=$(op_edit_version "$p2e_ws_id" "$p2e_create_opkey")
p2e_c1_put_body=$(jq -n --argjson ev "$p2e_c1_ev" '{
	overrideOn: true,
	routeOff: false,
	responses: {
		"201": {mode: "generated", schemaPatch: [{op: "add", path: "/properties/extra", value: {type: "string"}}]}
	},
	editVersion: $ev
}')
p2e_c1_put_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2e_ws_id}/operations/${p2e_create_opkey}" "$p2e_c1_put_body" -H "X-CSRF-Token: ${csrf}")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_c1_put_status" != "200" ]]; then
	echo "FAIL  C1/C3 setup: PUT a schemaPatch override on POST /widgets (201) -- want status 200, got ${p2e_c1_put_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C1/C3 setup: schemaPatch override stored on POST /widgets (201)"
fi

p2e_c1_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X POST -H "Host: ${p2e_host}" \
	-H 'Content-Type: application/json' --data '{"name":"c1-widget","kind":"c1"}' "${BASE_URL}/widgets")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_c1_status" != "201" ]] || ! jq -e 'has("extra")' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  C1: POST /widgets after a schemaPatch override on a NON-LIST operation with no example -- want status 201 with an 'extra' key, got status ${p2e_c1_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C1: the patch is applied through the deployed image (POST /widgets carries 'extra')"
fi

p2e_c3_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p2e_host}" "${BASE_URL}/widgets/1")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_c3_status" != "200" ]] || jq -e 'has("extra")' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  C3: GET /widgets/{id}, unpatched, same process and cached runtime as C1's POST -- want status 200 with NO 'extra' key (the shared Widget node must not be poisoned), got status ${p2e_c3_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C3: GET /widgets/{id} does not carry 'extra' -- the deep copy left the shared resolved node untouched"
fi

# --------------------------------------------------------------------------
# C2: the patch must survive BOTH whole-body example short-circuits --
# gen.go's contentExample (media-type "example") and leafValue's root check
# (schema-level "example"). Both fixture operations are non-list, both carry
# a real schema with "properties", and the SAME add-extra patch applies to
# both -- reused as p2e_patch200_body since both responses are keyed "200".
# --------------------------------------------------------------------------
# A3: editVersion 0 (D3's "I expect no row") is correct for all THREE reuses
# below -- p2e_media_opkey, p2e_schema_opkey and p2e_statusvar_opkey are
# three DISTINCT operations, each genuinely un-overridden on this
# fresh-per-block workspace before its own PUT below, so op_edit_version
# would answer 0 at each of the three call sites too; hard-coding it once
# here, on the one body literal all three share, says the same thing without
# three identical reads of three rows this workspace has never touched.
p2e_patch200_body=$(jq -n '{
	overrideOn: true,
	routeOff: false,
	responses: {
		"200": {mode: "generated", schemaPatch: [{op: "add", path: "/properties/extra", value: {type: "string"}}]}
	},
	editVersion: 0
}')

p2e_media_put_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2e_ws_id}/operations/${p2e_media_opkey}" "$p2e_patch200_body" -H "X-CSRF-Token: ${csrf}")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_media_put_status" != "200" ]]; then
	echo "FAIL  C2 setup: PUT a schemaPatch override on GET /example-media (200, media-type example) -- want status 200, got ${p2e_media_put_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C2 setup: schemaPatch override stored on GET /example-media"
fi

p2e_media_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p2e_host}" "${BASE_URL}/example-media")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_media_status" != "200" ]] || ! jq -e 'has("extra")' "$BODY_FILE" >/dev/null 2>&1 || grep -qF 'MEDIA_TYPE_EXAMPLE_MARKER' "$BODY_FILE"; then
	echo "FAIL  C2 (media-type example): GET /example-media -- want status 200, an 'extra' key present, and the media-type example's own values GONE; got status ${p2e_media_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C2 (media-type example): the patch survives the media-type example short-circuit -- 'extra' present, the example's marker gone"
fi

p2e_schema_put_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2e_ws_id}/operations/${p2e_schema_opkey}" "$p2e_patch200_body" -H "X-CSRF-Token: ${csrf}")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_schema_put_status" != "200" ]]; then
	echo "FAIL  C2 setup: PUT a schemaPatch override on GET /example-schema (200, schema-level example) -- want status 200, got ${p2e_schema_put_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C2 setup: schemaPatch override stored on GET /example-schema"
fi

p2e_schema_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p2e_host}" "${BASE_URL}/example-schema")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_schema_status" != "200" ]] || ! jq -e 'has("extra")' "$BODY_FILE" >/dev/null 2>&1 || grep -qF 'SCHEMA_EXAMPLE_MARKER' "$BODY_FILE"; then
	echo "FAIL  C2 (schema-level example): GET /example-schema -- want status 200, an 'extra' key present, and the schema example's own values GONE; got status ${p2e_schema_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C2 (schema-level example): the patch survives leafValue's root-example short-circuit -- 'extra' present, the example's marker gone"
fi

# --------------------------------------------------------------------------
# C12: the selector key tripwire. /status-variants declares BOTH a "200"
# and a "default" response; classifySelector maps both to http_status 200
# (internal/specs/index.go:202-209), so a patched-schema cache keyed by
# HTTPStatus rather than by Selector collides here -- the two spec variants
# share one key, and (per the sort classifySelector's own emission order
# gives them) "default" is built after "200" and would overwrite it. Only
# the "200" response is patched; buildPatchedSchemas applies that ONE
# stored patch to every spec variant sharing its HTTPStatus, so a root for
# "default" gets built too, from the DEFAULT schema, as a side effect. A
# status-keyed build then hands a request that actually lands on 200 the
# default variant's own root instead.
# --------------------------------------------------------------------------
p2e_statusvar_put_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2e_ws_id}/operations/${p2e_statusvar_opkey}" "$p2e_patch200_body" -H "X-CSRF-Token: ${csrf}")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_statusvar_put_status" != "200" ]]; then
	echo "FAIL  C12 setup: PUT a schemaPatch override on GET /status-variants (its \"200\" response) -- want status 200, got ${p2e_statusvar_put_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C12 setup: schemaPatch override stored on GET /status-variants' \"200\" response"
fi

p2e_statusvar_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p2e_host}" "${BASE_URL}/status-variants")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_statusvar_status" != "200" ]] || ! jq -e 'has("onlyIn200")' "$BODY_FILE" >/dev/null 2>&1 || jq -e 'has("onlyInDefault")' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  C12: GET /status-variants -- want status 200 carrying the 200 schema's own 'onlyIn200' and NOT the default schema's 'onlyInDefault' (a status-keyed build serves the wrong variant's root here), got status ${p2e_statusvar_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C12: GET /status-variants carries the 200 schema's own property, never the default variant's (selector key tripwire; not a proof -- see this block's own header)"
fi

# --------------------------------------------------------------------------
# C4: sequence and template derive from the GLOBAL array position, not a
# per-page counter -- proven at a NON-ZERO offset, the one case a
# per-request counter cannot fake (it would restart at 0 on every page).
# listSize -> 25 first: the default (5) makes offset 10 an empty page, which
# would make this check vacuous rather than red. Both recipes bind to
# STRING properties (items[*].name, items[*].status) because generateItems
# overwrites the item's own "id" property after the walk, discarding
# anything bound there.
# --------------------------------------------------------------------------
p2e_listsize_status=$(p2c_set_settings "$p2e_ws_id" ".listSize = 25")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_listsize_status" != "200" ]]; then
	echo "FAIL  C4 setup: PATCH p2e_ws's listSize to 25 -- want status 200, got ${p2e_listsize_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C4 setup: p2e_ws's listSize is 25"
fi

p2e_c4_ev=$(op_edit_version "$p2e_ws_id" "$p2e_list_opkey")
p2e_c4_put_body=$(jq -n --argjson ev "$p2e_c4_ev" '{
	overrideOn: true,
	routeOff: false,
	responses: {
		"200": {mode: "generated", recipes: {
			"items[*].name": {kind: "sequence", value: {start: 1000, step: 1}},
			"items[*].status": {kind: "template", value: "Widget #{{index}}"}
		}}
	},
	editVersion: $ev
}')
p2e_c4_put_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2e_ws_id}/operations/${p2e_list_opkey}" "$p2e_c4_put_body" -H "X-CSRF-Token: ${csrf}")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_c4_put_status" != "200" ]]; then
	echo "FAIL  C4 setup: PUT sequence+template recipes on GET /widgets (items[*].name, items[*].status) -- want status 200, got ${p2e_c4_put_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C4 setup: sequence (items[*].name) and template (items[*].status) recipes bound on GET /widgets"
fi

p2e_c4_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p2e_host}" "${BASE_URL}/widgets?offset=10")
p2e_c4_name=$(jq -r '.items[0].name // empty' "$BODY_FILE")
p2e_c4_row_status=$(jq -r '.items[0].status // empty' "$BODY_FILE")
p2e_checks=$((p2e_checks + 1))
if [[ "$p2e_c4_status" != "200" || "$p2e_c4_name" != "1010" || "$p2e_c4_row_status" != "Widget #10" ]]; then
	echo "FAIL  C4: GET /widgets?offset=10 (listSize=25) -- want status 200, items[0].name=1010 (sequence, coerced to string), items[0].status='Widget #10' (template); got status ${p2e_c4_status} name='${p2e_c4_name}' status='${p2e_c4_row_status}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C4: at offset 10, sequence and template both derive from the global position (name=1010, status='Widget #10') -- a per-request counter would have failed this"
fi

p2e_want_checks=13
if [[ "$p2e_checks" != "$p2e_want_checks" ]]; then
	echo "FAIL  P2e acceptance (whole section): expected exactly ${p2e_want_checks} checks to have run, ${p2e_checks} actually ran -- this block was short-circuited somewhere before reaching here"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2e acceptance (whole section): all ${p2e_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# P2f: POST /api/workspaces/{id}/preview -- one route that answers with the
# response the mock plane WOULD serve for a draft operation override once
# saved, writing nothing (A1). Criterion checks C1-C5 (context.md §I) land
# here.
#
# PLACEMENT: after the P2e block above, before the P1e MOCKER_ROUTING=path
# switch just below -- every check here addresses the mock plane by Host
# plus a bare path, the same reason P2e's own placement comment gives.
#
# ITS OWN WORKSPACE (§I): p2f_ws_id, never $ws_id or p2e_ws_id -- C2 PUTs a
# real override and C3 arms a session-wide fail directive, either of which
# would poison assertions any other block already made against a workspace
# it does not own. p2f_ws's basePath stays empty: the fixture document
# declares no "servers" block, so nothing here needs to touch it.
# p2f_checks is this block's own silent-skip guard, p2e_checks' twin.
# --------------------------------------------------------------------------
echo "== P2f: draft preview (POST /api/workspaces/{id}/preview), criterion checks C1-C5 =="

p2f_checks=0

p2f_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces "$(jq -cn --arg n "p2f-main" --argjson s "$spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p2f_ws_id=$(jq -r '.id' "$BODY_FILE")
p2f_slug=$(jq -r '.slug' "$BODY_FILE")
p2f_host="${p2f_slug}.${WORKSPACE_HOST_BASE}"
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_ws_status" != "201" || -z "$p2f_ws_id" || "$p2f_ws_id" == "null" ]]; then
	echo "FAIL  P2f setup: create p2f_ws with specId already attached -- want status 201, got ${p2f_ws_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2f setup: p2f_ws ${p2f_slug} (id ${p2f_ws_id}) created with its spec already attached"
fi

# opKey is method + relative path, percent-encoded (overrides.OpKey) -- the
# preview route's own JSON field carries the identical percent-encoded
# string PUT's URL segment does (overrides.ParseOpKey calls
# url.PathUnescape on it either way), so @uri here is the same computation
# P2e's own opKey variables above already use, not a new convention.
p2f_list_opkey=$(jq -rn --arg s "GET /widgets" '$s | @uri')
p2f_detail_opkey=$(jq -rn --arg s "GET /widgets/{id}" '$s | @uri')

# --------------------------------------------------------------------------
# C1 and C2 share one draft (§I: "the same operation C1 uses"). GET
# /widgets is a collection operation -- no path parameter, so no pathParams
# is ever required, and its schema carries no clock-derived field, no "now"
# and no "jwt" recipe (it is the plain items/total/limit/offset wrapper
# P1b's own fixture declares). The schemaPatch adds one property, mirroring
# P2e's own C1 patch shape exactly so a reviewer already familiar with that
# block recognises this one at a glance.
# --------------------------------------------------------------------------
p2f_draft=$(jq -n '{
	overrideOn: true,
	routeOff: false,
	responses: {
		"200": {mode: "generated", schemaPatch: [{op: "add", path: "/properties/extra", value: {type: "string"}}]}
	}
}')
p2f_preview_req=$(jq -cn --arg op "$p2f_list_opkey" --argjson d "$p2f_draft" '{opKey: $op, draft: $d}')

# curl direct, not http_json: C1 needs the preview response's OWN
# Content-Type header, which http_json's status/body split throws away.
p2f_c1_headers=$(mktemp)
p2f_c1_status=$(curl -s -c "$COOKIE_JAR" -b "$COOKIE_JAR" -o "$BODY_FILE" -D "$p2f_c1_headers" -w '%{http_code}' \
	-X POST -H "Host: ${ADMIN_HOST}" -H 'Content-Type: application/json' -H "Origin: http://${ADMIN_HOST}" \
	-H "X-CSRF-Token: ${csrf}" --data "$p2f_preview_req" "${BASE_URL}/api/workspaces/${p2f_ws_id}/preview")
p2f_c1_body_type=$(jq -r '.body | type' "$BODY_FILE")
p2f_c1_body_text=$(jq -r '.body' "$BODY_FILE")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c1_status" != "200" ]] || ! jq -e 'has("extra")' <<<"$p2f_c1_body_text" >/dev/null 2>&1; then
	echo "FAIL  C1: POST .../preview on GET /widgets with a schemaPatch draft -- want status 200 and a previewed body carrying 'extra', got status ${p2f_c1_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C1: the previewed body carries the schemaPatch's own 'extra' field"
fi

p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c1_body_type" != "string" ]]; then
	echo "FAIL  C1: previewResultView.body must arrive as a JSON STRING field (D5), not raw bytes spliced into the envelope -- got jq type '${p2f_c1_body_type}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C1: the previewed body is a JSON string field, not raw response bytes"
fi

p2f_c1_content_type=$(grep -i '^content-type:' "$p2f_c1_headers" | head -n1 | tr -d '\r')
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c1_content_type" != *"application/json"* ]]; then
	echo "FAIL  C1: the preview route's OWN response Content-Type must be application/json regardless of the previewed media type -- got '${p2f_c1_content_type}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C1: the preview response's own Content-Type is application/json (${p2f_c1_content_type})"
fi
rm -f "$p2f_c1_headers"

p2f_c1_get_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/operations/${p2f_list_opkey}")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c1_get_status" != "404" ]]; then
	echo "FAIL  C1: GET .../operations/${p2f_list_opkey} after a preview-only call -- want status 404 (nothing was ever saved), got ${p2f_c1_get_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C1: the draft was never saved -- GET .../operations/{opKey} answers 404, not merely 'unchanged'"
fi

# --------------------------------------------------------------------------
# C2: the preview equals what the plane then serves, BYTE FOR BYTE. p2f_c1_body_text
# above is the previewed body captured before anything was written --
# reused here rather than previewing a second time, so a divergence between
# the two calls can only come from the PUT/serve path this check exists to
# prove, never from two independent preview computations disagreeing with
# each other.
# --------------------------------------------------------------------------
# A3: p2f_draft itself stays untouched (the preview call above sent it
# unmodified, and preview_operation.md's own preview route -- P2f, outside
# A3's five-route population -- takes no editVersion at all); this PUT gets
# its own copy with the current token spliced in. p2f_list_opkey has never
# been overridden on this fresh workspace, so 0 is what op_edit_version
# answers.
p2f_c2_ev=$(op_edit_version "$p2f_ws_id" "$p2f_list_opkey")
p2f_draft_put=$(jq -c --argjson ev "$p2f_c2_ev" '.editVersion = $ev' <<<"$p2f_draft")
p2f_c2_put_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/operations/${p2f_list_opkey}" "$p2f_draft_put" -H "X-CSRF-Token: ${csrf}")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c2_put_status" != "200" ]]; then
	echo "FAIL  C2 setup: PUT the SAME draft C1 previewed onto GET /widgets -- want status 200, got ${p2f_c2_put_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C2 setup: the draft is now saved for real on GET /widgets"
fi

p2f_c2_served_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p2f_host}" "${BASE_URL}/widgets")
p2f_c2_served_text=$(cat "$BODY_FILE")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c2_served_status" != "200" || "$p2f_c2_served_text" != "$p2f_c1_body_text" ]]; then
	echo "FAIL  C2: GET /widgets on the mock plane after saving C1's draft -- want status 200 and a body BYTE FOR BYTE equal to the previewed body; got status ${p2f_c2_served_status}, served='${p2f_c2_served_text}', previewed='${p2f_c1_body_text}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C2: the served body is byte-for-byte identical to what the preview call answered before the draft was ever saved"
fi

p2f_checks=$((p2f_checks + 1))
if ! jq -e 'has("extra")' <<<"$p2f_c2_served_text" >/dev/null 2>&1; then
	echo "FAIL  C2 (localiser): the served body should still carry 'extra' on its own -- got: ${p2f_c2_served_text}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C2 (localiser): the served body carries 'extra' on its own, independent of the whole-body comparison above"
fi

# --------------------------------------------------------------------------
# C3: nothing is consumed. A wrong preview that reached livestate.Apply
# would spend the "once" fail unit on the preview call itself, and the
# REAL request right after it would then serve cleanly instead of failing
# -- this check would stay green over exactly the bug it exists to catch
# unless it asserts BOTH the preview's own 200 and the real request's 500.
# --------------------------------------------------------------------------
p2f_c3_arm_body=$(jq -n '{target: "*", action: "fail", status: 500, once: true}')
p2f_c3_arm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/session" "$p2f_c3_arm_body" -H "X-CSRF-Token: ${csrf}")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c3_arm_status" != "200" ]]; then
	echo "FAIL  C3 setup: arm a once-only fail-500 session directive on p2f_ws -- want status 200, got ${p2f_c3_arm_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C3 setup: a once-only fail-500 session directive is armed on p2f_ws"
fi

p2f_c3_preview_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X POST -H "Host: ${ADMIN_HOST}" \
	-H 'Content-Type: application/json' -H "Origin: http://${ADMIN_HOST}" -H "X-CSRF-Token: ${csrf}" \
	-b "$COOKIE_JAR" -c "$COOKIE_JAR" --data "$p2f_preview_req" "${BASE_URL}/api/workspaces/${p2f_ws_id}/preview")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c3_preview_status" != "200" ]]; then
	echo "FAIL  C3: preview call made WHILE a once-only fail directive is armed -- want status 200 (preview must not consume it), got ${p2f_c3_preview_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C3: the preview call answers 200 with the fail directive still armed -- a refusal here could not be mistaken for 'consumed nothing'"
fi

p2f_c3_real_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p2f_host}" "${BASE_URL}/widgets")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c3_real_status" != "500" ]]; then
	echo "FAIL  C3: the REAL request right after the preview call -- want status 500 (the once-only directive is still there, untouched by preview), got ${p2f_c3_real_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C3: the real request is the one that fails -- preview consumed nothing from the session's once-only directive"
fi

# --------------------------------------------------------------------------
# C4: a custom endpoint at the SAME (method, path) as a spec operation
# outranks it -- proven with an operation C1/C2 never touch (GET
# /widgets/{id}, a custom endpoint at equal specificity would answer C2's
# own mock-plane request otherwise) that carries no op_overrides row of its
# own, and no pathParams sent, the only input combination that
# distinguishes the ordered taxonomy (custom endpoint checked before
# missing-path-param, internal/mockplane/preview.go's own locatePreviewRoute
# running ahead of firstMissingPathParam) from a mis-ordered one.
# --------------------------------------------------------------------------
p2f_c4_ep_body=$(jq -n '{method: "GET", path: "/widgets/{id}", status: 200, body: {marker: "p2f-custom-wins"}}')
p2f_c4_ep_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/endpoints" "$p2f_c4_ep_body" -H "X-CSRF-Token: ${csrf}")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c4_ep_status" != "201" ]]; then
	echo "FAIL  C4 setup: create a custom endpoint GET /widgets/{id} (same method+path as the spec operation) -- want status 201, got ${p2f_c4_ep_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C4 setup: custom endpoint GET /widgets/{id} created, no op_overrides row exists for the spec operation of the same shape"
fi

p2f_c4_preview_req=$(jq -cn --arg op "$p2f_detail_opkey" '{opKey: $op, draft: {overrideOn: true, routeOff: false, responses: {"200": {mode: "generated"}}}}')
p2f_c4_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X POST -H "Host: ${ADMIN_HOST}" \
	-H 'Content-Type: application/json' -H "Origin: http://${ADMIN_HOST}" -H "X-CSRF-Token: ${csrf}" \
	-b "$COOKIE_JAR" -c "$COOKIE_JAR" --data "$p2f_c4_preview_req" "${BASE_URL}/api/workspaces/${p2f_ws_id}/preview")
p2f_checks=$((p2f_checks + 1))
if [[ "$p2f_c4_status" != "409" ]] || [[ "$(jq -r '.error.code // empty' "$BODY_FILE")" != "custom_endpoint_wins" ]]; then
	echo "FAIL  C4: preview GET /widgets/{id} with NO pathParams while a custom endpoint occupies the same (method, path) -- want status 409 code custom_endpoint_wins, got status ${p2f_c4_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  C4: the custom endpoint's precedence is refused BEFORE the missing pathParam is ever reached -- 409 custom_endpoint_wins"
fi

# C5 has no assertion of its own: it is proven by C1 running at all through
# make smoke -- a Server whose Previewer was never wired (cmd/mocker/main.go's
# SetPreviewer call missing or misplaced) would answer every preview call in
# this block 503, and C1's own status check above would be the one that goes
# red, not a check written to say "the route is wired" in so many words.

p2f_want_checks=13
if [[ "$p2f_checks" != "$p2f_want_checks" ]]; then
	echo "FAIL  P2f acceptance (whole section): expected exactly ${p2f_want_checks} checks to have run, ${p2f_checks} actually ran -- this block was short-circuited somewhere before reaching here"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2f acceptance (whole section): all ${p2f_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# A3 (mocker-a3-cas): per-row compare-and-swap over the write population's
# five object-addressed routes. Every write site above sends the CORRECT
# token and succeeds -- none of them proves the REFUSAL half of D13
# property 1, and none of them touches PUT .../endpoints/{eid} at all
# (grep -c 'PUT "\$ADMIN_HOST" .*\/endpoints\/' scripts/smoke.sh, run before
# this block existed, returns 0 -- a required-expectation route this bar
# would otherwise never reach). This block closes both gaps on the live
# stack, reusing p2f_ws (host mode, spec already attached) rather than
# standing up a workspace of its own.
# --------------------------------------------------------------------------
echo "== A3: per-row compare-and-swap -- PUT .../endpoints/{eid}, and the stale-token refusal end to end =="

a3_checks=0

echo "== A3: create a custom endpoint, then PUT a full replacement with its own editVersion =="
a3_ep_create_body=$(jq -n '{method: "GET", path: "/a3-endpoint", status: 200, body: {marker: "a3-v1"}}')
a3_ep_create_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/endpoints" "$a3_ep_create_body" -H "X-CSRF-Token: ${csrf}")
a3_ep_id=$(jq -r '.id' "$BODY_FILE")
a3_ep_create_ev=$(jq -r '.editVersion' "$BODY_FILE")
a3_checks=$((a3_checks + 1))
if [[ "$a3_ep_create_status" != "201" || -z "$a3_ep_id" || "$a3_ep_id" == "null" ]]; then
	echo "FAIL  A3 endpoint setup: create GET /a3-endpoint: want status 201, got ${a3_ep_create_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  A3 endpoint setup: GET /a3-endpoint created (id ${a3_ep_id}, editVersion ${a3_ep_create_ev})"
fi

# PUT .../endpoints/{eid} is a FULL replacement, not a merge -- resend the
# whole document (method/path/status/body) with a changed body plus the
# CREATE response's own editVersion, the only read available for a row this
# call just made.
a3_ep_put_body=$(jq -n --argjson ev "$a3_ep_create_ev" '{
	method: "GET", path: "/a3-endpoint", overrideOn: true, routeOff: false,
	activeStatus: 200, responses: {"200": {mode: "pinned", body: {marker: "a3-v2"}}},
	editVersion: $ev
}')
a3_ep_put_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/endpoints/${a3_ep_id}" "$a3_ep_put_body" -H "X-CSRF-Token: ${csrf}")
a3_ep_put_new_ev=$(jq -r '.editVersion' "$BODY_FILE")
a3_checks=$((a3_checks + 1))
if [[ "$a3_ep_put_status" != "200" || "$a3_ep_put_new_ev" == "$a3_ep_create_ev" || "$a3_ep_put_new_ev" == "null" ]]; then
	echo "FAIL  PUT .../endpoints/{eid}: want status 200 with a FRESH editVersion (not ${a3_ep_create_ev}), got status ${a3_ep_put_status} editVersion '${a3_ep_put_new_ev}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  PUT .../endpoints/{eid}: 200, editVersion ${a3_ep_create_ev} -> ${a3_ep_put_new_ev} -- this route's own required-expectation write site, absent everywhere else in this script"
fi

a3_ep_served=$(curl -s -H "Host: ${p2f_host}" "${BASE_URL}/a3-endpoint" | jq -r '.marker // empty')
a3_checks=$((a3_checks + 1))
if [[ "$a3_ep_served" != "a3-v2" ]]; then
	echo "FAIL  GET /a3-endpoint after the PUT: want marker a3-v2, got '${a3_ep_served}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  GET /a3-endpoint after the PUT: the full replacement actually took effect (marker a3-v2)"
fi

echo "== A3: a STALE editVersion is refused end to end (409 edit_conflict, atomic, retryable) =="

a3_op_opkey=$(jq -rn --arg s "GET /widgets/{id}" '$s | @uri')
a3_op_ev0=$(op_edit_version "$p2f_ws_id" "$a3_op_opkey")
a3_op_body1=$(jq -n --argjson ev "$a3_op_ev0" '{overrideOn:true, routeOff:false, responses:{"200":{mode:"generated", recipes:{name:{kind:"const", value:"A3-FIRST"}}}}, editVersion:$ev}')
a3_op_put1_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/operations/${a3_op_opkey}" "$a3_op_body1" -H "X-CSRF-Token: ${csrf}")
a3_op_ev1=$(jq -r '.editVersion' "$BODY_FILE")
a3_checks=$((a3_checks + 1))
if [[ "$a3_op_put1_status" != "200" || "$a3_op_ev1" == "$a3_op_ev0" || "$a3_op_ev1" == "null" ]]; then
	echo "FAIL  A3 stale-token setup: first PUT (editVersion ${a3_op_ev0}) -- want status 200 with a FRESH editVersion, got status ${a3_op_put1_status} editVersion '${a3_op_ev1}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

# THE ASSERTION this whole block exists for: a second write reusing the OLD
# (now stale) $a3_op_ev0 -- what a screen sends after writing twice off one
# read, or what a lost admin-panel edit racing another writer looks like on
# the wire. D13 properties 1 and 4: the refusal is 409 code=edit_conflict,
# ATOMIC (asserted separately right below), carrying `details` the caller
# can actually retry from -- the CURRENT document (A3-FIRST survives) and
# its real editVersion, never a bare status or a generic message. Reverting
# PutExpecting to the unguarded Put (D8's own unguarded verb) would answer
# this call 200 instead of 409 -- that is the production change this
# assertion is a test FOR.
a3_op_body_stale=$(jq -n --argjson ev "$a3_op_ev0" '{overrideOn:true, routeOff:false, responses:{"200":{mode:"generated", recipes:{name:{kind:"const", value:"A3-SHOULD-NEVER-LAND"}}}}, editVersion:$ev}')
a3_op_stale_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/operations/${a3_op_opkey}" "$a3_op_body_stale" -H "X-CSRF-Token: ${csrf}")
a3_op_stale_code=$(jq -r '.error.code // empty' "$BODY_FILE")
a3_op_stale_details_ev=$(jq -r '.error.details.editVersion // empty' "$BODY_FILE")
a3_op_stale_details_name=$(jq -r '.error.details.responses["200"].recipes.name.value // empty' "$BODY_FILE")
a3_checks=$((a3_checks + 1))
if [[ "$a3_op_stale_status" != "409" || "$a3_op_stale_code" != "edit_conflict" ||
	"$a3_op_stale_details_ev" != "$a3_op_ev1" || "$a3_op_stale_details_name" != "A3-FIRST" ]]; then
	echo "FAIL  A3 stale-token refusal: PUT reusing the OLD editVersion ${a3_op_ev0} -- want status 409 code=edit_conflict with details.editVersion=${a3_op_ev1} and details naming the CURRENT document (recipe A3-FIRST survives), got status ${a3_op_stale_status} code '${a3_op_stale_code}' details.editVersion '${a3_op_stale_details_ev}' details-name '${a3_op_stale_details_name}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  A3 stale-token refusal: 409 edit_conflict, details carry the CURRENT editVersion (${a3_op_stale_details_ev}) and the current document (name=A3-FIRST) -- a caller can retry from this without re-reading"
fi

# D13 property 6, qualified: the refused write left the four tokened
# tables byte-identical -- a fresh read's editVersion is still a3_op_ev1,
# not bumped by the write that was supposed to have been refused.
a3_op_ev_after=$(op_edit_version "$p2f_ws_id" "$a3_op_opkey")
a3_checks=$((a3_checks + 1))
if [[ "$a3_op_ev_after" != "$a3_op_ev1" ]]; then
	echo "FAIL  A3 stale-token refusal: the row's editVersion moved from ${a3_op_ev1} to ${a3_op_ev_after} despite the refused write -- the refusal was not atomic"
	fail_count=$((fail_count + 1))
else
	echo "PASS  A3 stale-token refusal changed nothing: editVersion is still ${a3_op_ev_after}"
fi

# The retry, with the CONFLICT'S OWN editVersion (a3_op_ev1, exactly what
# a3_op_stale_details_ev just carried) -- proves the payload is genuinely
# usable to retry from, not merely present on the wire.
a3_op_retry_body=$(jq -n --argjson ev "$a3_op_ev1" '{overrideOn:true, routeOff:false, responses:{"200":{mode:"generated", recipes:{name:{kind:"const", value:"A3-RETRY-OK"}}}}, editVersion:$ev}')
a3_op_retry_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p2f_ws_id}/operations/${a3_op_opkey}" "$a3_op_retry_body" -H "X-CSRF-Token: ${csrf}")
a3_checks=$((a3_checks + 1))
if [[ "$a3_op_retry_status" != "200" ]]; then
	echo "FAIL  A3 retry with the conflict's own editVersion: want status 200, got ${a3_op_retry_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  A3 retry with the conflict's own editVersion succeeds -- the 409's details are actually usable to retry from"
fi

a3_want_checks=7
if [[ "$a3_checks" != "$a3_want_checks" ]]; then
	echo "FAIL  A3 acceptance (whole section): expected exactly ${a3_want_checks} checks to have run, ${a3_checks} actually ran -- this block was short-circuited somewhere before reaching here"
	fail_count=$((fail_count + 1))
else
	echo "PASS  A3 acceptance (whole section): all ${a3_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# P3a: the mock plane REMEMBERS a write (DESIGN §19, mocker-p3a-resources).
# DESIGN §19's own readiness criterion, verbatim: "created it -- saw it in
# the list". This is the ONLY place in the whole run that proves it end to
# end against a REAL BINARY in a REAL CONTAINER, and it is deliberately the
# one property no Go test holds (D13 clause 3): every internal/mockplane
# test wires a fake ResourceSource/EntityStore directly and would stay
# green even with SetResources/SetEntities missing from
# cmd/mocker/main.go -- exactly the shape this repository has twice shipped
# a feature dead in prod behind. Confirming this run's setters actually
# reached main.go REQUIRES walking POST -> GET(list) -> GET(detail) ->
# DELETE -> GET(detail) 404 against the compose stack that main.go itself
# assembles.
#
# ITS OWN SPEC AND ITS OWN WORKSPACE (§G1's own reasoning, P2e/P2d/P2f's own
# comments give it for the identical reason): $spec_id's /widgets family
# has no DELETE /widgets/{id} at all (P1c-2's when[] check is the only
# reason POST /widgets exists, and nothing in this file's earlier blocks
# ever needed a delete route), so it cannot carry this walk's DELETE leg
# regardless of workspace. Confirming /widgets on $ws_id (or on any
# workspace sharing $spec_id) would ALSO put the resource branch in front
# of P1c's pinned-409/201 checks and P2e's schemaPatch/traffic assertions on
# that exact route, turning make smoke red against a CORRECT
# implementation -- the trap this comment exists to name so nobody "fixes"
# it by weakening those older assertions instead. A dedicated inline
# document, imported once, attached to a dedicated workspace at creation
# (the same POST /api/workspaces {name,specId} shape p2e_ws/p2f_ws/p3a_ws
# all share) sidesteps both problems at once.
#
# THE SPEC ITSELF: one family, /items -- GET /items (bare JSON array, no
# wrapper, so this walk stays independent of R6's wrapper logic, which is a
# Go-test-owned clause) and its detail route GET /items/{id}; POST /items
# whose requestBody schema is the SAME "$ref": "#/components/schemas/Item"
# node the two GET responses use (D4/R12's two-hop write_form walk resolves
# both to the identical schema map, so this family derives write_form =
# "bare" without this script needing to know that rule's name); DELETE
# /items/{id} answering a bare 204 (R14: deletion is never gated on
# write_form at all). Item.id is untyped-integer with no format, so a
# server-assigned id compares as a bare number on the wire -- no UseNumber
# dance needed for a curl+jq walk that only checks equality.
#
# NOT SKIPPABLE (D13 clause 3's own text: "Defeats: the slice being
# decorative"). This file carries six SKIP branches elsewhere, each printing
# SKIP and exiting the surrounding block with status 0 for a precondition
# it could not construct -- copying that idiom here would let a missing
# SetResources/SetEntities wiring pass make smoke with this block simply
# never running. Every step below is instead a hard `exit 1` on the first
# unexpected status or missing field, exactly the shape the P1b import setup
# above this block already uses for its own irrecoverable setup steps.
# --------------------------------------------------------------------------
echo "== P3a: a resource remembers a write -- import, confirm, POST, GET(list), GET(detail), DELETE, GET(detail) 404 =="

p3a_doc=$(jq -n '{
	openapi: "3.0.3",
	info: {title: "P3a Items", version: "1.0.0"},
	paths: {
		"/items": {
			get: {
				operationId: "listItems",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {type: "array", items: {"$ref": "#/components/schemas/Item"}}
							}
						}
					}
				}
			},
			post: {
				operationId: "createItem",
				requestBody: {
					content: {
						"application/json": {
							schema: {"$ref": "#/components/schemas/Item"}
						}
					}
				},
				responses: {
					"201": {
						description: "created",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/Item"}
							}
						}
					}
				}
			}
		},
		"/items/{id}": {
			get: {
				operationId: "getItem",
				parameters: [{name: "id", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/Item"}
							}
						}
					}
				}
			},
			delete: {
				operationId: "deleteItem",
				parameters: [{name: "id", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"204": {description: "deleted"}
				}
			}
		}
	},
	components: {
		schemas: {
			Item: {
				type: "object",
				properties: {
					id: {type: "integer"},
					name: {type: "string"}
				},
				required: ["id"]
			}
		}
	}
}')

p3a_import_body=$(jq -n --arg name "P3a Items" --arg source "upload" --arg document "$p3a_doc" \
	'{name: $name, source: $source, document: $document}')
p3a_import_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p3a_import_body" -H "X-CSRF-Token: ${csrf}")
if [[ "$p3a_import_status" != "201" ]]; then
	echo "FAIL  P3a: import the /items spec: want status 201, got ${p3a_import_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3a_spec_id=$(jq -r '.id' "$BODY_FILE")
echo "PASS  P3a: imported the /items spec (id ${p3a_spec_id})"

p3a_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces \
	"$(jq -cn --arg n "p3a-resources" --argjson s "$p3a_spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p3a_ws_id=$(jq -r '.id' "$BODY_FILE")
p3a_slug=$(jq -r '.slug' "$BODY_FILE")
if [[ "$p3a_ws_status" != "201" || -z "$p3a_ws_id" || "$p3a_ws_id" == "null" ]]; then
	echo "FAIL  P3a: create the dedicated workspace with specId already attached: want status 201, got ${p3a_ws_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3a_host="${p3a_slug}.${WORKSPACE_HOST_BASE}"
echo "PASS  P3a: dedicated workspace ${p3a_slug} (id ${p3a_ws_id}) created with the /items spec attached"

p3a_confirm_body=$(jq -n '{routeFamily: "/items", state: "confirmed"}')
p3a_confirm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3a_ws_id}/resource-decisions" "$p3a_confirm_body" -H "X-CSRF-Token: ${csrf}")
p3a_decision=$(jq -r '.family.decision' "$BODY_FILE")
p3a_resource_id=$(jq -r '.family.resourceId' "$BODY_FILE")
if [[ "$p3a_confirm_status" != "200" || "$p3a_decision" != "confirmed" || -z "$p3a_resource_id" || "$p3a_resource_id" == "null" ]]; then
	echo "FAIL  P3a: confirm the /items family: want status 200 decision=confirmed with a resourceId, got status ${p3a_confirm_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3a: /items confirmed (resourceId ${p3a_resource_id})"

echo "== P3a: GET /items (the seeded collection) =="
p3a_list_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3a_host}" "${BASE_URL}/items")
if [[ "$p3a_list_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3a: GET /items after confirm: want status 200 with a non-empty JSON array (the seeded population), got status ${p3a_list_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3a_seed_id=$(jq -r '.[0].id' "$BODY_FILE")
if [[ -z "$p3a_seed_id" || "$p3a_seed_id" == "null" ]]; then
	echo "FAIL  P3a: GET /items: could not take an id from the seeded row -- \$p3a_seed_id is empty, the exact precondition this walk cannot proceed without"
	exit 1
fi
echo "PASS  P3a: GET /items answers the seeded population, took id ${p3a_seed_id} from row 0"

echo "== P3a: GET /items/{id} for the seeded id =="
p3a_seed_detail_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3a_host}" "${BASE_URL}/items/${p3a_seed_id}")
if [[ "$p3a_seed_detail_status" != "200" ]] || ! jq -e --argjson id "$p3a_seed_id" '.id == $id' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3a: GET /items/${p3a_seed_id}: want status 200 with the same id, got status ${p3a_seed_detail_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3a: GET /items/${p3a_seed_id} answers the stored row"

echo "== P3a: POST /items -- the response carries a server-assigned id =="
# id:999999 is deliberately sent on the wire: R15's overwrite rule (D13
# clause 4) is a Go-test-owned property, not this block's -- but the
# assertion below (\$p3a_new_id != 999999) is free confirmation, on a real
# server, of the exact thing DESIGN §19's criterion promises: the id in the
# response is SERVER-assigned, not the one the client sent.
p3a_post_status=$(http_json POST "$p3a_host" /items '{"id":999999,"name":"smoke-created"}')
p3a_new_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$p3a_post_status" != "201" || -z "$p3a_new_id" || "$p3a_new_id" == "null" || "$p3a_new_id" == "999999" ]]; then
	echo "FAIL  P3a: POST /items: want status 201 with a server-assigned id distinct from the client-sent 999999, got status ${p3a_post_status} id '${p3a_new_id}': $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3a: POST /items created id ${p3a_new_id} (client-sent 999999 overwritten)"

echo "== P3a: GET /items now contains the created row (DESIGN §19's own words: \"saw it in the list\") =="
p3a_list2_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3a_host}" "${BASE_URL}/items")
if [[ "$p3a_list2_status" != "200" ]] || ! jq -e --argjson id "$p3a_new_id" 'any(.[]; .id == $id)' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3a: GET /items after the POST: want status 200 with id ${p3a_new_id} present, got status ${p3a_list2_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3a: GET /items contains the created row ${p3a_new_id} -- \"created it, saw it in the list\""

echo "== P3a: DELETE /items/{id} removes it =="
p3a_delete_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X DELETE -H "Host: ${p3a_host}" "${BASE_URL}/items/${p3a_new_id}")
if [[ "$p3a_delete_status" != "204" ]]; then
	echo "FAIL  P3a: DELETE /items/${p3a_new_id}: want status 204, got ${p3a_delete_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3a: DELETE /items/${p3a_new_id} answered 204"

echo "== P3a: GET /items/{id} on the deleted row is now 404 =="
p3a_gone_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3a_host}" "${BASE_URL}/items/${p3a_new_id}")
if [[ "$p3a_gone_status" != "404" ]]; then
	echo "FAIL  P3a: GET /items/${p3a_new_id} after DELETE: want status 404, got ${p3a_gone_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3a: GET /items/${p3a_new_id} after DELETE answers 404 -- the whole DESIGN §19 criterion held end to end, both setters reached cmd/mocker/main.go"

# --------------------------------------------------------------------------
# P3d acceptance property 9 (mocker-p3d-datasnap, D11): the checkpoint/
# rollback round trip over a confirmed resource's OWN rows, against the
# built image -- mutation-exempt (D11 property 9's own text: a mutation here
# costs a docker rebuild per attempt) so this walk against a real binary IS
# the property's whole verification, not a supplement to one.
#
# SITED HERE, in the P3a block, and not beside the P2c block's observation
# 14 above: this walk needs a DELETE leg over a CONFIRMED family, and
# $spec_id's /widgets family has none (the P3a block's own comment a few
# screens up says so in so many words) -- which is exactly why P3a already
# imports this dedicated /items document with GET, POST and DELETE and
# confirms its own workspace. Reusing $p3a_ws_id/$p3a_host/$p3a_slug and the
# family already confirmed above, rather than a second import, keeps this
# property from duplicating that fixture.
# --------------------------------------------------------------------------
echo "== P3d acceptance property 9: confirm (already done above), POST, checkpoint, DELETE, rollback restoreData:true, read the row back =="

p3d_post_status=$(http_json POST "$p3a_host" /items '{"name":"p3d-roundtrip"}')
p3d_row_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$p3d_post_status" != "201" || -z "$p3d_row_id" || "$p3d_row_id" == "null" ]]; then
	echo "FAIL  P3d property 9: POST /items: want status 201 with a server-assigned id, got status ${p3d_post_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3d property 9: POST /items created id ${p3d_row_id}"

p3d_ckpt_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3a_ws_id}/checkpoints" '{"label":"p3d-roundtrip"}' -H "X-CSRF-Token: ${csrf}")
p3d_ckpt_id=$(jq -r '.id' "$BODY_FILE")
p3d_ckpt_hasdata=$(jq -r '.hasData' "$BODY_FILE")
if [[ "$p3d_ckpt_status" != "201" || -z "$p3d_ckpt_id" || "$p3d_ckpt_id" == "null" || "$p3d_ckpt_hasdata" != "true" ]]; then
	echo "FAIL  P3d property 9: POST .../checkpoints with ${p3d_row_id} live: want status 201 hasData=true, got status ${p3d_ckpt_status} hasData=${p3d_ckpt_hasdata}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3d property 9: checkpoint ${p3d_ckpt_id} taken with hasData=true"

p3d_delete_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X DELETE -H "Host: ${p3a_host}" "${BASE_URL}/items/${p3d_row_id}")
if [[ "$p3d_delete_status" != "204" ]]; then
	echo "FAIL  P3d property 9: DELETE /items/${p3d_row_id}: want status 204, got ${p3d_delete_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3d_gone_status=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p3a_host}" "${BASE_URL}/items/${p3d_row_id}")
if [[ "$p3d_gone_status" != "404" ]]; then
	echo "FAIL  P3d property 9: GET /items/${p3d_row_id} after DELETE: want status 404, got ${p3d_gone_status}"
	exit 1
fi
echo "PASS  P3d property 9: DELETE /items/${p3d_row_id} removed it (404 confirmed before the rollback)"

p3d_rollback_body=$(jq -n --arg s "$p3a_slug" '{restoreData:true,confirmSlug:$s}')
p3d_rollback_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3a_ws_id}/rollback/${p3d_ckpt_id}" "$p3d_rollback_body" -H "X-CSRF-Token: ${csrf}")
p3d_rollback_restored=$(jq -r '.dataRestored' "$BODY_FILE")
if [[ "$p3d_rollback_status" != "200" || "$p3d_rollback_restored" != "true" ]]; then
	echo "FAIL  P3d property 9: POST .../rollback/${p3d_ckpt_id} {restoreData:true, confirmSlug:${p3a_slug}}: want status 200 dataRestored=true, got status ${p3d_rollback_status} dataRestored=${p3d_rollback_restored}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3d property 9: rollback to ${p3d_ckpt_id} with restoreData:true answered 200 dataRestored=true"

p3d_back_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3a_host}" "${BASE_URL}/items/${p3d_row_id}")
if [[ "$p3d_back_status" != "200" ]] || ! jq -e --argjson id "$p3d_row_id" '.id == $id' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3d property 9: GET /items/${p3d_row_id} after rollback: want status 200 with the restored row, got status ${p3d_back_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3d property 9: GET /items/${p3d_row_id} after rollback answers the row again -- the checkpoint/rollback round trip over entity data held end to end"

# --------------------------------------------------------------------------
# P3c: the "ref" recipe (DESIGN §9, mocker-p3c-ref-recipe) -- a generated
# body carries a value a confirmed resource really holds, instead of a
# plausible integer that matches nothing. This is the one property no Go
# test in internal/mockplane holds against a REAL BINARY in a REAL
# CONTAINER: every internal/mockplane test wires its own fake
# ResourceSource/EntityStore/OverrideSource directly (ref_test.go's own
# file header states the same precondition trap this comment is naming),
# so confirming cmd/mocker/main.go's setters actually reached each other --
# SetResources/SetEntities feeding a live [recipes.Ref] closure that a real
# stored op_overrides row actually invokes -- requires walking
# confirm -> read a real id -> PUT an active override -> GET the field back
# against the compose stack main.go itself assembles.
#
# ITS OWN SPEC AND ITS OWN WORKSPACE, the identical reasoning P2e/P2d/P2f/P3a
# give for theirs (this file's own comment above the P3a block): a FAMILY
# (so it can be confirmed at all -- D3 addresses by route_family, which
# does not exist without a collection AND a detail route) and, separately,
# an ORDINARY operation the confirm does NOT take over -- D8's own measured
# rule, "a ref never re-fires on a resource-served route": every operation
# of $p3a_ws_id's /items family (and of any workspace sharing $spec_id) IS
# resource-served the moment a family is confirmed there, so a ref bound
# inside it would never be evaluated at all (resourceBranch never calls
# gen.Body -- D8) and this whole block would pass while proving nothing.
# The one field a "ref" recipe can usefully bind here is on a route with NO
# matching detail counterpart of its own, so [router.ListFamily] never
# derives a family for it and [resourceBranch] never has a roster entry
# that could take it over.
#
# THE SPEC ITSELF: /subjects -- GET /subjects (bare array of
# Subject{id,name}) and GET /subjects/{id} -- a genuine family, confirmable
# the same way P3a's /items is; /quiz -- GET /quiz only, NO
# "/quiz/{id}" anywhere in this document, answering a single object
# {subjectId: integer} -- the ORDINARY operation the ref recipe binds to.
# --------------------------------------------------------------------------
echo "== P3c: the ref recipe -- confirm /subjects, bind a ref on ordinary GET /quiz, read back a real /subjects id =="

p3c_doc=$(jq -n '{
	openapi: "3.0.3",
	info: {title: "P3c Ref", version: "1.0.0"},
	paths: {
		"/subjects": {
			get: {
				operationId: "listSubjects",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {type: "array", items: {"$ref": "#/components/schemas/Subject"}}
							}
						}
					}
				}
			}
		},
		"/subjects/{id}": {
			get: {
				operationId: "getSubject",
				parameters: [{name: "id", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/Subject"}
							}
						}
					}
				}
			}
		},
		"/quiz": {
			get: {
				operationId: "getQuiz",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {
									type: "object",
									properties: {
										subjectId: {type: "integer"}
									},
									required: ["subjectId"]
								}
							}
						}
					}
				}
			}
		}
	},
	components: {
		schemas: {
			Subject: {
				type: "object",
				properties: {
					id: {type: "integer"},
					name: {type: "string"}
				},
				required: ["id"]
			}
		}
	}
}')

p3c_import_body=$(jq -n --arg name "P3c Ref" --arg source "upload" --arg document "$p3c_doc" \
	'{name: $name, source: $source, document: $document}')
p3c_import_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p3c_import_body" -H "X-CSRF-Token: ${csrf}")
if [[ "$p3c_import_status" != "201" ]]; then
	echo "FAIL  P3c: import the /subjects+/quiz spec: want status 201, got ${p3c_import_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3c_spec_id=$(jq -r '.id' "$BODY_FILE")
echo "PASS  P3c: imported the /subjects+/quiz spec (id ${p3c_spec_id})"

p3c_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces \
	"$(jq -cn --arg n "p3c-ref-recipe" --argjson s "$p3c_spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p3c_ws_id=$(jq -r '.id' "$BODY_FILE")
p3c_slug=$(jq -r '.slug' "$BODY_FILE")
if [[ "$p3c_ws_status" != "201" || -z "$p3c_ws_id" || "$p3c_ws_id" == "null" ]]; then
	echo "FAIL  P3c: create the dedicated workspace with specId already attached: want status 201, got ${p3c_ws_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3c_host="${p3c_slug}.${WORKSPACE_HOST_BASE}"
echo "PASS  P3c: dedicated workspace ${p3c_slug} (id ${p3c_ws_id}) created with the /subjects+/quiz spec attached"

p3c_confirm_body=$(jq -n '{routeFamily: "/subjects", state: "confirmed"}')
p3c_confirm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3c_ws_id}/resource-decisions" "$p3c_confirm_body" -H "X-CSRF-Token: ${csrf}")
p3c_decision=$(jq -r '.family.decision' "$BODY_FILE")
if [[ "$p3c_confirm_status" != "200" || "$p3c_decision" != "confirmed" ]]; then
	echo "FAIL  P3c: confirm the /subjects family: want status 200 decision=confirmed, got status ${p3c_confirm_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3c: /subjects confirmed"

echo "== P3c: GET /subjects -- the real ids a ref bound at /quiz's subjectId must land on =="
p3c_subjects_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3c_host}" "${BASE_URL}/subjects")
if [[ "$p3c_subjects_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3c: GET /subjects after confirm: want status 200 with a non-empty JSON array, got status ${p3c_subjects_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3c_subject_ids=$(jq -c '[.[].id]' "$BODY_FILE")
echo "PASS  P3c: GET /subjects answers the seeded population, real ids ${p3c_subject_ids}"

p3c_quiz_opkey=$(jq -rn --arg s "GET /quiz" '$s | @uri')
p3c_quiz_ev=$(op_edit_version "$p3c_ws_id" "$p3c_quiz_opkey")
p3c_override_body=$(jq -n --argjson ev "$p3c_quiz_ev" '{
	overrideOn: true,
	routeOff: false,
	responses: {
		"200": {
			mode: "generated",
			recipes: {
				subjectId: {kind: "ref", value: {family: "/subjects", property: "id", policy: "generate"}}
			}
		}
	},
	editVersion: $ev
}')
p3c_override_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p3c_ws_id}/operations/${p3c_quiz_opkey}" "$p3c_override_body" -H "X-CSRF-Token: ${csrf}")
if [[ "$p3c_override_status" != "200" ]]; then
	echo "FAIL  P3c: PUT an active override binding a ref recipe onto GET /quiz's subjectId: want status 200, got ${p3c_override_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3c: GET /quiz now carries an active override with a ref recipe bound at subjectId (family /subjects, property id, policy generate)"

echo "== P3c: GET /quiz -- subjectId must be one of /subjects' real ids, not a plausible-but-unrelated integer =="
p3c_quiz_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3c_host}" "${BASE_URL}/quiz")
if [[ "$p3c_quiz_status" != "200" ]]; then
	echo "FAIL  P3c: GET /quiz: want status 200, got ${p3c_quiz_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3c_quiz_subject_id=$(jq -r '.subjectId' "$BODY_FILE")
if [[ -z "$p3c_quiz_subject_id" || "$p3c_quiz_subject_id" == "null" ]]; then
	echo "FAIL  P3c: GET /quiz: subjectId is absent or null under policy generate -- the ref should have resolved against a confirmed, non-empty /subjects family, never declined"
	exit 1
fi
if ! jq -en --argjson ids "$p3c_subject_ids" --argjson id "$p3c_quiz_subject_id" 'any($ids[]; . == $id)' >/dev/null 2>&1; then
	echo "FAIL  P3c: GET /quiz subjectId=${p3c_quiz_subject_id} is not one of /subjects' real ids ${p3c_subject_ids} -- the ref recipe never actually resolved against stored resource rows"
	exit 1
fi
echo "PASS  P3c: GET /quiz subjectId=${p3c_quiz_subject_id} is one of /subjects' real stored ids ${p3c_subject_ids} -- the ref recipe resolved against a genuine confirmed resource, end to end through the real binary"

# --------------------------------------------------------------------------
# P3e: a nested family is populated one row set PER LIVE PARENT ROW and
# served per scope (DESIGN §11:505-508, mocker-p3e-nested). This is the one
# property no Go test in internal/mockplane holds against a REAL BINARY in a
# REAL CONTAINER, for the identical reason the P3a and P3c blocks above give
# for theirs: every internal/mockplane test wires a fake EntityStore/
# ResourceSource directly, so confirming that cmd/mocker/main.go's setters
# feed a *resources.Repo whose scope_key predicate (D6.2's ten sites, D17.4)
# actually partitions rows by scope -- rather than by mere confirm-time
# happenstance -- requires walking against the compose stack main.go itself
# assembles, not a fake.
#
# ITS OWN SPEC AND ITS OWN WORKSPACE, the identical reasoning the P3a and P3c
# blocks above give for theirs: a PARENT family (/orgs, confirmable and
# populated at the default listSize=5, so GET /orgs after confirm already
# hands this block two-or-more distinct org ids with no POST of its own
# needed) and a CHILD family nested exactly one level under it (/orgs/{}/
# users) -- the one shape D3.4 derives at all.
#
# THE SPEC ITSELF: /orgs -- GET /orgs (bare array of Org{id,name}) and its
# detail route GET /orgs/{orgId}; /orgs/{orgId}/users -- GET (bare array of
# User{id,name}), POST (write_form "bare", the same $ref both GET responses
# use, D4/R12's two-hop walk) and its own detail route GET
# /orgs/{orgId}/users/{id}, which is what makes router.ListFamily derive
# "/orgs/{}/users" as a family at all (ListFamily's own doc: the collection
# AND a detail route on the same prefix, both required).
# --------------------------------------------------------------------------
echo "== P3e: a nested family is scoped per live parent row -- import, confirm /orgs, confirm /orgs/{}/users, two scopes disagree, a write lands in its own scope only, an unanchored scope 404s =="

p3e_doc=$(jq -n '{
	openapi: "3.0.3",
	info: {title: "P3e Orgs", version: "1.0.0"},
	paths: {
		"/orgs": {
			get: {
				operationId: "listOrgs",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {type: "array", items: {"$ref": "#/components/schemas/Org"}}
							}
						}
					}
				}
			}
		},
		"/orgs/{orgId}": {
			get: {
				operationId: "getOrg",
				parameters: [{name: "orgId", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/Org"}
							}
						}
					}
				}
			}
		},
		"/orgs/{orgId}/users": {
			get: {
				operationId: "listOrgUsers",
				parameters: [{name: "orgId", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {type: "array", items: {"$ref": "#/components/schemas/User"}}
							}
						}
					}
				}
			},
			post: {
				operationId: "createOrgUser",
				parameters: [{name: "orgId", in: "path", required: true, schema: {type: "integer"}}],
				requestBody: {
					content: {
						"application/json": {
							schema: {"$ref": "#/components/schemas/User"}
						}
					}
				},
				responses: {
					"201": {
						description: "created",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/User"}
							}
						}
					}
				}
			}
		},
		"/orgs/{orgId}/users/{id}": {
			get: {
				operationId: "getOrgUser",
				parameters: [
					{name: "orgId", in: "path", required: true, schema: {type: "integer"}},
					{name: "id", in: "path", required: true, schema: {type: "integer"}}
				],
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/User"}
							}
						}
					}
				}
			}
		}
	},
	components: {
		schemas: {
			Org: {
				type: "object",
				properties: {
					id: {type: "integer"},
					name: {type: "string"}
				},
				required: ["id"]
			},
			User: {
				type: "object",
				properties: {
					id: {type: "integer"},
					name: {type: "string"}
				},
				required: ["id"]
			}
		}
	}
}')

p3e_import_body=$(jq -n --arg name "P3e Orgs" --arg source "upload" --arg document "$p3e_doc" \
	'{name: $name, source: $source, document: $document}')
p3e_import_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p3e_import_body" -H "X-CSRF-Token: ${csrf}")
if [[ "$p3e_import_status" != "201" ]]; then
	echo "FAIL  P3e: import the /orgs + /orgs/{}/users spec: want status 201, got ${p3e_import_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3e_spec_id=$(jq -r '.id' "$BODY_FILE")
echo "PASS  P3e: imported the /orgs + /orgs/{}/users spec (id ${p3e_spec_id})"

p3e_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces \
	"$(jq -cn --arg n "p3e-nested" --argjson s "$p3e_spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p3e_ws_id=$(jq -r '.id' "$BODY_FILE")
p3e_slug=$(jq -r '.slug' "$BODY_FILE")
if [[ "$p3e_ws_status" != "201" || -z "$p3e_ws_id" || "$p3e_ws_id" == "null" ]]; then
	echo "FAIL  P3e: create the dedicated workspace with specId already attached: want status 201, got ${p3e_ws_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3e_host="${p3e_slug}.${WORKSPACE_HOST_BASE}"
echo "PASS  P3e: dedicated workspace ${p3e_slug} (id ${p3e_ws_id}) created with the /orgs + /orgs/{}/users spec attached"

p3e_parent_confirm_body=$(jq -n '{routeFamily: "/orgs", state: "confirmed"}')
p3e_parent_confirm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3e_ws_id}/resource-decisions" "$p3e_parent_confirm_body" -H "X-CSRF-Token: ${csrf}")
p3e_parent_decision=$(jq -r '.family.decision' "$BODY_FILE")
if [[ "$p3e_parent_confirm_status" != "200" || "$p3e_parent_decision" != "confirmed" ]]; then
	echo "FAIL  P3e: confirm the /orgs parent family: want status 200 decision=confirmed, got status ${p3e_parent_confirm_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3e: /orgs (parent) confirmed"

# D5.1: a nested family cannot confirm before its parent does -- confirming
# the child family only NOW, after the parent above, is what D5.2 calls
# "the invariant this buys": a confirmed nested family always has a
# confirmed parent, held by this refusal on one end and D7.1's on the other.
p3e_child_confirm_body=$(jq -n '{routeFamily: "/orgs/{}/users", state: "confirmed"}')
p3e_child_confirm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3e_ws_id}/resource-decisions" "$p3e_child_confirm_body" -H "X-CSRF-Token: ${csrf}")
p3e_child_decision=$(jq -r '.family.decision' "$BODY_FILE")
if [[ "$p3e_child_confirm_status" != "200" || "$p3e_child_decision" != "confirmed" ]]; then
	echo "FAIL  P3e: confirm the /orgs/{}/users child family (parent already confirmed): want status 200 decision=confirmed, got status ${p3e_child_confirm_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3e: /orgs/{}/users (child) confirmed"

echo "== P3e: GET /orgs -- the default listSize=5 population, two distinct scopes to compare =="
p3e_orgs_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3e_host}" "${BASE_URL}/orgs")
if [[ "$p3e_orgs_status" != "200" ]] || ! jq -e 'type == "array" and length >= 2' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3e: GET /orgs after confirm: want status 200 with at least two seeded rows, got status ${p3e_orgs_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3e_org_a=$(jq -r '.[0].id' "$BODY_FILE")
p3e_org_b=$(jq -r '.[1].id' "$BODY_FILE")
if [[ -z "$p3e_org_a" || "$p3e_org_a" == "null" || -z "$p3e_org_b" || "$p3e_org_b" == "null" || "$p3e_org_a" == "$p3e_org_b" ]]; then
	echo "FAIL  P3e: GET /orgs: could not take two distinct seeded org ids (a=${p3e_org_a} b=${p3e_org_b})"
	exit 1
fi
echo "PASS  P3e: GET /orgs seeded org a=${p3e_org_a}, org b=${p3e_org_b}"

echo "== P3e: GET /orgs/{a}/users and GET /orgs/{b}/users -- two different stored row sets =="
p3e_users_a_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_org_a}/users")
if [[ "$p3e_users_a_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3e: GET /orgs/${p3e_org_a}/users: want status 200 with a non-empty seeded scope, got status ${p3e_users_a_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3e_ids_a=$(jq -c '[.[].id]' "$BODY_FILE")

p3e_users_b_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_org_b}/users")
if [[ "$p3e_users_b_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3e: GET /orgs/${p3e_org_b}/users: want status 200 with a non-empty seeded scope, got status ${p3e_users_b_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3e_ids_b=$(jq -c '[.[].id]' "$BODY_FILE")

if jq -en --argjson a "$p3e_ids_a" --argjson b "$p3e_ids_b" 'any($a[]; . as $x | $b | any(. == $x))' >/dev/null 2>&1; then
	echo "FAIL  P3e: GET /orgs/${p3e_org_a}/users ${p3e_ids_a} and GET /orgs/${p3e_org_b}/users ${p3e_ids_b} share an id -- two scopes of the same nested family answered the SAME row set"
	exit 1
fi
echo "PASS  P3e: GET /orgs/${p3e_org_a}/users ${p3e_ids_a} and GET /orgs/${p3e_org_b}/users ${p3e_ids_b} are disjoint -- each scope is its own stored row set"

echo "== P3e: POST /orgs/{a}/users -- visible in scope a, absent from scope b =="
p3e_post_status=$(http_json POST "$p3e_host" "/orgs/${p3e_org_a}/users" '{"name":"smoke-scoped-write"}')
p3e_new_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$p3e_post_status" != "201" || -z "$p3e_new_id" || "$p3e_new_id" == "null" ]]; then
	echo "FAIL  P3e: POST /orgs/${p3e_org_a}/users: want status 201 with a server-assigned id, got status ${p3e_post_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3e: POST /orgs/${p3e_org_a}/users created id ${p3e_new_id} in scope a"

p3e_reread_a_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_org_a}/users")
if [[ "$p3e_reread_a_status" != "200" ]] || ! jq -e --argjson id "$p3e_new_id" 'any(.[]; .id == $id)' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3e: GET /orgs/${p3e_org_a}/users after the POST: want status 200 with id ${p3e_new_id} present, got status ${p3e_reread_a_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3e: GET /orgs/${p3e_org_a}/users after the POST contains ${p3e_new_id} -- the write landed in its own scope"

p3e_reread_b_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_org_b}/users")
if [[ "$p3e_reread_b_status" != "200" ]] || jq -e --argjson id "$p3e_new_id" 'any(.[]; .id == $id)' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3e: GET /orgs/${p3e_org_b}/users after the POST into scope a: want status 200 WITHOUT id ${p3e_new_id}, got status ${p3e_reread_b_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3e: GET /orgs/${p3e_org_b}/users after the POST into scope a does NOT contain ${p3e_new_id} -- the write did not leak into a sibling scope"

echo "== P3e: GET /orgs/{no-such-org}/users -- a scope no live parent row anchors is 404 entity_not_found =="
p3e_orphan_org_id=999999999
p3e_orphan_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_orphan_org_id}/users")
p3e_orphan_code=$(jq -r '.error.code // empty' "$BODY_FILE")
if [[ "$p3e_orphan_status" != "404" || "$p3e_orphan_code" != "entity_not_found" ]]; then
	echo "FAIL  P3e: GET /orgs/${p3e_orphan_org_id}/users (no such org row): want status 404 code entity_not_found, got status ${p3e_orphan_status} code ${p3e_orphan_code}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3e: GET /orgs/${p3e_orphan_org_id}/users answers 404 entity_not_found -- the anchor check (D6.3), not a generated body, not scope a's or b's rows"

echo "== P3e: POST /orgs/{no-such-org}/users -- a scope no live parent row anchors is 404, and nothing is written =="
p3e_ids_a_before_orphan=$(curl -s -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_org_a}/users" | jq -cS '[.[].id] | sort')
p3e_ids_b_before_orphan=$(curl -s -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_org_b}/users" | jq -cS '[.[].id] | sort')

p3e_orphan_post_status=$(http_json POST "$p3e_host" "/orgs/${p3e_orphan_org_id}/users" '{"name":"should-not-write"}')
p3e_orphan_post_code=$(jq -r '.error.code // empty' "$BODY_FILE")
if [[ "$p3e_orphan_post_status" != "404" || "$p3e_orphan_post_code" != "entity_not_found" ]]; then
	echo "FAIL  P3e: POST /orgs/${p3e_orphan_org_id}/users (no such org row): want status 404 code entity_not_found, got status ${p3e_orphan_post_status} code ${p3e_orphan_post_code}: $(cat "$BODY_FILE")"
	exit 1
fi

p3e_ids_a_after_orphan=$(curl -s -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_org_a}/users" | jq -cS '[.[].id] | sort')
p3e_ids_b_after_orphan=$(curl -s -H "Host: ${p3e_host}" "${BASE_URL}/orgs/${p3e_org_b}/users" | jq -cS '[.[].id] | sort')
if [[ "$p3e_ids_a_after_orphan" != "$p3e_ids_a_before_orphan" || "$p3e_ids_b_after_orphan" != "$p3e_ids_b_before_orphan" ]]; then
	echo "FAIL  P3e: POST /orgs/${p3e_orphan_org_id}/users: a real scope's id set changed -- want the anchor check to refuse BEFORE any write, but scope a ${p3e_ids_a_before_orphan} -> ${p3e_ids_a_after_orphan}, scope b ${p3e_ids_b_before_orphan} -> ${p3e_ids_b_after_orphan}"
	exit 1
fi
echo "PASS  P3e: POST /orgs/${p3e_orphan_org_id}/users answers 404 entity_not_found and writes nothing into scope a or scope b -- the anchor check refuses BEFORE any write, on POST exactly as on GET"

# --------------------------------------------------------------------------
# P3g (mocker-p3g-deep-nesting): nesting raises from the ONE level P3e shipped
# to a ceiling of THREE (D3.1, internal/specs.maxNestingDepth), and the P3e
# block above proves only depth 1 -- root and one child, one hop of anchor
# check, one hop of confirm. This block is the reason D16 names
# scripts/smoke.sh at all: it is the only place the deep-nesting wiring
# through cmd/mocker/main.go is proven against a REAL BINARY in a REAL
# CONTAINER, for the identical reason the P3a/P3c/P3e blocks give for
# theirs -- every internal/mockplane and internal/resources test wires a
# fake EntityStore/ResourceSource, so confirming that the deep-nesting anchor
# walk (D6.2), the chain-scope arithmetic (D5.3/D17.2) and the subtree reseed
# group (D8.2) all actually work through the *resources.Repo main.go's own
# setters feed -- rather than merely against the arithmetic in isolation --
# needs a walk against the compose stack itself.
#
# ITS OWN SPEC AND ITS OWN WORKSPACE, exactly as the P3e block reasons for
# its own: a THREE-LEVEL CHAIN --
#   /orgs                                    depth 0, the root
#   /orgs/{orgId}/teams                      depth 1, the middle
#   /orgs/{orgId}/teams/{teamId}/users       depth 2, the leaf
# -- the depth-2 positive control D4.3 also puts in internal/testspec's
# DeepNestingDoc, but this block does not import that document (it is a Go
# fixture, not a spec on the wire): it builds its own inline exactly as P3e's
# block builds its own, so this proof does not depend on internal/testspec
# staying unchanged.
#
# THE ROOT DECLARES DELETE /orgs/{orgId} -- P3e's own spec never needed a
# DELETE route because nothing in that block deletes an ancestor; this block
# does (the anchor-walk step below), and D6.4/R14's table only takes DELETE
# X/{} over when the spec declares it.
# --------------------------------------------------------------------------
echo "== P3g: a three-level chain -- import, confirm root/middle/leaf in order, four disjoint leaf scopes, a scoped write, a root delete 404s a whole leaf scope on GET and POST, siblings untouched =="

p3g_doc=$(jq -n '{
	openapi: "3.0.3",
	info: {title: "P3g Orgs", version: "1.0.0"},
	paths: {
		"/orgs": {
			get: {
				operationId: "listOrgs",
				responses: {
					"200": {
						description: "ok",
						content: {"application/json": {schema: {type: "array", items: {"$ref": "#/components/schemas/Org"}}}}
					}
				}
			}
		},
		"/orgs/{orgId}": {
			get: {
				operationId: "getOrg",
				parameters: [{name: "orgId", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"200": {description: "ok", content: {"application/json": {schema: {"$ref": "#/components/schemas/Org"}}}}
				}
			},
			delete: {
				operationId: "deleteOrg",
				parameters: [{name: "orgId", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"200": {description: "deleted", content: {"application/json": {schema: {"$ref": "#/components/schemas/Org"}}}}
				}
			}
		},
		"/orgs/{orgId}/teams": {
			get: {
				operationId: "listOrgTeams",
				parameters: [{name: "orgId", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"200": {
						description: "ok",
						content: {"application/json": {schema: {type: "array", items: {"$ref": "#/components/schemas/Team"}}}}
					}
				}
			},
			post: {
				operationId: "createOrgTeam",
				parameters: [{name: "orgId", in: "path", required: true, schema: {type: "integer"}}],
				requestBody: {content: {"application/json": {schema: {"$ref": "#/components/schemas/Team"}}}},
				responses: {
					"201": {description: "created", content: {"application/json": {schema: {"$ref": "#/components/schemas/Team"}}}}
				}
			}
		},
		"/orgs/{orgId}/teams/{teamId}": {
			get: {
				operationId: "getOrgTeam",
				parameters: [
					{name: "orgId", in: "path", required: true, schema: {type: "integer"}},
					{name: "teamId", in: "path", required: true, schema: {type: "integer"}}
				],
				responses: {
					"200": {description: "ok", content: {"application/json": {schema: {"$ref": "#/components/schemas/Team"}}}}
				}
			}
		},
		"/orgs/{orgId}/teams/{teamId}/users": {
			get: {
				operationId: "listOrgTeamUsers",
				parameters: [
					{name: "orgId", in: "path", required: true, schema: {type: "integer"}},
					{name: "teamId", in: "path", required: true, schema: {type: "integer"}}
				],
				responses: {
					"200": {
						description: "ok",
						content: {"application/json": {schema: {type: "array", items: {"$ref": "#/components/schemas/User"}}}}
					}
				}
			},
			post: {
				operationId: "createOrgTeamUser",
				parameters: [
					{name: "orgId", in: "path", required: true, schema: {type: "integer"}},
					{name: "teamId", in: "path", required: true, schema: {type: "integer"}}
				],
				requestBody: {content: {"application/json": {schema: {"$ref": "#/components/schemas/User"}}}},
				responses: {
					"201": {description: "created", content: {"application/json": {schema: {"$ref": "#/components/schemas/User"}}}}
				}
			}
		},
		"/orgs/{orgId}/teams/{teamId}/users/{id}": {
			get: {
				operationId: "getOrgTeamUser",
				parameters: [
					{name: "orgId", in: "path", required: true, schema: {type: "integer"}},
					{name: "teamId", in: "path", required: true, schema: {type: "integer"}},
					{name: "id", in: "path", required: true, schema: {type: "integer"}}
				],
				responses: {
					"200": {description: "ok", content: {"application/json": {schema: {"$ref": "#/components/schemas/User"}}}}
				}
			}
		}
	},
	components: {
		schemas: {
			Org: {type: "object", properties: {id: {type: "integer"}, name: {type: "string"}}, required: ["id"]},
			Team: {type: "object", properties: {id: {type: "integer"}, name: {type: "string"}}, required: ["id"]},
			User: {type: "object", properties: {id: {type: "integer"}, name: {type: "string"}}, required: ["id"]}
		}
	}
}')

p3g_import_body=$(jq -n --arg name "P3g Orgs" --arg source "upload" --arg document "$p3g_doc" \
	'{name: $name, source: $source, document: $document}')
p3g_import_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p3g_import_body" -H "X-CSRF-Token: ${csrf}")
if [[ "$p3g_import_status" != "201" ]]; then
	echo "FAIL  P3g: import the three-level /orgs/{}/teams/{}/users spec: want status 201, got ${p3g_import_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_spec_id=$(jq -r '.id' "$BODY_FILE")
echo "PASS  P3g: imported the three-level spec (id ${p3g_spec_id})"

p3g_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces \
	"$(jq -cn --arg n "p3g-deep-nested" --argjson s "$p3g_spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p3g_ws_id=$(jq -r '.id' "$BODY_FILE")
p3g_slug=$(jq -r '.slug' "$BODY_FILE")
if [[ "$p3g_ws_status" != "201" || -z "$p3g_ws_id" || "$p3g_ws_id" == "null" ]]; then
	echo "FAIL  P3g: create the dedicated workspace with specId already attached: want status 201, got ${p3g_ws_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_host="${p3g_slug}.${WORKSPACE_HOST_BASE}"
echo "PASS  P3g: dedicated workspace ${p3g_slug} (id ${p3g_ws_id}) created with the three-level spec attached"

# D5.1/D5.2: the chain confirms top down, and each step's own single-hop
# refusal is what D5.2's induction turns into "a confirmed family at any
# depth has a confirmed family at every level of its ancestor chain" -- so
# confirming out of order (leaf before middle, or middle before root) is
# refused with 409 parent_not_confirmed and is not exercised here; the P3e
# block above already pins that refusal at depth 1 and D5.1 states this
# slice adds no chain check on top of it.
p3g_root_confirm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3g_ws_id}/resource-decisions" \
	"$(jq -n '{routeFamily: "/orgs", state: "confirmed"}')" -H "X-CSRF-Token: ${csrf}")
p3g_root_decision=$(jq -r '.family.decision' "$BODY_FILE")
if [[ "$p3g_root_confirm_status" != "200" || "$p3g_root_decision" != "confirmed" ]]; then
	echo "FAIL  P3g: confirm the /orgs root family: want status 200 decision=confirmed, got status ${p3g_root_confirm_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: /orgs (root, depth 0) confirmed"

p3g_mid_confirm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3g_ws_id}/resource-decisions" \
	"$(jq -n '{routeFamily: "/orgs/{}/teams", state: "confirmed"}')" -H "X-CSRF-Token: ${csrf}")
p3g_mid_decision=$(jq -r '.family.decision' "$BODY_FILE")
if [[ "$p3g_mid_confirm_status" != "200" || "$p3g_mid_decision" != "confirmed" ]]; then
	echo "FAIL  P3g: confirm the /orgs/{}/teams middle family (root already confirmed): want status 200 decision=confirmed, got status ${p3g_mid_confirm_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: /orgs/{}/teams (middle, depth 1) confirmed"

p3g_leaf_confirm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3g_ws_id}/resource-decisions" \
	"$(jq -n '{routeFamily: "/orgs/{}/teams/{}/users", state: "confirmed"}')" -H "X-CSRF-Token: ${csrf}")
p3g_leaf_decision=$(jq -r '.family.decision' "$BODY_FILE")
if [[ "$p3g_leaf_confirm_status" != "200" || "$p3g_leaf_decision" != "confirmed" ]]; then
	echo "FAIL  P3g: confirm the /orgs/{}/teams/{}/users leaf family (middle already confirmed): want status 200 decision=confirmed, got status ${p3g_leaf_confirm_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: /orgs/{}/teams/{}/users (leaf, depth 2) confirmed -- the whole chain is now confirmed, D5.2's invariant holds by construction"

# D5.3: population is per scope TUPLE, walked top down from the root -- at
# the default listSize=5, /orgs alone already hands this block two-or-more
# distinct root rows with no POST of its own needed, exactly as the P3e
# block reasons for depth 1.
echo "== P3g: GET /orgs -- two distinct root rows =="
p3g_orgs_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs")
if [[ "$p3g_orgs_status" != "200" ]] || ! jq -e 'type == "array" and length >= 2' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs after confirm: want status 200 with at least two seeded rows, got status ${p3g_orgs_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_org_a=$(jq -r '.[0].id' "$BODY_FILE")
p3g_org_b=$(jq -r '.[1].id' "$BODY_FILE")
if [[ -z "$p3g_org_a" || "$p3g_org_a" == "null" || -z "$p3g_org_b" || "$p3g_org_b" == "null" || "$p3g_org_a" == "$p3g_org_b" ]]; then
	echo "FAIL  P3g: GET /orgs: could not take two distinct seeded org ids (a=${p3g_org_a} b=${p3g_org_b})"
	exit 1
fi
echo "PASS  P3g: GET /orgs seeded root a=${p3g_org_a}, root b=${p3g_org_b}"

# Two distinct MIDDLE rows under EACH root -- this is what gives the leaf
# level four DISTINCT scopes below (a,m1) (a,m2) (b,m3) (b,m4), rather than
# just two: with one middle row taken per root, D5.3's own tuple walk would
# only ever be exercised at ONE tuple per root and a bug that confused two
# SIBLING middle scopes under the SAME root (rather than two scopes under
# different roots) would pass unnoticed.
echo "== P3g: GET /orgs/{a}/teams and GET /orgs/{b}/teams -- two distinct middle rows under each root =="
p3g_teams_a_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams")
if [[ "$p3g_teams_a_status" != "200" ]] || ! jq -e 'type == "array" and length >= 2' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_a}/teams: want status 200 with at least two seeded rows, got status ${p3g_teams_a_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_team_m1=$(jq -r '.[0].id' "$BODY_FILE")
p3g_team_m2=$(jq -r '.[1].id' "$BODY_FILE")

p3g_teams_b_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_b}/teams")
if [[ "$p3g_teams_b_status" != "200" ]] || ! jq -e 'type == "array" and length >= 2' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_b}/teams: want status 200 with at least two seeded rows, got status ${p3g_teams_b_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_team_m3=$(jq -r '.[0].id' "$BODY_FILE")
p3g_team_m4=$(jq -r '.[1].id' "$BODY_FILE")

if [[ -z "$p3g_team_m1" || "$p3g_team_m1" == "null" || -z "$p3g_team_m2" || "$p3g_team_m2" == "null" || "$p3g_team_m1" == "$p3g_team_m2" ]]; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_a}/teams: could not take two distinct middle ids (m1=${p3g_team_m1} m2=${p3g_team_m2})"
	exit 1
fi
if [[ -z "$p3g_team_m3" || "$p3g_team_m3" == "null" || -z "$p3g_team_m4" || "$p3g_team_m4" == "null" || "$p3g_team_m3" == "$p3g_team_m4" ]]; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_b}/teams: could not take two distinct middle ids (m3=${p3g_team_m3} m4=${p3g_team_m4})"
	exit 1
fi
echo "PASS  P3g: under root a, middle m1=${p3g_team_m1} m2=${p3g_team_m2}; under root b, middle m3=${p3g_team_m3} m4=${p3g_team_m4}"

# D6.2: the anchor walk at depth 2 checks BOTH ancestors -- the middle row
# AND the root row above it -- so a leaf scope is reachable only while its
# whole chain is live. This step does not exercise that refusal (both
# ancestors are live); it only reads the four leaf scopes the tuple walk
# populated, one per (root, middle) pair.
echo "== P3g: four leaf scopes (a,m1) (a,m2) (b,m3) (b,m4) -- pairwise disjoint row sets =="
p3g_leaf_am1_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams/${p3g_team_m1}/users")
if [[ "$p3g_leaf_am1_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_a}/teams/${p3g_team_m1}/users: want status 200 with a non-empty seeded scope, got status ${p3g_leaf_am1_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_ids_am1=$(jq -c '[.[].id]' "$BODY_FILE")

p3g_leaf_am2_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams/${p3g_team_m2}/users")
if [[ "$p3g_leaf_am2_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_a}/teams/${p3g_team_m2}/users: want status 200 with a non-empty seeded scope, got status ${p3g_leaf_am2_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_ids_am2=$(jq -c '[.[].id]' "$BODY_FILE")

p3g_leaf_bm3_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_b}/teams/${p3g_team_m3}/users")
if [[ "$p3g_leaf_bm3_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_b}/teams/${p3g_team_m3}/users: want status 200 with a non-empty seeded scope, got status ${p3g_leaf_bm3_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_ids_bm3=$(jq -c '[.[].id]' "$BODY_FILE")

p3g_leaf_bm4_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_b}/teams/${p3g_team_m4}/users")
if [[ "$p3g_leaf_bm4_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_b}/teams/${p3g_team_m4}/users: want status 200 with a non-empty seeded scope, got status ${p3g_leaf_bm4_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3g_ids_bm4=$(jq -c '[.[].id]' "$BODY_FILE")

if ! jq -en --argjson am1 "$p3g_ids_am1" --argjson am2 "$p3g_ids_am2" --argjson bm3 "$p3g_ids_bm3" --argjson bm4 "$p3g_ids_bm4" '
		([$am1, $am2, $bm3, $bm4] | flatten | unique | length) == ([$am1, $am2, $bm3, $bm4] | flatten | length)
	' >/dev/null 2>&1; then
	echo "FAIL  P3g: the four leaf scopes share an id -- (a,m1)=${p3g_ids_am1} (a,m2)=${p3g_ids_am2} (b,m3)=${p3g_ids_bm3} (b,m4)=${p3g_ids_bm4} -- want all four pairwise disjoint, each scope its own stored row set"
	exit 1
fi
echo "PASS  P3g: (a,m1)=${p3g_ids_am1} (a,m2)=${p3g_ids_am2} (b,m3)=${p3g_ids_bm3} (b,m4)=${p3g_ids_bm4} are pairwise disjoint -- the depth-2 tuple walk (D5.3) gave each (root, middle) pair its own rows"

echo "== P3g: POST /orgs/{a}/teams/{m1}/users -- visible in scope (a,m1), absent from sibling scope (a,m2) =="
p3g_post_status=$(http_json POST "$p3g_host" "/orgs/${p3g_org_a}/teams/${p3g_team_m1}/users" '{"name":"smoke-deep-scoped-write"}')
p3g_new_id=$(jq -r '.id' "$BODY_FILE")
if [[ "$p3g_post_status" != "201" || -z "$p3g_new_id" || "$p3g_new_id" == "null" ]]; then
	echo "FAIL  P3g: POST /orgs/${p3g_org_a}/teams/${p3g_team_m1}/users: want status 201 with a server-assigned id, got status ${p3g_post_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: POST /orgs/${p3g_org_a}/teams/${p3g_team_m1}/users created id ${p3g_new_id} in scope (a,m1)"

p3g_reread_am1_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams/${p3g_team_m1}/users")
if [[ "$p3g_reread_am1_status" != "200" ]] || ! jq -e --argjson id "$p3g_new_id" 'any(.[]; .id == $id)' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_a}/teams/${p3g_team_m1}/users after the POST: want status 200 with id ${p3g_new_id} present, got status ${p3g_reread_am1_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: GET (a,m1) after the POST contains ${p3g_new_id} -- the write landed in its own leaf scope"

p3g_reread_am2_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams/${p3g_team_m2}/users")
if [[ "$p3g_reread_am2_status" != "200" ]] || jq -e --argjson id "$p3g_new_id" 'any(.[]; .id == $id)' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_a}/teams/${p3g_team_m2}/users after the POST into (a,m1): want status 200 WITHOUT id ${p3g_new_id}, got status ${p3g_reread_am2_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: GET (a,m2) after the POST into (a,m1) does NOT contain ${p3g_new_id} -- the write did not leak into a sibling leaf scope under the SAME root"

# D6.2: deleting a ROOT row -- not the immediate parent, the family two hops
# above the leaf -- is the case P3e's own single-hop anchor check could not
# have exercised at all (it has no grandparent), and it is the wrong
# implementation D6.2 names by name: "checking only the immediate parent"
# would keep (b,m3) reachable after /orgs/{b} is gone, because team m3 is
# still a live row one hop up -- the resurrection
# DESIGN.md:508-511#parent_entity_id warns about, arriving through the depth
# this slice adds (D9.1). The walk must refuse at the FIRST miss reading
# from the ROOT down, so deleting the root -- not the middle -- is what
# actually exercises the "top down, not bottom up" half of D6.2's rule.
echo "== P3g: DELETE /orgs/{b} (a root row) -- a leaf scope under it is 404 entity_not_found on GET and POST, siblings under root a untouched =="

p3g_ids_am1_before_delete=$(curl -s -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams/${p3g_team_m1}/users" | jq -cS '[.[].id] | sort')
p3g_ids_am2_before_delete=$(curl -s -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams/${p3g_team_m2}/users" | jq -cS '[.[].id] | sort')

p3g_delete_root_status=$(http_json DELETE "$p3g_host" "/orgs/${p3g_org_b}" '')
if [[ "$p3g_delete_root_status" != "200" ]]; then
	echo "FAIL  P3g: DELETE /orgs/${p3g_org_b}: want status 200, got ${p3g_delete_root_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: DELETE /orgs/${p3g_org_b} removed the root row"

p3g_orphan_get_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_b}/teams/${p3g_team_m3}/users")
p3g_orphan_get_code=$(jq -r '.error.code // empty' "$BODY_FILE")
if [[ "$p3g_orphan_get_status" != "404" || "$p3g_orphan_get_code" != "entity_not_found" ]]; then
	echo "FAIL  P3g: GET /orgs/${p3g_org_b}/teams/${p3g_team_m3}/users after DELETE /orgs/${p3g_org_b}: want status 404 code entity_not_found, got status ${p3g_orphan_get_status} code ${p3g_orphan_get_code}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: GET (b,m3) after the root delete answers 404 entity_not_found -- the anchor walk caught the ROOT miss, two hops up from the leaf, not just the immediate parent"

p3g_orphan_post_status=$(http_json POST "$p3g_host" "/orgs/${p3g_org_b}/teams/${p3g_team_m3}/users" '{"name":"should-not-write"}')
p3g_orphan_post_code=$(jq -r '.error.code // empty' "$BODY_FILE")
if [[ "$p3g_orphan_post_status" != "404" || "$p3g_orphan_post_code" != "entity_not_found" ]]; then
	echo "FAIL  P3g: POST /orgs/${p3g_org_b}/teams/${p3g_team_m3}/users after DELETE /orgs/${p3g_org_b}: want status 404 code entity_not_found, got status ${p3g_orphan_post_status} code ${p3g_orphan_post_code}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3g: POST (b,m3) after the root delete answers 404 entity_not_found too -- the anchor walk refuses on all four verbs, before any write"

p3g_ids_am1_after_delete=$(curl -s -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams/${p3g_team_m1}/users" | jq -cS '[.[].id] | sort')
p3g_ids_am2_after_delete=$(curl -s -H "Host: ${p3g_host}" "${BASE_URL}/orgs/${p3g_org_a}/teams/${p3g_team_m2}/users" | jq -cS '[.[].id] | sort')
if [[ "$p3g_ids_am1_after_delete" != "$p3g_ids_am1_before_delete" || "$p3g_ids_am2_after_delete" != "$p3g_ids_am2_before_delete" ]]; then
	echo "FAIL  P3g: deleting root b changed a scope under root a -- want (a,m1) and (a,m2) untouched, got (a,m1) ${p3g_ids_am1_before_delete} -> ${p3g_ids_am1_after_delete}, (a,m2) ${p3g_ids_am2_before_delete} -> ${p3g_ids_am2_after_delete}"
	exit 1
fi
echo "PASS  P3g: (a,m1) and (a,m2) -- scopes under the OTHER root -- are byte-identical before and after deleting root b: the anchor check is per-request (D6.3: no membership List, no cross-request memoisation), never a cascade (D9: parent_entity_id stays NULL, deleting a root orphans its own descendants OBSERVABLY rather than reaching into a sibling subtree)"

# --------------------------------------------------------------------------
# P3h (mocker-p3h-basepath): settings.basePath may now carry a {param}
# (e.g. "/orgs/{orgId}"), and a workspace DECLARES which values that
# parameter may take in settings.basePathValues -- every entity row a
# confirmed family populates carries the base scope it belongs to
# (entities.base_scope_key, 0003_base_scope.sql), a confirm populates one
# row set per declared base value, and a request resolves its base scope
# POSITIONALLY off its own path segments before touching storage. This is
# the one property no Go test in internal/mockplane or internal/resources
# holds against a REAL BINARY in a REAL CONTAINER, for the identical reason
# the P3a/P3c/P3e/P3g blocks above give for theirs: every one of those
# packages' tests wires a fake EntityStore/ResourceSource, so confirming
# that cmd/mocker/main.go's own setters feed a *resources.Repo whose
# base_scope_key predicate actually partitions rows by TENANT -- rather than
# by mere confirm-time happenstance -- needs a walk against the compose
# stack main.go itself assembles, not a fake (P16).
#
# ITS OWN SPEC AND ITS OWN WORKSPACE, exactly as the P3a/P3e/P3g blocks
# reason for theirs: a top-level family (/quizzes -- GET, POST "bare" write
# form, and its own detail route GET+DELETE /quizzes/{id}) plus one
# NON-resource route (/health) that exists for exactly one assertion below:
# an undeclared base value must still serve a route the resource branch
# never touches (P7). basePath is set to "/orgs/{orgId}" and
# basePathValues to ["7","8"] through the admin API AFTER the workspace is
# created and BEFORE the family is confirmed, because confirm's own fence
# (fenceConfirmTx) reads basePath/basePathValues as part of what it fences
# against a race (D11's own P11) -- confirming before declaring the set
# would refuse with 409 base_scope_undeclared.
#
# p3h_checks counts every assertion this block makes, the p2c_checks/
# p2d_checks pattern this file's own comment at :53-64 recommends and which
# the P3e and P3g blocks above did NOT carry (P16's own text says so): this
# is the one place the P3h wiring through cmd/mocker/main.go is proven end
# to end at all, so a silently skipped block here would be indistinguishable
# from a passing one exactly where the whole slice is proved.
# --------------------------------------------------------------------------
echo "== P3h: a base-path parameter partitions one family's rows per declared tenant value -- import, declare the set, confirm, disjoint collections, an undeclared value refuses on all four verbs, a non-resource route under it still serves =="

p3h_checks=0

p3h_doc=$(jq -n '{
	openapi: "3.0.3",
	info: {title: "P3h Quizzes", version: "1.0.0"},
	paths: {
		"/quizzes": {
			get: {
				operationId: "listQuizzes",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {type: "array", items: {"$ref": "#/components/schemas/Quiz"}}
							}
						}
					}
				}
			},
			post: {
				operationId: "createQuiz",
				requestBody: {
					content: {
						"application/json": {
							schema: {"$ref": "#/components/schemas/Quiz"}
						}
					}
				},
				responses: {
					"201": {
						description: "created",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/Quiz"}
							}
						}
					}
				}
			}
		},
		"/quizzes/{id}": {
			get: {
				operationId: "getQuiz",
				parameters: [{name: "id", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {"$ref": "#/components/schemas/Quiz"}
							}
						}
					}
				}
			},
			delete: {
				operationId: "deleteQuiz",
				parameters: [{name: "id", in: "path", required: true, schema: {type: "integer"}}],
				responses: {
					"204": {description: "deleted"}
				}
			}
		},
		"/health": {
			get: {
				operationId: "getHealth",
				responses: {
					"200": {
						description: "ok",
						content: {
							"application/json": {
								schema: {type: "object", properties: {status: {type: "string"}}, required: ["status"]}
							}
						}
					}
				}
			}
		}
	},
	components: {
		schemas: {
			Quiz: {
				type: "object",
				properties: {
					id: {type: "integer"},
					name: {type: "string"}
				},
				required: ["id"]
			}
		}
	}
}')

p3h_import_body=$(jq -n --arg name "P3h Quizzes" --arg source "upload" --arg document "$p3h_doc" \
	'{name: $name, source: $source, document: $document}')
p3h_import_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p3h_import_body" -H "X-CSRF-Token: ${csrf}")
p3h_spec_id=$(jq -r '.id' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_import_status" != "201" || -z "$p3h_spec_id" || "$p3h_spec_id" == "null" ]]; then
	echo "FAIL  P3h: import the /quizzes + /health spec: want status 201, got ${p3h_import_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: imported the /quizzes + /health spec (id ${p3h_spec_id})"

p3h_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces \
	"$(jq -cn --arg n "p3h-basepath" --argjson s "$p3h_spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p3h_ws_id=$(jq -r '.id' "$BODY_FILE")
p3h_slug=$(jq -r '.slug' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_ws_status" != "201" || -z "$p3h_ws_id" || "$p3h_ws_id" == "null" ]]; then
	echo "FAIL  P3h: create the dedicated workspace with specId already attached: want status 201, got ${p3h_ws_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p3h_host="${p3h_slug}.${WORKSPACE_HOST_BASE}"
echo "PASS  P3h: dedicated workspace ${p3h_slug} (id ${p3h_ws_id}) created with the /quizzes + /health spec attached"

# p2c_set_settings (smoke.sh:1930's own reference) is set_settings'
# workspace-id-parameterised twin -- exactly what this block needs, since
# set_settings itself is hard-wired to the shared $ws_id. basePath and
# basePathValues are set in the SAME PATCH (D9: the two travel together --
# a P12b-shaped bug is a rollback that restores one without the other,
# which this single-call set-up cannot itself exercise, but sending both
# together here at least does not manufacture the drift by hand).
p3h_settings_status=$(p2c_set_settings "$p3h_ws_id" '.basePath = "/orgs/{orgId}" | .basePathValues = ["7", "8"]')
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_settings_status" != "200" ]]; then
	echo "FAIL  P3h: PATCH settings basePath=/orgs/{orgId} basePathValues=[7,8]: want status 200, got ${p3h_settings_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: basePath declared as /orgs/{orgId} with two declared tenant values, 7 and 8"

p3h_confirm_body=$(jq -n '{routeFamily: "/quizzes", state: "confirmed"}')
p3h_confirm_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p3h_ws_id}/resource-decisions" "$p3h_confirm_body" -H "X-CSRF-Token: ${csrf}")
p3h_decision=$(jq -r '.family.decision' "$BODY_FILE")
p3h_resource_id=$(jq -r '.family.resourceId' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_confirm_status" != "200" || "$p3h_decision" != "confirmed" || -z "$p3h_resource_id" || "$p3h_resource_id" == "null" ]]; then
	echo "FAIL  P3h: confirm the /quizzes family (basePath declared, two values): want status 200 decision=confirmed with a resourceId, got status ${p3h_confirm_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: /quizzes confirmed (resourceId ${p3h_resource_id}) -- population ran once per declared base value (D14 P4)"

echo "== P3h: GET /orgs/{7,8}/quizzes -- two disjoint collections, one per declared tenant (P1) =="

p3h_list7_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3h_host}" "${BASE_URL}/orgs/7/quizzes")
p3h_ids7=$(jq -cS '[.[].id] | sort' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_list7_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3h: GET /orgs/7/quizzes after confirm: want status 200 with a non-empty seeded array, got status ${p3h_list7_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: GET /orgs/7/quizzes seeded ${p3h_ids7}"

p3h_list8_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3h_host}" "${BASE_URL}/orgs/8/quizzes")
p3h_ids8=$(jq -cS '[.[].id] | sort' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_list8_status" != "200" ]] || ! jq -e 'type == "array" and length > 0' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3h: GET /orgs/8/quizzes after confirm: want status 200 with a non-empty seeded array, got status ${p3h_list8_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: GET /orgs/8/quizzes seeded ${p3h_ids8}"

p3h_checks=$((p3h_checks + 1))
if jq -en --argjson a "$p3h_ids7" --argjson b "$p3h_ids8" 'any($a[]; . as $x | $b | any(. == $x))' >/dev/null 2>&1; then
	echo "FAIL  P3h: /orgs/7/quizzes ${p3h_ids7} and /orgs/8/quizzes ${p3h_ids8} share an id -- want the two declared base scopes pairwise disjoint"
	exit 1
fi
echo "PASS  P3h: the two base scopes are disjoint -- entity_key is one family-wide counter (D6.2), so the property this observes is cross-base REACH, not a shared key"

echo "== P3h: POST /orgs/7/quizzes -- visible in 7, invisible in 8, 8 byte-identical (P2) =="

p3h_post_status=$(http_json POST "$p3h_host" /orgs/7/quizzes '{"id":999999,"name":"p3h-tenant-7"}')
p3h_new_id=$(jq -r '.id' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_post_status" != "201" || -z "$p3h_new_id" || "$p3h_new_id" == "null" || "$p3h_new_id" == "999999" ]]; then
	echo "FAIL  P3h: POST /orgs/7/quizzes: want status 201 with a server-assigned id distinct from the client-sent 999999, got status ${p3h_post_status} id '${p3h_new_id}': $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: POST /orgs/7/quizzes created id ${p3h_new_id} in base scope 7"

p3h_reread7_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3h_host}" "${BASE_URL}/orgs/7/quizzes")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_reread7_status" != "200" ]] || ! jq -e --argjson id "$p3h_new_id" 'any(.[]; .id == $id)' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3h: GET /orgs/7/quizzes after the POST: want status 200 with id ${p3h_new_id} present, got status ${p3h_reread7_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: GET /orgs/7/quizzes after the POST contains ${p3h_new_id} -- the write landed in its own base scope"
# Snapshot the EXACT id set here, not merely the fact that the new id is in it.
# The post-refusal check below compares against this, the same way base scope 8
# is compared against its own snapshot: a containment test would pass a bug that
# lets a refused write under the undeclared value 999 mint an EXTRA row into
# scope 7, since the id it looks for would still be there beside it.
p3h_ids7_after_post=$(jq -cS '[.[].id] | sort' "$BODY_FILE")

p3h_reread8_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3h_host}" "${BASE_URL}/orgs/8/quizzes")
p3h_ids8_after=$(jq -cS '[.[].id] | sort' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_reread8_status" != "200" || "$p3h_ids8_after" != "$p3h_ids8" ]]; then
	echo "FAIL  P3h: GET /orgs/8/quizzes after the POST into base scope 7: want status 200 and the SAME id set as before (${p3h_ids8}), got status ${p3h_reread8_status} ids ${p3h_ids8_after} -- a write into one tenant must be invisible, and the sibling tenant must stay byte-identical, in the other"
	exit 1
fi
echo "PASS  P3h: /orgs/8/quizzes is byte-identical before and after the POST into 7 -- a write into one declared value never reaches another (P2)"

echo "== P3h: GET, POST and DELETE under an UNDECLARED base value (999) all answer 404 entity_not_found, before any write (P6) =="

p3h_undeclared_get_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3h_host}" "${BASE_URL}/orgs/999/quizzes")
p3h_undeclared_get_code=$(jq -r '.error.code // empty' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_undeclared_get_status" != "404" || "$p3h_undeclared_get_code" != "entity_not_found" ]]; then
	echo "FAIL  P3h: GET /orgs/999/quizzes (undeclared base value): want status 404 code entity_not_found, got status ${p3h_undeclared_get_status} code ${p3h_undeclared_get_code}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: GET /orgs/999/quizzes answers 404 entity_not_found -- 999 is not in basePathValues"

p3h_undeclared_post_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X POST -H "Host: ${p3h_host}" -H 'Content-Type: application/json' \
	--data '{"name":"should-not-write"}' "${BASE_URL}/orgs/999/quizzes")
p3h_undeclared_post_code=$(jq -r '.error.code // empty' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_undeclared_post_status" != "404" || "$p3h_undeclared_post_code" != "entity_not_found" ]]; then
	echo "FAIL  P3h: POST /orgs/999/quizzes (undeclared base value): want status 404 code entity_not_found, got status ${p3h_undeclared_post_status} code ${p3h_undeclared_post_code}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: POST /orgs/999/quizzes answers 404 entity_not_found too -- the membership refusal is checked on writes, not only on reads"

p3h_undeclared_delete_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -X DELETE -H "Host: ${p3h_host}" "${BASE_URL}/orgs/999/quizzes/${p3h_new_id}")
p3h_undeclared_delete_code=$(jq -r '.error.code // empty' "$BODY_FILE")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_undeclared_delete_status" != "404" || "$p3h_undeclared_delete_code" != "entity_not_found" ]]; then
	echo "FAIL  P3h: DELETE /orgs/999/quizzes/${p3h_new_id} (undeclared base value, a real id that belongs to base scope 7): want status 404 code entity_not_found, got status ${p3h_undeclared_delete_status} code ${p3h_undeclared_delete_code}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: DELETE /orgs/999/quizzes/${p3h_new_id} answers 404 entity_not_found -- the membership check refuses before the request's own scope is even resolved, so it cannot reach a row that belongs to base scope 7"

p3h_ids7_after_refusals=$(curl -s -H "Host: ${p3h_host}" "${BASE_URL}/orgs/7/quizzes" | jq -cS '[.[].id] | sort')
p3h_ids8_after_refusals=$(curl -s -H "Host: ${p3h_host}" "${BASE_URL}/orgs/8/quizzes" | jq -cS '[.[].id] | sort')
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_ids8_after_refusals" != "$p3h_ids8" || "$p3h_ids7_after_refusals" != "$p3h_ids7_after_post" ]]; then
	echo "FAIL  P3h: after the three refused calls under base value 999: want base scope 7 byte-identical to its post-POST set (${p3h_ids7_after_post}) and base scope 8 unchanged (${p3h_ids8}), got 7=${p3h_ids7_after_refusals} 8=${p3h_ids8_after_refusals} -- a refused write must change nothing"
	exit 1
fi
echo "PASS  P3h: base scopes 7 and 8 are unchanged by the three refused calls -- the refusal writes nothing (P6's own second half)"

echo "== P3h: GET /orgs/999/health -- a NON-resource route under the same undeclared value answers normally (P7) =="

p3h_health_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p3h_host}" "${BASE_URL}/orgs/999/health")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_health_status" != "200" ]]; then
	echo "FAIL  P3h: GET /orgs/999/health: want status 200 (a non-resource route never reaches the membership refusal), got ${p3h_health_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: GET /orgs/999/health answers 200 -- the membership refusal is scoped to resource-served routes, not to the whole base value"

echo "== P3h: GET /api/workspaces/{id}/resources -- the per-base-scope breakdown matches what was actually served (D13) =="

http_json GET "$ADMIN_HOST" "/api/workspaces/${p3h_ws_id}/resources" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p3h_by7=$(jq -r --arg b "7" '.families[] | select(.routeFamily == "/quizzes") | .byBaseScope[] | select(.baseScope == $b) | .entityCount' "$BODY_FILE")
p3h_by8=$(jq -r --arg b "8" '.families[] | select(.routeFamily == "/quizzes") | .byBaseScope[] | select(.baseScope == $b) | .entityCount' "$BODY_FILE")
p3h_served7_count=$(jq 'length' <<<"$p3h_ids7_after_refusals")
p3h_served8_count=$(jq 'length' <<<"$p3h_ids8_after_refusals")
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_by7" != "$p3h_served7_count" ]]; then
	echo "FAIL  P3h: GET .../resources byBaseScope for base value 7: want entityCount ${p3h_served7_count} (matching GET /orgs/7/quizzes), got ${p3h_by7}: $(cat "$BODY_FILE")"
	exit 1
fi
p3h_checks=$((p3h_checks + 1))
if [[ "$p3h_by8" != "$p3h_served8_count" ]]; then
	echo "FAIL  P3h: GET .../resources byBaseScope for base value 8: want entityCount ${p3h_served8_count} (matching GET /orgs/8/quizzes), got ${p3h_by8}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3h: byBaseScope reports 7=${p3h_by7} 8=${p3h_by8}, matching the rows actually served under each declared tenant"

# The whole block's own total, outside any guard -- p2c_checks/p2d_checks'
# own silent-skip protection (:53-64), which the P3e and P3g blocks above
# did not carry (D14 P16's own text names that gap by section).
p3h_want_checks=17
if [[ "$p3h_checks" != "$p3h_want_checks" ]]; then
	echo "FAIL  P3h acceptance (whole block): expected exactly ${p3h_want_checks} checks to have run, ${p3h_checks} actually ran -- the block was short-circuited somewhere before reaching here"
	exit 1
fi
echo "PASS  P3h acceptance (whole block): all ${p3h_checks} checks ran to completion"

# --------------------------------------------------------------------------
# P3f acceptance (mocker-p3f-rederive, D9 P15): the rederive verb against the
# REAL running image. Every mutation property in D9 is already pinned by Go
# tests over a sqlite fixture that can seed a deliberately narrow generation
# 1 by hand (D9's own "the positive control cannot come from the deriver"
# clause) -- what only this file can prove is that the wired route, the
# wired internal/specs method and the wired MCP adapter actually reach a
# live binary end to end: import a spec, call POST /api/specs/{id}/rederive,
# and read the suggestion listing back afterward -- the three verbs P15's
# own text names, never a bare "got 200".
# --------------------------------------------------------------------------
echo "== P3f: import a dedicated /gadgets spec for the rederive walk =="

p3f_checks=0

p3f_doc=$(jq -n '{
	openapi: "3.1.0",
	info: {title: "P3f Gadgets", version: "1.0.0"},
	paths: {
		"/gadgets": {
			get: {
				operationId: "listGadgets",
				responses: {"200": {description: "ok", content: {"application/json": {schema: {type: "array", items: {"$ref": "#/components/schemas/Gadget"}}}}}}
			}
		},
		"/gadgets/{id}": {
			get: {
				operationId: "getGadget",
				parameters: [{name: "id", in: "path", required: true, schema: {type: "integer"}}],
				responses: {"200": {description: "ok", content: {"application/json": {schema: {"$ref": "#/components/schemas/Gadget"}}}}}
			}
		}
	},
	components: {
		schemas: {
			Gadget: {
				type: "object",
				properties: {id: {type: "integer"}, name: {type: "string"}},
				required: ["id"]
			}
		}
	}
}')

p3f_import_body=$(jq -n --arg name "P3f Gadgets" --arg source "upload" --arg document "$p3f_doc" \
	'{name: $name, source: $source, document: $document}')
p3f_import_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p3f_import_body" -H "X-CSRF-Token: ${csrf}")
p3f_spec_id=$(jq -r '.id' "$BODY_FILE")
p3f_checks=$((p3f_checks + 1))
if [[ "$p3f_import_status" != "201" || -z "$p3f_spec_id" || "$p3f_spec_id" == "null" ]]; then
	echo "FAIL  P3f: import the /gadgets spec: want status 201, got ${p3f_import_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3f: imported the /gadgets spec (id ${p3f_spec_id}) -- Import's own backfill already wrote generation 1"

echo "== P3f: GET the suggestion listing before any rederive call =="

http_json GET "$ADMIN_HOST" "/api/specs/${p3f_spec_id}/resource-suggestions" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p3f_listing_before=$(jq -cS '.suggestions | sort_by(.routeFamily)' "$BODY_FILE")
p3f_checks=$((p3f_checks + 1))
if ! jq -e '.suggestions | any(.[]; .routeFamily == "/gadgets")' "$BODY_FILE" >/dev/null 2>&1; then
	echo "FAIL  P3f: GET .../resource-suggestions before any rederive: want /gadgets among the suggestions, got: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3f: pre-rederive listing already names /gadgets (generation 1, from Import's own backfill) -- ${p3f_listing_before}"

echo "== P3f: POST /api/specs/{id}/rederive over an UNCHANGED spec -- a no-op, generation 1 stays newest =="

p3f_rederive1_status=$(http_json POST "$ADMIN_HOST" "/api/specs/${p3f_spec_id}/rederive" '{}' -H "X-CSRF-Token: ${csrf}")
p3f_changed1=$(jq -r '.changed' "$BODY_FILE")
p3f_gen1=$(jq -r '.generation' "$BODY_FILE")
p3f_added1=$(jq -c '.added' "$BODY_FILE")
p3f_removed1=$(jq -c '.removed' "$BODY_FILE")
p3f_checks=$((p3f_checks + 1))
if [[ "$p3f_rederive1_status" != "200" || "$p3f_changed1" != "false" || "$p3f_gen1" != "1" || "$p3f_added1" != "[]" || "$p3f_removed1" != "[]" ]]; then
	echo "FAIL  P3f: POST .../rederive over an untouched spec: want status 200, changed:false, generation:1, added:[] removed:[], got status ${p3f_rederive1_status} changed=${p3f_changed1} generation=${p3f_gen1} added=${p3f_added1} removed=${p3f_removed1}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3f: first rederive over an unchanged document reports changed:false, generation:1, no families entered or left"

echo "== P3f: a SECOND rederive is still a no-op -- the comparison is idempotent, not one-shot =="

p3f_rederive2_status=$(http_json POST "$ADMIN_HOST" "/api/specs/${p3f_spec_id}/rederive" '{}' -H "X-CSRF-Token: ${csrf}")
p3f_changed2=$(jq -r '.changed' "$BODY_FILE")
p3f_gen2=$(jq -r '.generation' "$BODY_FILE")
p3f_checks=$((p3f_checks + 1))
if [[ "$p3f_rederive2_status" != "200" || "$p3f_changed2" != "false" || "$p3f_gen2" != "1" ]]; then
	echo "FAIL  P3f: a second consecutive rederive: want status 200, changed:false, generation still 1, got status ${p3f_rederive2_status} changed=${p3f_changed2} generation=${p3f_gen2}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3f: second rederive still changed:false at generation 1 -- no phantom generation was minted"

echo "== P3f: the suggestion listing is byte-identical after two no-op rederives =="

http_json GET "$ADMIN_HOST" "/api/specs/${p3f_spec_id}/resource-suggestions" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p3f_listing_after=$(jq -cS '.suggestions | sort_by(.routeFamily)' "$BODY_FILE")
p3f_checks=$((p3f_checks + 1))
if [[ "$p3f_listing_after" != "$p3f_listing_before" ]]; then
	echo "FAIL  P3f: GET .../resource-suggestions after two no-op rederives: want the SAME listing as before (${p3f_listing_before}), got ${p3f_listing_after} -- a no-op call must leave the read side untouched"
	exit 1
fi
echo "PASS  P3f: the listing read back after the two no-op calls is unchanged -- $(jq -c '. | length' <<<"$p3f_listing_after") families, same as before"

echo "== P3f: POST /api/specs/{unknown}/rederive -- 404, never derive from thin air =="

p3f_unknown_status=$(http_json POST "$ADMIN_HOST" "/api/specs/999999999/rederive" '{}' -H "X-CSRF-Token: ${csrf}")
p3f_checks=$((p3f_checks + 1))
if [[ "$p3f_unknown_status" != "404" ]]; then
	echo "FAIL  P3f: POST /api/specs/999999999/rederive (a spec id that parses but names nothing): want status 404, got ${p3f_unknown_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P3f: POST /api/specs/999999999/rederive answers 404 -- the same 404-before-derive shape the sibling GET already has"

echo "== P3f: the MCP tool rederive_suggestions reaches the SAME repo method through the loopback adapter =="

mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" rederive_suggestions "$(jq -n --argjson sid "$p3f_spec_id" '{specId: $sid}')")
p3f_mcp_changed=$(jq -r '.result.structuredContent.changed' "$BODY_FILE")
p3f_mcp_gen=$(jq -r '.result.structuredContent.generation' "$BODY_FILE")
p3f_checks=$((p3f_checks + 1))
if [[ "$mcp_status" != "200" || "$p3f_mcp_changed" != "false" || "$p3f_mcp_gen" != "1" ]]; then
	echo "FAIL  MCP rederive_suggestions over spec ${p3f_spec_id}: want status 200, structuredContent.changed:false generation:1 (a third consecutive no-op, this time over the MCP adapter), got status ${mcp_status} changed=${p3f_mcp_changed} generation=${p3f_mcp_gen}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  MCP rederive_suggestions: changed:false generation:1 -- the tool's loopback call reached the identical [specs.Repo.Rederive], not a stub"

# The whole block's own total, outside any guard -- the same silent-skip
# protection p2c_checks/p2d_checks/p3h_checks already give their own blocks.
p3f_want_checks=7
if [[ "$p3f_checks" != "$p3f_want_checks" ]]; then
	echo "FAIL  P3f acceptance (whole block): expected exactly ${p3f_want_checks} checks to have run, ${p3f_checks} actually ran -- the block was short-circuited somewhere before reaching here"
	exit 1
fi
echo "PASS  P3f acceptance (whole block): all ${p3f_checks} checks ran to completion"

# --------------------------------------------------------------------------
# P4a acceptance (mocker-p4a-triage, D8 P20): GET /api/workspaces/{id}/drift
# and its MCP tool, get_workspace_drift, against the REAL running image.
# Every mutation property this route and tool answer to is already pinned by
# Go tests over a sqlite fixture (D8's own properties, and P16/P26 in
# internal/mcp) -- what only this file can prove is that the wired route,
# its wired repository call and the wired MCP adapter reach a live binary
# end to end: import spec A declaring one operation, create a workspace
# bound to it, pin an override on that operation, import spec B lacking it,
# re-bind the workspace to spec B, and read the orphaned override back both
# through the admin route and through the MCP tool -- P20's own two-verb
# requirement, never a bare "got 200" on either side.
# --------------------------------------------------------------------------
echo "== P4a: import spec A declaring POST /auth/login =="

p4a_checks=0

p4a_doc_a=$(jq -n '{
	openapi: "3.0.3",
	info: {title: "P4a Auth", version: "1.0.0"},
	paths: {
		"/auth/login": {
			post: {
				operationId: "login",
				responses: {"200": {description: "ok", content: {"application/json": {schema: {type: "object", properties: {token: {type: "string"}}}}}}}
			}
		}
	}
}')
p4a_import_a_body=$(jq -n --arg name "P4a Auth" --arg source "upload" --arg document "$p4a_doc_a" \
	'{name: $name, source: $source, document: $document}')
p4a_import_a_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p4a_import_a_body" -H "X-CSRF-Token: ${csrf}")
p4a_spec_a=$(jq -r '.id' "$BODY_FILE")
p4a_checks=$((p4a_checks + 1))
if [[ "$p4a_import_a_status" != "201" || -z "$p4a_spec_a" || "$p4a_spec_a" == "null" ]]; then
	echo "FAIL  P4a: import spec A (POST /auth/login): want status 201, got ${p4a_import_a_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P4a: imported spec A (id ${p4a_spec_a})"

echo "== P4a: create a workspace bound to spec A =="

p4a_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces \
	"$(jq -cn --arg n "p4a-drift" --argjson s "$p4a_spec_a" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p4a_ws_id=$(jq -r '.id' "$BODY_FILE")
p4a_checks=$((p4a_checks + 1))
if [[ "$p4a_ws_status" != "201" || -z "$p4a_ws_id" || "$p4a_ws_id" == "null" ]]; then
	echo "FAIL  P4a: create workspace bound to spec A: want status 201, got ${p4a_ws_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P4a: workspace ${p4a_ws_id} created, bound to spec A"

echo "== P4a: pin an override on POST /auth/login =="

p4a_opkey=$(jq -rn --arg s "POST /auth/login" '$s | @uri')
p4a_override_ev=$(op_edit_version "$p4a_ws_id" "$p4a_opkey")
p4a_override_body=$(jq -n --argjson ev "$p4a_override_ev" '{overrideOn: true, routeOff: false, responses: {
	"200": {mode: "generated", recipes: {token: {kind: "const", value: "P4A-PINNED"}}}
}, editVersion: $ev}')
p4a_override_status=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p4a_ws_id}/operations/${p4a_opkey}" "$p4a_override_body" -H "X-CSRF-Token: ${csrf}")
p4a_checks=$((p4a_checks + 1))
if [[ "$p4a_override_status" != "200" ]]; then
	echo "FAIL  P4a: PUT an override on POST /auth/login: want status 200, got ${p4a_override_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P4a: override pinned on POST /auth/login"

echo "== P4a: import spec B, lacking POST /auth/login =="

p4a_doc_b=$(jq -n '{
	openapi: "3.0.3",
	info: {title: "P4a Widgets", version: "1.0.0"},
	paths: {
		"/widgets": {
			get: {
				operationId: "listWidgets",
				responses: {"200": {description: "ok", content: {"application/json": {schema: {type: "array", items: {type: "object"}}}}}}
			}
		}
	}
}')
p4a_import_b_body=$(jq -n --arg name "P4a Widgets" --arg source "upload" --arg document "$p4a_doc_b" \
	'{name: $name, source: $source, document: $document}')
p4a_import_b_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p4a_import_b_body" -H "X-CSRF-Token: ${csrf}")
p4a_spec_b=$(jq -r '.id' "$BODY_FILE")
p4a_checks=$((p4a_checks + 1))
if [[ "$p4a_import_b_status" != "201" || -z "$p4a_spec_b" || "$p4a_spec_b" == "null" ]]; then
	echo "FAIL  P4a: import spec B (/widgets only): want status 201, got ${p4a_import_b_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P4a: imported spec B (id ${p4a_spec_b})"

echo "== P4a: re-bind the workspace to spec B =="

p4a_ws_ev=$(ws_edit_version "$p4a_ws_id")
p4a_rebind_status=$(http_json PATCH "$ADMIN_HOST" "/api/workspaces/${p4a_ws_id}" \
	"$(jq -cn --argjson s "$p4a_spec_b" --argjson ev "$p4a_ws_ev" '{specId: $s, editVersion: $ev}')" -H "X-CSRF-Token: ${csrf}")
p4a_checks=$((p4a_checks + 1))
if [[ "$p4a_rebind_status" != "200" ]]; then
	echo "FAIL  P4a: PATCH the workspace to spec B: want status 200, got ${p4a_rebind_status}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P4a: workspace re-bound to spec B"

echo "== P4a: GET /api/workspaces/{id}/drift names the orphaned override =="

p4a_drift_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${p4a_ws_id}/drift" '' -H "X-CSRF-Token: ${csrf}")
p4a_has_drift=$(jq -r '.hasDrift' "$BODY_FILE")
p4a_orphan_opkey=$(jq -r '.orphanedOverrides[0].opKey // ""' "$BODY_FILE")
p4a_checks=$((p4a_checks + 1))
if [[ "$p4a_drift_status" != "200" || "$p4a_has_drift" != "true" || "$p4a_orphan_opkey" != "$p4a_opkey" ]]; then
	echo "FAIL  P4a: GET .../drift after re-bind: want status 200 hasDrift:true orphanedOverrides[0].opKey=${p4a_opkey}, got status ${p4a_drift_status} hasDrift=${p4a_has_drift} opKey=${p4a_orphan_opkey}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  P4a: GET .../drift names the orphaned override by its opKey (${p4a_orphan_opkey})"

echo "== P4a: the MCP tool get_workspace_drift names the same override =="

mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" get_workspace_drift "$(jq -n --argjson wid "$p4a_ws_id" '{workspaceId: $wid}')")
p4a_mcp_has_drift=$(jq -r '.result.structuredContent.hasDrift' "$BODY_FILE")
p4a_mcp_opkey=$(jq -r '.result.structuredContent.orphanedOverrides[0].opKey // ""' "$BODY_FILE")
p4a_checks=$((p4a_checks + 1))
if [[ "$mcp_status" != "200" || "$p4a_mcp_has_drift" != "true" || "$p4a_mcp_opkey" != "$p4a_opkey" ]]; then
	echo "FAIL  MCP get_workspace_drift over workspace ${p4a_ws_id}: want status 200 hasDrift:true orphanedOverrides[0].opKey=${p4a_opkey}, got status ${mcp_status} hasDrift=${p4a_mcp_has_drift} opKey=${p4a_mcp_opkey}: $(cat "$BODY_FILE")"
	exit 1
fi
echo "PASS  MCP get_workspace_drift: hasDrift:true, names the same override -- the tool's loopback call reached the real GET .../drift route, not a stub"

# The whole block's own total, outside any guard -- the same silent-skip
# protection p2c_checks/p2d_checks/p3h_checks/p3f_checks already give their
# own blocks.
p4a_want_checks=7
if [[ "$p4a_checks" != "$p4a_want_checks" ]]; then
	echo "FAIL  P4a acceptance (whole block): expected exactly ${p4a_want_checks} checks to have run, ${p4a_checks} actually ran -- the block was short-circuited somewhere before reaching here"
	exit 1
fi
echo "PASS  P4a acceptance (whole block): all ${p4a_checks} checks ran to completion"

# --------------------------------------------------------------------------
# P6a (decisions.md mocker-p6a-sse, §4): the admin traffic feed over SSE.
# Runs on the MCP stack (MOCKER_MCP_KEY set, cookie session still valid)
# because A6 needs tools/call, and BEFORE the path-mode recreate below. The
# three MOCKER_STREAM_* values were written into .env at the top of this
# file (A8: 7/3/4, every one non-default and none of them round); this block
# reads them back out of the running container rather than trusting what it
# wrote, then observes behaviour consistent with them (A9-A14, A16, A18-A20).
#
# The clause numbers are the order the clauses were ADDED, never the order
# they run; each segment below names the clause it observes, and the
# segments that must not overlap A13's window (A12, A14, A19, A20, A18) are
# placed outside it, as their own text requires. Every connection a segment
# opens is closed before the next segment measures anything.
# --------------------------------------------------------------------------
echo "== P6a: the SSE traffic feed =="
p6a_checks=0
p6a_stream_route="/api/workspaces/${ws_id}/traffic/stream"

p6a_stats() {
	# One field of GET /api/stream/stats, read live.
	http_json GET "$ADMIN_HOST" /api/stream/stats '' -H "X-CSRF-Token: ${csrf}" >/dev/null
	jq -r ".$1" "$BODY_FILE"
}

p6a_wait_open() {
	# Poll stats.open until it equals $1, for at most $2 seconds. Prints the
	# final value; the caller compares.
	local want=$1 secs=$2 tries=0 got
	while :; do
		got=$(p6a_stats open)
		if [[ "$got" == "$want" ]]; then
			break
		fi
		tries=$((tries + 1))
		if ((tries >= secs * 4)); then
			break
		fi
		sleep 0.25
	done
	printf '%s' "$got"
}

# p6a_jar is a read-only SNAPSHOT of the cookie jar for every background
# curl in this block. http_json rewrites $COOKIE_JAR (-c) on every call, and
# a background curl reading it (-b) at that instant sees an empty file, sends
# no cookie, and is answered 401 — measured on this block's first green-ish
# run as "64 opened, 63 live", with p6a_wait_open's own stats polling doing
# the rewriting. Refreshed after the one re-login this block performs.
p6a_jar=$(mktemp)
cp "$COOKIE_JAR" "$p6a_jar"

p6a_open_stream() {
	# A curl -N reader of workspace $1's stream, writing frames to $2 as they
	# arrive; prints the pid. --max-time is a backstop for a server that
	# never closes, not a duration any clause reads.
	curl -sN --max-time 300 -b "$p6a_jar" -H "Host: ${ADMIN_HOST}" \
		"${BASE_URL}/api/workspaces/$1/traffic/stream?since=0" >"$2" 2>/dev/null &
	echo $!
}

p6a_mock_hit() {
	# One recorded mock-plane request (a 404 on a spec-less workspace is
	# still a traffic row).
	curl -s -o /dev/null -H "Host: $1" "${BASE_URL}/p6a/$2"
}

echo "== P6a A8: the three MOCKER_STREAM_* values reach the running container =="
p6a_container=$(docker compose ps -q mocker)
p6a_env=$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$p6a_container" | grep -E '^MOCKER_STREAM_(FRAME_TIMEOUT|PING|SESSION_RECHECK)=' | sort | tr '\n' ' ')
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_env" != "MOCKER_STREAM_FRAME_TIMEOUT=4 MOCKER_STREAM_PING=3 MOCKER_STREAM_SESSION_RECHECK=7 " ]]; then
	echo "FAIL  P6a A8: the container's environment does not carry the three non-default values (want FRAME_TIMEOUT=4 PING=3 SESSION_RECHECK=7), got '${p6a_env}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A8: the running container carries MOCKER_STREAM_FRAME_TIMEOUT=4 PING=3 SESSION_RECHECK=7"
fi

echo "== P6a: a fresh workspace for the feed =="
p6a_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p6a"}' -H "X-CSRF-Token: ${csrf}")
if [[ "$p6a_status" != "201" ]]; then
	echo "FAIL  P6a setup: create workspace: want 201, got ${p6a_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p6a_ws_id=$(jq -r '.id' "$BODY_FILE")
p6a_host="$(jq -r '.slug' "$BODY_FILE").${WORKSPACE_HOST_BASE}"
p6a_stream_route="/api/workspaces/${p6a_ws_id}/traffic/stream"

echo "== P6a A12: the poll route is unchanged, and the stream emits no CORS allowance =="
p6a_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${p6a_ws_id}/traffic/poll?since=0" '' -H "X-CSRF-Token: ${csrf}")
p6a_poll_keys=$(jq -r 'keys | join(",")' "$BODY_FILE")
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_status" != "200" || "$p6a_poll_keys" != "dropped,lastId,rows" ]]; then
	echo "FAIL  P6a A12: GET .../traffic/poll?since=0: want 200 with rows/lastId/dropped, got ${p6a_status} keys '${p6a_poll_keys}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A12: GET .../traffic/poll?since=0 still answers 200 with rows, lastId, dropped"
fi
p6a_headers=$(mktemp)
# --max-time 3 is part of the clause: the response is a stream that does not
# end, so curl's timeout exit (28) is the EXPECTED one here.
curl -s -D "$p6a_headers" -o /dev/null --max-time 3 -b "$p6a_jar" -H "Host: ${ADMIN_HOST}" "${BASE_URL}${p6a_stream_route}" || true
p6a_status_lines=$(grep -c '^HTTP/' "$p6a_headers" || true)
p6a_cors_lines=$(grep -ci 'access-control' "$p6a_headers" || true)
p6a_ctype=$(grep -i '^content-type:' "$p6a_headers" | tr -d '\r' | head -n1)
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_status_lines" != "1" || "$p6a_cors_lines" != "0" || "$p6a_ctype" != *"text/event-stream"* ]]; then
	echo "FAIL  P6a A12: bounded probe of the stream route: want exactly one HTTP status line, zero access-control headers, text/event-stream; got ${p6a_status_lines} status line(s), ${p6a_cors_lines} access-control line(s), '${p6a_ctype}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A12: the stream handshake answered (one status line, text/event-stream) with no access-control header"
fi
rm -f "$p6a_headers"

echo "== P6a A13 read 1: stats before any stream =="
p6a_open_before=$(p6a_stats open)
p6a_cap_1=$(p6a_stats cap)

echo "== P6a A9/A10/A11/A13: one connection held past the 30-second WriteTimeout =="
p6a_stream_out=$(mktemp)
p6a_stream_pid=$(p6a_open_stream "$p6a_ws_id" "$p6a_stream_out")
p6a_open_live=$(p6a_wait_open "$((p6a_open_before + 1))" 5)
p6a_cap_2=$(p6a_stats cap)
p6a_t0=$(date +%s%N)
# A10: the ping period is 3 s (A8); any ten-second idle window holds at
# least three `:` comment lines. Measured between t=10 and t=20.
sleep 10
p6a_pings_10=$(grep -c '^:' "$p6a_stream_out" || true)
sleep 10
p6a_pings_20=$(grep -c '^:' "$p6a_stream_out" || true)
p6a_checks=$((p6a_checks + 1))
if ((p6a_pings_20 - p6a_pings_10 < 3)); then
	echo "FAIL  P6a A10: only $((p6a_pings_20 - p6a_pings_10)) ping comment line(s) arrived in a ten-second idle window (want >= 3 at MOCKER_STREAM_PING=3)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A10: $((p6a_pings_20 - p6a_pings_10)) ping comment lines in a ten-second idle window at MOCKER_STREAM_PING=3"
fi
# A9: past the 30-second mark the connection must still be open and must
# still deliver a frame. 10 + 10 + 13 + 2.5 = 35.5 s of hold, measured in
# milliseconds below: an integer-second clock could read 34 on a hold of
# 34.9 and go red against a correct server.
sleep 13
p6a_frames_before=$(grep -c '^event: traffic' "$p6a_stream_out" || true)
p6a_alive_at_32=false
if kill -0 "$p6a_stream_pid" 2>/dev/null; then
	p6a_alive_at_32=true
fi
p6a_mock_hit "$p6a_host" "after-thirty"
sleep 2.5
p6a_frames_after=$(grep -c '^event: traffic' "$p6a_stream_out" || true)
p6a_elapsed_ms=$((($(date +%s%N) - p6a_t0) / 1000000))
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_alive_at_32" != true ]] || ((p6a_frames_after <= p6a_frames_before)) || ((p6a_elapsed_ms < 35000)); then
	echo "FAIL  P6a A9: the stream must outlive the server's 30 s WriteTimeout and deliver a frame after it; alive at 32 s: ${p6a_alive_at_32}, frames ${p6a_frames_before} -> ${p6a_frames_after}, elapsed ${p6a_elapsed_ms}ms"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A9: held ${p6a_elapsed_ms}ms and received a traffic frame after the 30-second mark (${p6a_frames_before} -> ${p6a_frames_after})"
fi
# The frame carries the poll view with the SSE id equal to lastId (D6).
p6a_last_frame_id=$(grep '^id: ' "$p6a_stream_out" | tail -n1 | sed 's/^id: //')
p6a_last_frame_lastid=$(grep '^data: ' "$p6a_stream_out" | tail -n1 | sed 's/^data: //' | jq -r '.lastId')
p6a_last_frame_path=$(grep '^data: ' "$p6a_stream_out" | tail -n1 | sed 's/^data: //' | jq -r '.rows[-1].path')
p6a_checks=$((p6a_checks + 1))
if [[ -z "$p6a_last_frame_id" || "$p6a_last_frame_id" != "$p6a_last_frame_lastid" || "$p6a_last_frame_path" != "/p6a/after-thirty" ]]; then
	echo "FAIL  P6a D6: the last frame's id '${p6a_last_frame_id}' must equal its data.lastId '${p6a_last_frame_lastid}' and its newest row must be /p6a/after-thirty (got '${p6a_last_frame_path}')"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a D6: frame id ${p6a_last_frame_id} == data.lastId, newest row /p6a/after-thirty — the poll view rides in the frame"
fi
# A11: on THIS connection, the session is invalidated and the stream closes
# within 15 s (MOCKER_STREAM_SESSION_RECHECK=7). '{}' rather than '' as the
# body, the same as every admin DELETE above: http_json only sends
# Content-Type with a body, and the admin plane answers 415 without one.
p6a_logout_status=$(http_json POST "$ADMIN_HOST" /api/auth/logout '{}' -H "X-CSRF-Token: ${csrf}")
p6a_t_logout=$(date +%s)
p6a_closed=false
for _ in $(seq 1 60); do
	if ! kill -0 "$p6a_stream_pid" 2>/dev/null; then
		p6a_closed=true
		break
	fi
	sleep 0.25
done
p6a_close_took=$(($(date +%s) - p6a_t_logout))
wait "$p6a_stream_pid" 2>/dev/null || true
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_logout_status" != "204" && "$p6a_logout_status" != "200" ]] || [[ "$p6a_closed" != true ]]; then
	echo "FAIL  P6a A11: after POST /api/logout (status ${p6a_logout_status}) the stream must close within 15 s; closed: ${p6a_closed} after ${p6a_close_took}s"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A11: the stream closed ${p6a_close_took}s after the session was logged out (recheck every 7 s)"
fi
rm -f "$p6a_stream_out"

echo "== P6a: logging in again (A11 killed the session) =="
p6a_status=$(http_json POST "$ADMIN_HOST" /api/auth/login "{\"password\":\"${SMOKE_PASSWORD}\",\"name\":\"smoke\"}")
if [[ "$p6a_status" != "200" ]]; then
	echo "FAIL  P6a: re-login after A11: want 200, got ${p6a_status}: $(cat "$BODY_FILE")"
	exit 1
fi
csrf=$(jq -r '.csrfToken' "$BODY_FILE")
cp "$COOKIE_JAR" "$p6a_jar"

echo "== P6a A13 read 3: stats after the close =="
p6a_open_after=$(p6a_wait_open "$p6a_open_before" 5)
p6a_cap_3=$(p6a_stats cap)
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_open_live" != "$((p6a_open_before + 1))" || "$p6a_open_after" != "$p6a_open_before" || "$p6a_cap_1" != "64" || "$p6a_cap_2" != "64" || "$p6a_cap_3" != "64" ]]; then
	echo "FAIL  P6a A13: open must rise by exactly one and return (got ${p6a_open_before} -> ${p6a_open_live} -> ${p6a_open_after}) with cap 64 in all three reads (got ${p6a_cap_1}/${p6a_cap_2}/${p6a_cap_3})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A13: open ${p6a_open_before} -> ${p6a_open_live} -> ${p6a_open_after}, cap 64 in all three reads"
fi

echo "== P6a A14/A20: a stalled peer is cut by the frame deadline while an ordinary peer keeps receiving =="
# The stalled client is a raw socket that sends the handshake and never
# reads (a curl piped into a stopped reader cannot notice the server's close
# while it is blocked on the pipe, so its exit would prove nothing; the
# registry's own open count is what shows the server cut it).
p6a_cookie=$(awk '$6 == "mocker_session" {print $7}' "$COOKIE_JAR" | tail -n1)
p6a_port=${BASE_URL##*:}
p6a_ordinary_out=$(mktemp)
p6a_ordinary_pid=$(p6a_open_stream "$p6a_ws_id" "$p6a_ordinary_out")
exec 3<>"/dev/tcp/127.0.0.1/${p6a_port}"
printf 'GET %s?since=0 HTTP/1.1\r\nHost: %s\r\nCookie: mocker_session=%s\r\nAccept: text/event-stream\r\n\r\n' "$p6a_stream_route" "$ADMIN_HOST" "$p6a_cookie" >&3
p6a_open_two=$(p6a_wait_open "$((p6a_open_before + 2))" 5)
p6a_coalesced_before=$(p6a_stats coalescedNudges)
# One recorded row is ~8 KiB here: a POST whose body is exactly
# MOCKER_TRAFFIC_MAX_BODY's default, so that the socket's buffers (a few MiB
# on loopback, plus the port mapping's own) fill within a bounded number
# of waves. Each wave is 150 rows (> one 64-row batch) and waves are spaced
# more than one 500 ms flush tick apart, the shape A20 requires for the
# nudges to be distinct and for a later one to land while the stalled
# connection still holds its writer. At least three waves always; at most
# twelve, stopping early once the registry reports the stalled peer gone.
p6a_blob=$(mktemp)
head -c 8192 /dev/zero | tr '\0' 'x' >"$p6a_blob"
p6a_wave() {
	seq 150 | xargs -P 8 -I{} curl -s -o /dev/null -X POST -H "Host: ${p6a_host}" \
		-H 'Content-Type: application/octet-stream' --data-binary "@${p6a_blob}" "${BASE_URL}/p6a/burst/{}"
	sleep 0.7
}
p6a_waves=0
p6a_open_now=$p6a_open_two
while ((p6a_waves < 12)); do
	p6a_wave
	p6a_waves=$((p6a_waves + 1))
	p6a_open_now=$(p6a_stats open)
	if ((p6a_waves >= 3)) && [[ "$p6a_open_now" == "$((p6a_open_before + 1))" ]]; then
		break
	fi
done
p6a_t_burst=$(date +%s)
p6a_open_cut=$(p6a_wait_open "$((p6a_open_before + 1))" 15)
p6a_cut_took=$(($(date +%s) - p6a_t_burst))
p6a_ordinary_alive=false
if kill -0 "$p6a_ordinary_pid" 2>/dev/null; then
	p6a_ordinary_alive=true
fi
sleep 2
p6a_ordinary_frames=$(grep -c '^event: traffic' "$p6a_ordinary_out" || true)
# "Receives every row" means EVERY row, not the last one: the ids of all rows
# across all frames, deduplicated, must form one contiguous range ending at
# the table's newest id — a server that dropped a page in the middle would
# still show the right last id. (Rows older than the retention cap can be
# pruned before a slow client reads them; this client drains at once, so a
# gap here is the server's, not retention's.)
p6a_ordinary_ids=$(grep '^data: ' "$p6a_ordinary_out" | sed 's/^data: //' | jq -r '.rows[].id' | sort -un)
p6a_ordinary_count=$(printf '%s\n' "$p6a_ordinary_ids" | grep -c . || true)
p6a_ordinary_min=$(printf '%s\n' "$p6a_ordinary_ids" | head -n1)
p6a_ordinary_last=$(printf '%s\n' "$p6a_ordinary_ids" | tail -n1)
p6a_ordinary_span=$((p6a_ordinary_last - p6a_ordinary_min + 1))
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6a_ws_id}/traffic?limit=1" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6a_newest_id=$(jq -r '.rows[0].id' "$BODY_FILE")
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_open_two" != "$((p6a_open_before + 2))" || "$p6a_open_cut" != "$((p6a_open_before + 1))" || "$p6a_ordinary_alive" != true ]] || ((p6a_ordinary_frames < 1)) || [[ "$p6a_ordinary_last" != "$p6a_newest_id" ]] || ((p6a_ordinary_count != p6a_ordinary_span)); then
	echo "FAIL  P6a A14: with two streams open (${p6a_open_two}) and ${p6a_waves} burst wave(s) of 150 x 8 KiB rows, the stalled peer must be cut within 15 s (open ${p6a_open_cut}, want $((p6a_open_before + 1)), ${p6a_cut_took}s after the burst) while the ordinary peer stays connected (alive: ${p6a_ordinary_alive}) and receives every row (frames ${p6a_ordinary_frames}, ${p6a_ordinary_count} distinct ids over a span of ${p6a_ordinary_span}, last id ${p6a_ordinary_last} vs newest ${p6a_newest_id})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A14: after ${p6a_waves} wave(s) the stalled peer was disconnected (open ${p6a_open_two} -> ${p6a_open_cut}, ${p6a_cut_took}s after the burst) while the ordinary peer received ${p6a_ordinary_frames} frame(s), ${p6a_ordinary_count} contiguous ids up to ${p6a_newest_id}"
fi
p6a_coalesced_after=$(p6a_stats coalescedNudges)
p6a_checks=$((p6a_checks + 1))
if ((p6a_coalesced_after <= p6a_coalesced_before)); then
	echo "FAIL  P6a A20: coalescedNudges must RISE across a burst that lands while a stalled connection holds its writer: ${p6a_coalesced_before} -> ${p6a_coalesced_after}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A20: coalescedNudges rose ${p6a_coalesced_before} -> ${p6a_coalesced_after} across the burst"
fi
exec 3>&-
kill "$p6a_ordinary_pid" 2>/dev/null || true
wait "$p6a_ordinary_pid" 2>/dev/null || true
rm -f "$p6a_ordinary_out"
p6a_open_after_a14=$(p6a_wait_open "$p6a_open_before" 10)
if [[ "$p6a_open_after_a14" != "$p6a_open_before" ]]; then
	echo "FAIL  P6a A14 teardown: open is ${p6a_open_after_a14}, want ${p6a_open_before} before the next segment"
	fail_count=$((fail_count + 1))
fi

echo "== P6a A19: 64 streams, and the 65th is refused =="
p6a_refused_before=$(p6a_stats refusedCap)
p6a_cap_pids=()
for _ in $(seq 1 64); do
	curl -sN --max-time 300 -b "$p6a_jar" -H "Host: ${ADMIN_HOST}" "${BASE_URL}${p6a_stream_route}" >/dev/null 2>&1 &
	p6a_cap_pids+=("$!")
done
p6a_open_64=$(p6a_wait_open 64 15)
p6a_65_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' --max-time 5 -b "$p6a_jar" -H "Host: ${ADMIN_HOST}" "${BASE_URL}${p6a_stream_route}" || true)
p6a_65_code=$(jq -r '.error.code // empty' "$BODY_FILE" 2>/dev/null || true)
p6a_refused_after=$(p6a_stats refusedCap)
for p in "${p6a_cap_pids[@]}"; do
	kill "$p" 2>/dev/null || true
done
for p in "${p6a_cap_pids[@]}"; do
	wait "$p" 2>/dev/null || true
done
p6a_open_after_cap=$(p6a_wait_open "$p6a_open_before" 15)
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_open_64" != "64" || "$p6a_65_status" != "503" || "$p6a_65_code" != "service_unavailable" || "$p6a_refused_after" != "$((p6a_refused_before + 1))" || "$p6a_open_after_cap" != "$p6a_open_before" ]]; then
	echo "FAIL  P6a A19: want 64 open, the 65th refused 503 service_unavailable, refusedCap +1, open back to ${p6a_open_before}; got open ${p6a_open_64}, 65th ${p6a_65_status} '${p6a_65_code}', refusedCap ${p6a_refused_before} -> ${p6a_refused_after}, open after ${p6a_open_after_cap}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A19: 64 streams open, the 65th answered 503 service_unavailable, refusedCap ${p6a_refused_before} -> ${p6a_refused_after}, all closed"
fi

echo "== P6a A20 control: the same waves with one draining client do not coalesce =="
p6a_control_out=$(mktemp)
p6a_control_pid=$(p6a_open_stream "$p6a_ws_id" "$p6a_control_out")
p6a_open_ctl=$(p6a_wait_open "$((p6a_open_before + 1))" 5)
p6a_coalesced_ctl_before=$(p6a_stats coalescedNudges)
p6a_wave
p6a_wave
p6a_wave
sleep 1.5
p6a_coalesced_ctl_after=$(p6a_stats coalescedNudges)
p6a_control_frames=$(grep -c '^event: traffic' "$p6a_control_out" || true)
kill "$p6a_control_pid" 2>/dev/null || true
wait "$p6a_control_pid" 2>/dev/null || true
rm -f "$p6a_control_out" "$p6a_blob"
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_open_ctl" != "$((p6a_open_before + 1))" || "$p6a_coalesced_ctl_after" != "$p6a_coalesced_ctl_before" ]] || ((p6a_control_frames < 1)); then
	echo "FAIL  P6a A20 control: with one draining client (open ${p6a_open_ctl}) three waves must move coalescedNudges by nothing: ${p6a_coalesced_ctl_before} -> ${p6a_coalesced_ctl_after} (client received ${p6a_control_frames} frame(s))"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A20 control: three waves against a draining client, coalescedNudges unchanged at ${p6a_coalesced_ctl_after}, client received ${p6a_control_frames} frame(s)"
fi
p6a_wait_open "$p6a_open_before" 10 >/dev/null

echo "== P6a A16: a clear does not reissue traffic ids =="
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6a_ws_id}/traffic?limit=1" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6a_id_before_clear=$(jq -r '.rows[0].id' "$BODY_FILE")
p6a_clear_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p6a_ws_id}/traffic" '{}' -H "X-CSRF-Token: ${csrf}")
p6a_mock_hit "$p6a_host" "after-clear"
sleep 1.5
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6a_ws_id}/traffic?limit=1" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6a_id_after_clear=$(jq -r '.rows[0].id // 0' "$BODY_FILE")
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_clear_status" != "200" ]] || ((p6a_id_after_clear <= p6a_id_before_clear)); then
	echo "FAIL  P6a A16: after DELETE .../traffic (status ${p6a_clear_status}) the next row's id must exceed the highest before the clear: ${p6a_id_before_clear} -> ${p6a_id_after_clear}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A16: highest id before the clear ${p6a_id_before_clear}, first row after it ${p6a_id_after_clear} (AUTOINCREMENT, 0004)"
fi

echo "== P6a A18: a workspace deleted and its id reissued does not keep serving =="
p6a_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p6a-w"}' -H "X-CSRF-Token: ${csrf}")
p6a_w_id=$(jq -r '.id' "$BODY_FILE")
p6a_w_out=$(mktemp)
p6a_w_pid=$(p6a_open_stream "$p6a_w_id" "$p6a_w_out")
p6a_open_w=$(p6a_wait_open "$((p6a_open_before + 1))" 5)
p6a_del_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p6a_w_id}" '{}' -H "X-CSRF-Token: ${csrf}")
p6a_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p6a-impostor"}' -H "X-CSRF-Token: ${csrf}")
p6a_w2_id=$(jq -r '.id' "$BODY_FILE")
p6a_w2_host="$(jq -r '.slug' "$BODY_FILE").${WORKSPACE_HOST_BASE}"
# IMMEDIATELY after the id assertion: tied to the event, not to a window.
p6a_mock_hit "$p6a_w2_host" "impostor"
p6a_t_del=$(date +%s)
p6a_w_closed=false
for _ in $(seq 1 60); do
	if ! kill -0 "$p6a_w_pid" 2>/dev/null; then
		p6a_w_closed=true
		break
	fi
	sleep 0.25
done
p6a_w_took=$(($(date +%s) - p6a_t_del))
wait "$p6a_w_pid" 2>/dev/null || true
p6a_w_frames=$(grep -c '^event: traffic' "$p6a_w_out" || true)
rm -f "$p6a_w_out"
p6a_checks=$((p6a_checks + 1))
if [[ "$p6a_open_w" != "$((p6a_open_before + 1))" || "$p6a_del_status" != "204" && "$p6a_del_status" != "200" || "$p6a_w2_id" != "$p6a_w_id" || "$p6a_w_closed" != true ]] || ((p6a_w_frames > 0)); then
	echo "FAIL  P6a A18: stream on W=${p6a_w_id} (open ${p6a_open_w}), W deleted (${p6a_del_status}), new workspace id ${p6a_w2_id} (want ${p6a_w_id}, the reissued id), traffic generated on it; the connection must close within 15 s (closed: ${p6a_w_closed}, ${p6a_w_took}s) having delivered no frame (delivered ${p6a_w_frames})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A18: W=${p6a_w_id} deleted, its id reissued to a new workspace, the stream closed ${p6a_w_took}s later with 0 frames of the new workspace's traffic"
fi
p6a_wait_open "$p6a_open_before" 10 >/dev/null

echo "== P6a A6: the MCP tool get_stream_stats answers the six fields =="
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" get_stream_stats '{}')
p6a_tool_keys=$(jq -r '.result.structuredContent | keys | join(",")' "$BODY_FILE" 2>/dev/null || true)
p6a_tool_cap=$(jq -r '.result.structuredContent.cap' "$BODY_FILE" 2>/dev/null || true)
p6a_checks=$((p6a_checks + 1))
if [[ "$mcp_status" != "200" || "$p6a_tool_keys" != "byWorkspace,cap,coalescedNudges,open,refusedCap,refusedUnsupported" || "$p6a_tool_cap" != "64" ]]; then
	echo "FAIL  P6a A6: get_stream_stats: want 200 with byWorkspace,cap,coalescedNudges,open,refusedCap,refusedUnsupported and cap 64; got ${mcp_status} keys '${p6a_tool_keys}' cap '${p6a_tool_cap}': $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a A6: get_stream_stats answered all six fields, cap 64"
fi

rm -f "$p6a_jar"
p6a_want_checks=15
if [[ "$p6a_checks" != "$p6a_want_checks" ]]; then
	echo "FAIL  P6a acceptance (whole block): expected exactly ${p6a_want_checks} checks to have run, ${p6a_checks} actually ran"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6a acceptance (whole block): all ${p6a_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# P6b (decisions.md mocker-p6b-sse-mock, §3 A3): SSE mock endpoints with a
# timeline and a tick. Runs on the MCP stack right after the P6a block — the
# tools are the authoring surface (D1) — and before the path-mode recreate.
# MOCKER_STREAM_MAX_CONNS=3, MOCKER_STREAM_MAX_LIFETIME=20 and
# MOCKER_STREAM_TRAFFIC_FRAMES=first were written into .env at the top of the
# file (A3), every one non-default, and are read back from the container.
# --------------------------------------------------------------------------
echo "== P6b: SSE mock endpoints =="
p6b_checks=0
p6b_jar=$(mktemp)
cp "$COOKIE_JAR" "$p6b_jar"

p6b_stats() {
	http_json GET "$ADMIN_HOST" /api/stream/stats '' -H "X-CSRF-Token: ${csrf}" >/dev/null
	jq -r ".$1" "$BODY_FILE"
}

p6b_wait_mock_open() {
	local want=$1 secs=$2 tries=0 got
	while :; do
		got=$(p6b_stats mock.open)
		if [[ "$got" == "$want" ]]; then
			break
		fi
		tries=$((tries + 1))
		if ((tries >= secs * 4)); then
			break
		fi
		sleep 0.25
	done
	printf '%s' "$got"
}

echo "== P6b A3 env: the three MOCKER_STREAM_* values of the mock plane reach the container =="
p6b_env=$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$(docker compose ps -q mocker)" | grep -E '^MOCKER_STREAM_(MAX_CONNS|MAX_LIFETIME|TRAFFIC_FRAMES)=' | sort | tr '\n' ' ')
p6b_checks=$((p6b_checks + 1))
if [[ "$p6b_env" != "MOCKER_STREAM_MAX_CONNS=3 MOCKER_STREAM_MAX_LIFETIME=20 MOCKER_STREAM_TRAFFIC_FRAMES=first " ]]; then
	echo "FAIL  P6b env: want MAX_CONNS=3 MAX_LIFETIME=20 TRAFFIC_FRAMES=first in the container, got '${p6b_env}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b env: the container carries MOCKER_STREAM_MAX_CONNS=3 MAX_LIFETIME=20 TRAFFIC_FRAMES=first"
fi

echo "== P6b: two workspaces, a timeline endpoint and a tick endpoint through create_endpoint =="
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_workspace '{"name":"p6b"}')
p6b_ws_id=$(jq -r '.result.structuredContent.workspace.id // .result.structuredContent.id' "$BODY_FILE")
p6b_slug=$(jq -r '.result.structuredContent.workspace.slug // .result.structuredContent.slug' "$BODY_FILE")
if [[ "$mcp_status" != "200" || -z "$p6b_ws_id" || "$p6b_ws_id" == "null" ]]; then
	echo "FAIL  P6b setup: create_workspace: status ${mcp_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p6b_host="${p6b_slug}.${WORKSPACE_HOST_BASE}"
mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_workspace '{"name":"p6b-other"}' >/dev/null
p6b_other_slug=$(jq -r '.result.structuredContent.workspace.slug // .result.structuredContent.slug' "$BODY_FILE")
p6b_other_host="${p6b_other_slug}.${WORKSPACE_HOST_BASE}"

p6b_timeline_req=$(jq -n --argjson wid "$p6b_ws_id" '{workspaceId: $wid, method: "GET", path: "/timeline", kind: "sse", stream: {timeline: {frames: [{delayMs: 0, event: "one", data: {n: 1}}, {delayMs: 300, event: "two", data: {n: 2}}, {delayMs: 300, event: "three", data: {n: 3}}]}}}')
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_endpoint "$p6b_timeline_req")
p6b_timeline_kind=$(jq -r '.result.structuredContent.endpoint.kind' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$p6b_timeline_kind" != "sse" ]]; then
	echo "FAIL  P6b setup: create_endpoint (timeline): status ${mcp_status} kind '${p6b_timeline_kind}': $(cat "$BODY_FILE")"
	exit 1
fi
p6b_tick_req=$(jq -n --argjson wid "$p6b_ws_id" '{workspaceId: $wid, method: "GET", path: "/ticks", kind: "sse", stream: {tick: {intervalMs: 500, event: "price", schema: {type: "object", properties: {price: {type: "number"}, sym: {type: "string"}}, required: ["price", "sym"]}}}}')
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_endpoint "$p6b_tick_req")
p6b_tick_id=$(jq -r '.result.structuredContent.endpoint.id' "$BODY_FILE")
p6b_tick_ev=$(jq -r '.result.structuredContent.endpoint.editVersion' "$BODY_FILE")
if [[ "$mcp_status" != "200" || -z "$p6b_tick_id" || "$p6b_tick_id" == "null" ]]; then
	echo "FAIL  P6b setup: create_endpoint (tick): status ${mcp_status}: $(cat "$BODY_FILE")"
	exit 1
fi
# The other workspace gets the tick endpoint too, for (d)'s "another
# workspace still opens".
mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_workspace '{"name":"p6b-cap"}' >/dev/null
p6b_other_id=$(jq -r '.result.structuredContent.workspace.id // .result.structuredContent.id' "$BODY_FILE")
p6b_other_slug=$(jq -r '.result.structuredContent.workspace.slug // .result.structuredContent.slug' "$BODY_FILE")
p6b_other_host="${p6b_other_slug}.${WORKSPACE_HOST_BASE}"
mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_endpoint "$(jq -n --argjson wid "$p6b_other_id" '{workspaceId: $wid, method: "GET", path: "/ticks", kind: "sse", stream: {tick: {intervalMs: 500, schema: {type: "object"}}}}')" >/dev/null

echo "== P6b A3(a): the timeline arrives in order, at its delays, and the connection closes on its own =="
p6b_tl_out=$(mktemp)
p6b_t0=$(date +%s%N)
p6b_tl_status=$(curl -sN --max-time 15 -o "$p6b_tl_out" -w '%{http_code}' -H "Host: ${p6b_host}" "${BASE_URL}/timeline" || true)
p6b_tl_ms=$((($(date +%s%N) - p6b_t0) / 1000000))
p6b_tl_ids=$(grep '^id: ' "$p6b_tl_out" | sed 's/^id: //' | tr '\n' ',' || true)
p6b_tl_events=$(grep '^event: ' "$p6b_tl_out" | sed 's/^event: //' | tr '\n' ',' || true)
p6b_tl_last=$(grep '^data: ' "$p6b_tl_out" | tail -n1 || true)
p6b_checks=$((p6b_checks + 1))
if [[ "$p6b_tl_status" != "200" || "$p6b_tl_ids" != "1,2,3," || "$p6b_tl_events" != "one,two,three," || "$p6b_tl_last" != 'data: {"n":3}' ]] || ((p6b_tl_ms < 550)) || ((p6b_tl_ms > 10000)); then
	echo "FAIL  P6b A3(a): want 200, ids 1,2,3, events one,two,three, last data {\"n\":3}, a self-closing connection of 550..10000 ms; got ${p6b_tl_status} ids '${p6b_tl_ids}' events '${p6b_tl_events}' last '${p6b_tl_last}' ${p6b_tl_ms}ms"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(a): three timeline frames in order with their delays, closed by the server after ${p6b_tl_ms}ms"
fi

echo "== P6b A3(f): one traffic row for that connection, first frame in the body =="
sleep 1.5
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/traffic?limit=10" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6b_tl_rows=$(jq '[.rows[] | select(.path == "/timeline")] | length' "$BODY_FILE")
p6b_tl_notes=$(jq -r '[.rows[] | select(.path == "/timeline")][0].notes // ""' "$BODY_FILE")
p6b_tl_dur=$(jq -r '[.rows[] | select(.path == "/timeline")][0].durationMs // 0 | floor' "$BODY_FILE")
p6b_tl_body=$(jq -r '[.rows[] | select(.path == "/timeline")][0].respBody // ""' "$BODY_FILE")
p6b_checks=$((p6b_checks + 1))
if [[ "$p6b_tl_rows" != "1" || "$p6b_tl_notes" != *"stream:sse"* || "$p6b_tl_notes" != *"frames:3"* || "$p6b_tl_body" != "id: 1"* ]] || ((p6b_tl_dur < 550)); then
	echo "FAIL  P6b A3(f): want ONE row for /timeline with notes stream:sse,frames:3, durationMs >= 550 and respBody starting with 'id: 1'; got ${p6b_tl_rows} row(s), notes '${p6b_tl_notes}', duration ${p6b_tl_dur}, body '${p6b_tl_body:0:40}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(f): one row per connection (notes '${p6b_tl_notes}', ${p6b_tl_dur}ms) with the first frame as its body"
fi

echo "== P6b A3(b): the tick is deterministic across connections =="
p6b_tk1=$(mktemp)
p6b_tk2=$(mktemp)
curl -sN --max-time 3 -o "$p6b_tk1" -H "Host: ${p6b_host}" "${BASE_URL}/ticks" || true
curl -sN --max-time 3 -o "$p6b_tk2" -H "Host: ${p6b_host}" "${BASE_URL}/ticks" || true
p6b_tk1_frames=$(grep -c '^data: ' "$p6b_tk1" || true)
p6b_tk1_head=$(grep '^data: ' "$p6b_tk1" | head -n3 || true)
p6b_tk2_head=$(grep '^data: ' "$p6b_tk2" | head -n3 || true)
p6b_tk_sym=$(grep '^data: ' "$p6b_tk1" | head -n1 | sed 's/^data: //' | jq -r '.sym // empty' || true)
p6b_checks=$((p6b_checks + 1))
if ((p6b_tk1_frames < 3)) || [[ "$p6b_tk1_head" != "$p6b_tk2_head" || -z "$p6b_tk_sym" ]]; then
	echo "FAIL  P6b A3(b): want >= 3 tick frames in 3 s and the first three identical across two connections with a generated sym; got ${p6b_tk1_frames} frame(s), sym '${p6b_tk_sym}', equal: $([[ "$p6b_tk1_head" == "$p6b_tk2_head" ]] && echo yes || echo no)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(b): ${p6b_tk1_frames} tick frames in 3 s, first three byte-identical across two connections"
fi
rm -f "$p6b_tk1" "$p6b_tk2" "$p6b_tl_out"

echo "== P6b A3(c): the session layer applies at the handshake only =="
# A13: DELETE .../session with a body that names no target is REFUSED, never
# read as "clear everything" — so the clear-all below sends NO body (the
# JSON content type alone, for the CSRF chain). The pre-A13 '{}' this block
# used to send answered 400 and left the forced 503 in place, which the
# delay half then observed as "0 frames".
p6b_force=$(jq -n '{target: {method: "GET", path: "/timeline"}, action: "status", status: 503}')
http_json POST "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/session" "$p6b_force" -H "X-CSRF-Token: ${csrf}" >/dev/null
p6b_forced=$(curl -s -o "$BODY_FILE" -D "$BODY_FILE.h" -w '%{http_code}' --max-time 5 -H "Host: ${p6b_host}" "${BASE_URL}/timeline" || true)
p6b_forced_ct=$(grep -i '^content-type:' "$BODY_FILE.h" | tr -d '\r' | head -n1 || true)
http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/session" '' -H 'Content-Type: application/json' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6b_delay=$(jq -n '{target: {method: "GET", path: "/timeline"}, action: "delay", ms: 300}')
http_json POST "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/session" "$p6b_delay" -H "X-CSRF-Token: ${csrf}" >/dev/null
p6b_d_out=$(mktemp)
p6b_t0=$(date +%s%N)
curl -sN --max-time 15 -o "$p6b_d_out" -H "Host: ${p6b_host}" "${BASE_URL}/timeline" || true
p6b_d_ms=$((($(date +%s%N) - p6b_t0) / 1000000))
http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/session" '' -H 'Content-Type: application/json' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6b_d_frames=$(grep -c '^data: ' "$p6b_d_out" || true)
rm -f "$p6b_d_out" "$BODY_FILE.h"
p6b_checks=$((p6b_checks + 1))
# Without the delay the timeline takes ~600 ms (two 300 ms gaps); with a
# 300 ms handshake delay ~900; had the delay been added per frame it would
# be ~1500. The window 850..1400 separates the two.
if [[ "$p6b_forced" != "503" || "$p6b_forced_ct" == *"text/event-stream"* || "$p6b_d_frames" != "3" ]] || ((p6b_d_ms < 850)) || ((p6b_d_ms > 1400)); then
	echo "FAIL  P6b A3(c): forced 503 must abort the handshake (got ${p6b_forced}, '${p6b_forced_ct}') and a 300 ms delay must move only the handshake (3 frames in 850..1400 ms; got ${p6b_d_frames} frames, ${p6b_d_ms}ms)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(c): forced 503 aborted the handshake; under a 300 ms delay the timeline took ${p6b_d_ms}ms (handshake delayed, frames not)"
fi

echo "== P6b A3(d): the cap is per workspace (MOCKER_STREAM_MAX_CONNS=3) =="
# (b)'s and (c)'s clients have exited, but their handlers deregister on
# their own schedule; a correct server could still hold one slot for a few
# milliseconds and refuse the third of the three below.
p6b_wait_mock_open 0 10 >/dev/null
p6b_refused_before=$(p6b_stats mock.refusedCap)
p6b_cap_pids=()
for _ in 1 2 3; do
	curl -sN --max-time 60 -H "Host: ${p6b_host}" "${BASE_URL}/ticks" >/dev/null 2>&1 &
	p6b_cap_pids+=("$!")
done
p6b_open3=$(p6b_wait_mock_open 3 10)
p6b_fourth=$(curl -s -o "$BODY_FILE" -w '%{http_code}' --max-time 5 -H "Host: ${p6b_host}" "${BASE_URL}/ticks" || true)
p6b_fourth_code=$(jq -r '.error.code // empty' "$BODY_FILE" 2>/dev/null || true)
p6b_other_status=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 -H "Host: ${p6b_other_host}" "${BASE_URL}/ticks" || true)
p6b_refused_after=$(p6b_stats mock.refusedCap)
p6b_by_ws=$(p6b_stats "mock.byWorkspace[] | select(.workspaceId == ${p6b_ws_id}) | .open")
for p in "${p6b_cap_pids[@]}"; do kill "$p" 2>/dev/null || true; done
for p in "${p6b_cap_pids[@]}"; do wait "$p" 2>/dev/null || true; done
p6b_wait_mock_open 0 10 >/dev/null
p6b_checks=$((p6b_checks + 1))
# The other workspace's curl times out at 2 s (exit 28) with status 200
# already received — that is the expected shape for a stream that does
# not end.
if [[ "$p6b_open3" != "3" || "$p6b_fourth" != "503" || "$p6b_fourth_code" != "service_unavailable" || "$p6b_refused_after" != "$((p6b_refused_before + 1))" || "$p6b_by_ws" != "3" || "$p6b_other_status" != "200" ]]; then
	echo "FAIL  P6b A3(d): want mock.open 3, the 4th 503 service_unavailable, refusedCap +1, byWorkspace 3, another workspace 200; got open ${p6b_open3}, 4th ${p6b_fourth} '${p6b_fourth_code}', refusedCap ${p6b_refused_before} -> ${p6b_refused_after}, byWorkspace '${p6b_by_ws}', other ${p6b_other_status}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(d): three streams open, the fourth refused 503 (refusedCap ${p6b_refused_before} -> ${p6b_refused_after}), another workspace still opens"
fi

echo "== P6b A3(e): the lifetime closes a tick stream (MOCKER_STREAM_MAX_LIFETIME=20) =="
p6b_t0=$(date +%s%N)
curl -sN --max-time 40 -o /dev/null -H "Host: ${p6b_host}" "${BASE_URL}/ticks" || true
p6b_life_ms=$((($(date +%s%N) - p6b_t0) / 1000000))
p6b_checks=$((p6b_checks + 1))
if ((p6b_life_ms < 19000)) || ((p6b_life_ms > 25000)); then
	echo "FAIL  P6b A3(e): the tick stream lasted ${p6b_life_ms}ms, want 19000..25000 under MOCKER_STREAM_MAX_LIFETIME=20"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(e): the tick stream was closed by the server after ${p6b_life_ms}ms"
fi

echo "== P6b A3(g)/(h): a checkpoint, an edit while a stream is open, and the rollback =="
p6b_cp_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/checkpoints" '{"label":"before the edit"}' -H "X-CSRF-Token: ${csrf}")
p6b_cp_id=$(jq -r '.id' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6b_rev_before=$(jq -r '.revision' "$BODY_FILE")
p6b_g_out=$(mktemp)
curl -sN --max-time 4 -o "$p6b_g_out" -H "Host: ${p6b_host}" "${BASE_URL}/ticks" >/dev/null 2>&1 &
p6b_g_pid=$!
sleep 0.6
p6b_edit_req=$(jq -n --argjson wid "$p6b_ws_id" --argjson eid "$p6b_tick_id" --argjson ev "$p6b_tick_ev" '{workspaceId: $wid, endpointId: $eid, method: "GET", path: "/ticks", activeStatus: 200, kind: "sse", stream: {tick: {intervalMs: 1000, event: "price", schema: {type: "object", properties: {price: {type: "number"}, sym: {type: "string"}}, required: ["price", "sym"]}}}, editVersion: $ev}')
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" update_endpoint "$p6b_edit_req")
p6b_edit_interval=$(jq -r '.result.structuredContent.endpoint.stream.tick.intervalMs' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6b_rev_after=$(jq -r '.revision' "$BODY_FILE")
wait "$p6b_g_pid" 2>/dev/null || true
p6b_g_frames=$(grep -c '^data: ' "$p6b_g_out" || true)
rm -f "$p6b_g_out"
# A fresh connection sees the new cadence: 3 s at 1000 ms is 2 or 3 frames,
# never the 5-6 a 500 ms cadence gives.
p6b_n_out=$(mktemp)
curl -sN --max-time 3 -o "$p6b_n_out" -H "Host: ${p6b_host}" "${BASE_URL}/ticks" || true
p6b_n_frames=$(grep -c '^data: ' "$p6b_n_out" || true)
rm -f "$p6b_n_out"
p6b_checks=$((p6b_checks + 1))
if [[ "$mcp_status" != "200" || "$p6b_edit_interval" != "1000" ]] || ((p6b_rev_after <= p6b_rev_before)) || ((p6b_g_frames < 6)) || ((p6b_n_frames > 3)); then
	echo "FAIL  P6b A3(g): update_endpoint (status ${mcp_status}, interval ${p6b_edit_interval}) must bump revision (${p6b_rev_before} -> ${p6b_rev_after}), leave the open stream on its 500 ms cadence (got ${p6b_g_frames} frames in 4 s, want >= 6) and give a new connection the 1000 ms cadence (got ${p6b_n_frames} in 3 s, want <= 3)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(g): revision ${p6b_rev_before} -> ${p6b_rev_after}; the open stream kept its cadence (${p6b_g_frames} frames in 4 s), a new one got the edit (${p6b_n_frames} in 3 s)"
fi
p6b_rb_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/rollback/${p6b_cp_id}" '{}' -H "X-CSRF-Token: ${csrf}")
mcp_tool "$ADMIN_HOST" "$MCP_KEY" list_endpoints "$(jq -n --argjson wid "$p6b_ws_id" '{workspaceId: $wid}')" >/dev/null
p6b_rb_interval=$(jq -r '.result.structuredContent.endpoints[] | select(.path == "/ticks") | .stream.tick.intervalMs' "$BODY_FILE")
p6b_rb_kind=$(jq -r '.result.structuredContent.endpoints[] | select(.path == "/ticks") | .kind' "$BODY_FILE")
p6b_checks=$((p6b_checks + 1))
if [[ "$p6b_cp_status" != "201" || "$p6b_rb_status" != "200" || "$p6b_rb_interval" != "500" || "$p6b_rb_kind" != "sse" ]]; then
	echo "FAIL  P6b A3(h): checkpoint ${p6b_cp_status}, rollback ${p6b_rb_status}; after the rollback /ticks must read kind sse with intervalMs 500, got kind '${p6b_rb_kind}' interval '${p6b_rb_interval}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(h): the rollback restored kind sse and intervalMs 500 through the v4 snapshot"
fi

echo "== P6b A3(i): both writers refuse by name =="
p6b_bad_admin=$(jq -n '{method: "GET", path: "/bad", kind: "sse", stream: {tick: {intervalMs: 50, schema: {type: "object"}}}}')
p6b_i_admin_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/endpoints" "$p6b_bad_admin" -H "X-CSRF-Token: ${csrf}")
p6b_i_admin_msg=$(jq -r '.error.message' "$BODY_FILE")
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_endpoint "$(jq -n --argjson wid "$p6b_ws_id" '{workspaceId: $wid, method: "GET", path: "/bad", kind: "sse", stream: {tick: {intervalMs: 50, schema: {type: "object"}}}}')")
p6b_i_mcp_msg=$(jq -r '.result.content[0].text // .error.message // ""' "$BODY_FILE")
p6b_long=$(jq -n '{method: "GET", path: "/long", kind: "sse", stream: {timeline: {frames: [range(501) | {delayMs: 0, data: .}]}}}')
p6b_i_long_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/endpoints" "$p6b_long" -H "X-CSRF-Token: ${csrf}")
p6b_i_long_msg=$(jq -r '.error.message' "$BODY_FILE")
p6b_resp=$(jq -n '{method: "GET", path: "/withresp", kind: "sse", stream: {tick: {intervalMs: 500, schema: {type: "object"}}}}')
p6b_i_resp_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/endpoints" "$(jq '.status = 200 | .body = {}' <<<"$p6b_resp")" -H "X-CSRF-Token: ${csrf}")
p6b_checks=$((p6b_checks + 1))
if [[ "$p6b_i_admin_status" != "400" || "$p6b_i_admin_msg" != *"below the floor of 100"* || "$p6b_i_mcp_msg" != *"below the floor of 100"* || "$p6b_i_long_status" != "400" || "$p6b_i_long_msg" != *"the cap is 500"* || "$p6b_i_resp_status" != "400" ]]; then
	echo "FAIL  P6b A3(i): admin ${p6b_i_admin_status} '${p6b_i_admin_msg}', MCP '${p6b_i_mcp_msg}', 501 frames ${p6b_i_long_status} '${p6b_i_long_msg}', sse with a body ${p6b_i_resp_status} — every one must be a 400 naming the limit"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(i): the tick floor, the frame cap and the no-responses rule are refused by name on both writers"
fi

echo "== P6b D13: preview_endpoint lays a draft out without saving it =="
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" preview_endpoint "$(jq -n --argjson wid "$p6b_ws_id" '{workspaceId: $wid, method: "GET", path: "/draft", kind: "sse", stream: {timeline: {frames: [{delayMs: 50, event: "a", data: 1}]}, tick: {intervalMs: 100, schema: {type: "object", properties: {v: {type: "integer"}}, required: ["v"]}}}}')")
p6b_pv_frames=$(jq -r '.result.structuredContent.frames | length' "$BODY_FILE")
p6b_pv_first=$(jq -c '.result.structuredContent.frames[0] | {atMs, event}' "$BODY_FILE")
p6b_pv_trunc=$(jq -r '.result.structuredContent.truncated' "$BODY_FILE")
p6b_pv_rate=$(jq -r '.result.structuredContent.maxBytesPerSec' "$BODY_FILE")
mcp_tool "$ADMIN_HOST" "$MCP_KEY" list_endpoints "$(jq -n --argjson wid "$p6b_ws_id" '{workspaceId: $wid}')" >/dev/null
p6b_pv_saved=$(jq -r '[.result.structuredContent.endpoints[] | select(.path == "/draft")] | length' "$BODY_FILE")
p6b_checks=$((p6b_checks + 1))
if [[ "$mcp_status" != "200" || "$p6b_pv_frames" != "50" || "$p6b_pv_first" != '{"atMs":50,"event":"a"}' || "$p6b_pv_trunc" != "true" || "$p6b_pv_saved" != "0" ]] || ((p6b_pv_rate <= 0)); then
	echo "FAIL  P6b D13: preview_endpoint: status ${mcp_status}, frames ${p6b_pv_frames} (want 50), first ${p6b_pv_first}, truncated ${p6b_pv_trunc}, maxBytesPerSec ${p6b_pv_rate}, saved rows ${p6b_pv_saved} (want 0)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b D13: preview_endpoint laid out 50 frames (first at 50 ms, event a), truncated, ${p6b_pv_rate} B/s, and saved nothing"
fi

echo "== P6b A3(j): an http endpoint created after the migration reports kind http and no stream =="
p6b_http_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/endpoints" '{"method":"GET","path":"/plain","status":200,"body":{"ok":true},"mediaType":"application/json"}' -H "X-CSRF-Token: ${csrf}")
p6b_http_kind=$(jq -r '.kind' "$BODY_FILE")
p6b_http_stream=$(jq -r 'has("stream")' "$BODY_FILE")
p6b_http_get=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${p6b_host}" "${BASE_URL}/plain")
p6b_checks=$((p6b_checks + 1))
if [[ "$p6b_http_status" != "201" || "$p6b_http_kind" != "http" || "$p6b_http_stream" != "false" || "$p6b_http_get" != "200" ]]; then
	echo "FAIL  P6b A3(j): an http endpoint: create ${p6b_http_status}, kind '${p6b_http_kind}', has stream ${p6b_http_stream}, GET ${p6b_http_get}; want 201 / http / false / 200"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b A3(j): an http endpoint still reads kind http with no stream and serves 200"
fi

rm -f "$p6b_jar"
p6b_want_checks=12
if [[ "$p6b_checks" != "$p6b_want_checks" ]]; then
	echo "FAIL  P6b acceptance (whole block): expected exactly ${p6b_want_checks} checks to have run, ${p6b_checks} actually ran"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6b acceptance (whole block): all ${p6b_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# P6c (decisions.md mocker-p6c-live-conns, §3 A3): the live-connection
# surface — list, close, push — over the P6b tick endpoints that are still
# there (workspace $p6b_ws_id on $p6b_host, the cap workspace $p6b_other_id
# on $p6b_other_host), through the three MCP tools. Same .env as P6b:
# MOCKER_STREAM_MAX_CONNS=3, MOCKER_STREAM_FRAME_TIMEOUT=4 (so a push waits
# up to 8 s and a close is observed within FrameTimeout + 1).
# --------------------------------------------------------------------------
echo "== P6c: the live-connection surface =="
p6c_checks=0
p6b_wait_mock_open 0 10 >/dev/null

p6c_tool() {
	# $1 tool, $2 arguments (JSON); prints the HTTP status, leaves the
	# envelope in BODY_FILE. A tool error is a 200 whose result carries
	# isError and the message in content[0].text.
	mcp_tool "$ADMIN_HOST" "$MCP_KEY" "$1" "$2"
}
p6c_tool_text() { jq -r '.result.content[0].text // .error.message // ""' "$BODY_FILE"; }
p6c_tool_err() { jq -r '.result.isError // false' "$BODY_FILE"; }

p6c_list() {
	# $1 workspace id; leaves structuredContent in p6c_list_json.
	p6c_tool list_stream_connections "$(jq -n --argjson wid "$1" '{workspaceId: $wid}')" >/dev/null
	p6c_list_json=$(jq -c '.result.structuredContent' "$BODY_FILE")
}

# Three readers: two on workspace A's tick route, one on workspace B's.
# --max-time is a backstop; every clause below ends them itself.
p6c_f1=$(mktemp)
p6c_f2=$(mktemp)
p6c_f3=$(mktemp)
curl -sN --max-time 60 -H "Host: ${p6b_host}" "${BASE_URL}/ticks" >"$p6c_f1" 2>/dev/null &
p6c_p1=$!
curl -sN --max-time 60 -H "Host: ${p6b_host}" "${BASE_URL}/ticks" >"$p6c_f2" 2>/dev/null &
p6c_p2=$!
curl -sN --max-time 60 -H "Host: ${p6b_other_host}" "${BASE_URL}/ticks" >"$p6c_f3" 2>/dev/null &
p6c_p3=$!
p6b_wait_mock_open 3 10 >/dev/null
sleep 1.2

echo "== P6c A3(a): list is per workspace, with the cap beside the rows =="
p6c_list "$p6b_ws_id"
p6c_a_open=$(jq -r '.open' <<<"$p6c_list_json")
p6c_a_cap=$(jq -r '.cap' <<<"$p6c_list_json")
p6c_a_n=$(jq -r '.connections | length' <<<"$p6c_list_json")
p6c_a_ids=$(jq -r '[.connections[].id] | sort | unique | length' <<<"$p6c_list_json")
p6c_a_shape=$(jq -r --argjson eid "$p6b_tick_id" '[.connections[] | (.endpointId == $eid) and (.path == "/ticks") and (.kind == "sse") and (.frames >= 1) and (.pushed == 0) and (.remoteAddr != "") and (.openedAt != "")] | all' <<<"$p6c_list_json")
p6c_id1=$(jq -r '[.connections[].id] | min' <<<"$p6c_list_json")
p6c_id2=$(jq -r '[.connections[].id] | max' <<<"$p6c_list_json")
p6c_list "$p6b_other_id"
p6c_b_n=$(jq -r '.connections | length' <<<"$p6c_list_json")
p6c_b_id=$(jq -r '.connections[0].id' <<<"$p6c_list_json")
p6c_checks=$((p6c_checks + 1))
if [[ "$p6c_a_open" != "2" || "$p6c_a_cap" != "3" || "$p6c_a_n" != "2" || "$p6c_a_ids" != "2" || "$p6c_a_shape" != "true" || "$p6c_b_n" != "1" || "$p6c_b_id" == "$p6c_id1" || "$p6c_b_id" == "$p6c_id2" ]]; then
	echo "FAIL  P6c A3(a): want A open 2 cap 3 with two distinct rows on endpoint ${p6b_tick_id} (/ticks, sse, frames>=1, pushed 0, a peer) and B one row with a third id; got A open ${p6c_a_open} cap ${p6c_a_cap} rows ${p6c_a_n} distinct ${p6c_a_ids} shape ${p6c_a_shape}, B rows ${p6c_b_n} id ${p6c_b_id} (A ids ${p6c_id1},${p6c_id2})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6c A3(a): A lists its two connections (ids ${p6c_id1},${p6c_id2}) under cap 3; B lists one with another id (${p6c_b_id})"
fi

echo "== P6c A3(b): a push reaches ONE connection under the next ordinal =="
p6c_push_status=$(p6c_tool push_stream_frame "$(jq -n --argjson wid "$p6b_ws_id" --argjson cid "$p6c_id1" '{workspaceId: $wid, connectionId: $cid, event: "op", data: {x: 1}}')")
p6c_push_err=$(p6c_tool_err)
p6c_k=$(jq -r '.result.structuredContent.frameId // 0' "$BODY_FILE")
sleep 1
# Which reader got it: exactly one of the two files carries "event: op".
p6c_op1=$(grep -c '^event: op$' "$p6c_f1" || true)
p6c_op2=$(grep -c '^event: op$' "$p6c_f2" || true)
if [[ "$p6c_op1" == "1" && "$p6c_op2" == "0" ]]; then
	p6c_hit="$p6c_f1"
	p6c_hit_pid=$p6c_p1
	p6c_miss_pid=$p6c_p2
elif [[ "$p6c_op1" == "0" && "$p6c_op2" == "1" ]]; then
	p6c_hit="$p6c_f2"
	p6c_hit_pid=$p6c_p2
	p6c_miss_pid=$p6c_p1
else
	p6c_hit=""
	p6c_hit_pid=""
	p6c_miss_pid=""
fi
p6c_seq_ok=false
p6c_op_frame=""
if [[ -n "$p6c_hit" ]]; then
	# The captured id: sequence is 1..N with no gap, and frame k is the op.
	p6c_ids=$(grep '^id: ' "$p6c_hit" | sed 's/^id: //' | tr '\n' ' ')
	p6c_n=$(wc -w <<<"$p6c_ids")
	p6c_want=$(seq -s ' ' 1 "$p6c_n")
	[[ "$p6c_ids" == "$p6c_want " ]] && ((p6c_k >= 1)) && ((p6c_k <= p6c_n)) && p6c_seq_ok=true
	p6c_op_frame=$(awk -v k="id: ${p6c_k}" 'BEGIN{RS=""} $0 ~ "^"k"\n" {print; exit}' "$p6c_hit" | tr '\n' '|')
fi
p6c_list "$p6b_ws_id"
p6c_pushed1=$(jq -r --argjson id "$p6c_id1" '[.connections[] | select(.id == $id)][0].pushed' <<<"$p6c_list_json")
p6c_pushed2=$(jq -r --argjson id "$p6c_id2" '[.connections[] | select(.id == $id)][0].pushed' <<<"$p6c_list_json")
p6c_checks=$((p6c_checks + 1))
if [[ "$p6c_push_status" != "200" || "$p6c_push_err" != "false" || -z "$p6c_hit" || "$p6c_seq_ok" != "true" || "$p6c_op_frame" != "id: ${p6c_k}|event: op|data: {\"x\":1}|" || "$p6c_pushed1" != "1" || "$p6c_pushed2" != "0" ]]; then
	echo "FAIL  P6c A3(b): push status ${p6c_push_status} err ${p6c_push_err} frameId ${p6c_k}; op frames in readers ${p6c_op1}/${p6c_op2} (want exactly one); sequence ok ${p6c_seq_ok} (ids '${p6c_ids:-}'); frame '${p6c_op_frame}'; pushed ${p6c_pushed1}/${p6c_pushed2} (want 1/0)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6c A3(b): frame id ${p6c_k} (event op) reached connection ${p6c_id1} only, in sequence; list shows pushed 1/0"
fi

echo "== P6c A3(c): a close ends one connection, a second close is 404, the row says closed:admin =="
p6c_t0=$(date +%s%N)
p6c_close_status=$(p6c_tool close_stream_connection "$(jq -n --argjson wid "$p6b_ws_id" --argjson cid "$p6c_id1" '{workspaceId: $wid, connectionId: $cid}')")
p6c_close_err=$(p6c_tool_err)
p6c_closed=$(jq -r '.result.structuredContent.closed // false' "$BODY_FILE")
p6c_hit_exit=""
if [[ -n "$p6c_hit_pid" ]]; then
	for _ in $(seq 1 50); do
		if ! kill -0 "$p6c_hit_pid" 2>/dev/null; then break; fi
		sleep 0.1
	done
	wait "$p6c_hit_pid" && p6c_hit_exit=0 || p6c_hit_exit=$?
fi
p6c_close_ms=$((($(date +%s%N) - p6c_t0) / 1000000))
p6c_miss_alive=false
[[ -n "$p6c_miss_pid" ]] && kill -0 "$p6c_miss_pid" 2>/dev/null && p6c_miss_alive=true
p6c_tool close_stream_connection "$(jq -n --argjson wid "$p6b_ws_id" --argjson cid "$p6c_id1" '{workspaceId: $wid, connectionId: $cid}')" >/dev/null
p6c_close2_err=$(p6c_tool_err)
p6c_close2_text=$(p6c_tool_text)
p6c_list "$p6b_ws_id"
p6c_left=$(jq -r '[.connections[].id] | join(",")' <<<"$p6c_list_json")
sleep 1.5
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6b_ws_id}/traffic?limit=50" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6c_row_notes=$(jq -r '[.rows[] | select(.path == "/ticks" and (.notes // "" | contains("closed:admin")))][0].notes // ""' "$BODY_FILE")
p6c_tl_notes=$(jq -r '[.rows[] | select(.path == "/timeline")][0].notes // ""' "$BODY_FILE")
p6c_checks=$((p6c_checks + 1))
if [[ "$p6c_close_status" != "200" || "$p6c_close_err" != "false" || "$p6c_closed" != "true" || "$p6c_hit_exit" != "0" || "$p6c_miss_alive" != "true" || "$p6c_close2_err" != "true" || "$p6c_close2_text" != *"no live connection with this id"* || "$p6c_left" != "$p6c_id2" || "$p6c_row_notes" != *"stream:sse"* || "$p6c_row_notes" != *"pushed:1"* || "$p6c_tl_notes" == *"closed:admin"* || "$p6c_tl_notes" == *"pushed:"* ]] || ((p6c_close_ms > 5000)); then
	echo "FAIL  P6c A3(c): close ${p6c_close_status}/err ${p6c_close_err}/closed ${p6c_closed}; reader exit '${p6c_hit_exit}' after ${p6c_close_ms}ms (want 0 within 5000); other reader alive ${p6c_miss_alive}; second close err ${p6c_close2_err} '${p6c_close2_text}'; left '${p6c_left}' (want ${p6c_id2}); row notes '${p6c_row_notes}' (want stream:sse,pushed:1,closed:admin); timeline notes '${p6c_tl_notes}' (want neither token)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6c A3(c): connection ${p6c_id1} closed in ${p6c_close_ms}ms (reader exit 0, the other still open), second close 404, row notes '${p6c_row_notes}'"
fi

echo "== P6c A3(d): no cross-workspace address =="
p6c_tool push_stream_frame "$(jq -n --argjson wid "$p6b_other_id" --argjson cid "$p6c_id2" '{workspaceId: $wid, connectionId: $cid, data: 1}')" >/dev/null
p6c_x_push_err=$(p6c_tool_err)
p6c_x_push_text=$(p6c_tool_text)
p6c_tool close_stream_connection "$(jq -n --argjson wid "$p6b_other_id" --argjson cid "$p6c_id2" '{workspaceId: $wid, connectionId: $cid}')" >/dev/null
p6c_x_close_err=$(p6c_tool_err)
p6c_x_close_text=$(p6c_tool_text)
p6c_x_alive=false
kill -0 "$p6c_miss_pid" 2>/dev/null && p6c_x_alive=true
p6c_checks=$((p6c_checks + 1))
if [[ "$p6c_x_push_err" != "true" || "$p6c_x_push_text" != *"no live connection with this id"* || "$p6c_x_close_err" != "true" || "$p6c_x_close_text" != *"no live connection with this id"* || "$p6c_x_alive" != "true" ]]; then
	echo "FAIL  P6c A3(d): A's connection ${p6c_id2} addressed through B must answer 404 on push ('${p6c_x_push_text}') and close ('${p6c_x_close_text}') and stay open (alive ${p6c_x_alive})"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6c A3(d): A's connection is not reachable through B's workspace (404 on push and close, still open)"
fi

echo "== P6c A3(e): the push validator is the endpoint writer's =="
p6c_tool push_stream_frame "$(jq -n --argjson wid "$p6b_ws_id" --argjson cid "$p6c_id2" '{workspaceId: $wid, connectionId: $cid, event: "bad\nname", data: 1}')" >/dev/null
p6c_v_err=$(p6c_tool_err)
p6c_v_text=$(p6c_tool_text)
p6c_tool create_endpoint "$(jq -n --argjson wid "$p6b_ws_id" '{workspaceId: $wid, method: "GET", path: "/badevent", kind: "sse", stream: {timeline: {frames: [{delayMs: 0, event: "bad\nname", data: 1}]}}}')" >/dev/null
p6c_w_text=$(p6c_tool_text)
sleep 0.5
p6c_bad_frames=$({ grep -c 'bad' "$p6c_f1" "$p6c_f2" || true; } | awk -F: '{s+=$2} END{print s+0}')
p6c_checks=$((p6c_checks + 1))
if [[ "$p6c_v_err" != "true" || "$p6c_v_text" != *"must not contain a line break or NUL"* || "$p6c_w_text" != *"must not contain a line break or NUL"* || "$p6c_bad_frames" != "0" ]]; then
	echo "FAIL  P6c A3(e): a line break in event must be refused by push ('${p6c_v_text}') with the words create_endpoint uses ('${p6c_w_text}'), and reach no reader (${p6c_bad_frames} frames)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6c A3(e): push and create_endpoint refuse the event name with the same words; nothing reached the wire"
fi

echo "== P6c A3(f): the listing agrees with the cap =="
curl -sN --max-time 60 -H "Host: ${p6b_host}" "${BASE_URL}/ticks" >/dev/null 2>&1 &
p6c_p4=$!
curl -sN --max-time 60 -H "Host: ${p6b_host}" "${BASE_URL}/ticks" >/dev/null 2>&1 &
p6c_p5=$!
p6b_wait_mock_open 4 10 >/dev/null
p6c_list "$p6b_ws_id"
p6c_f_open=$(jq -r '.open' <<<"$p6c_list_json")
p6c_f_cap=$(jq -r '.cap' <<<"$p6c_list_json")
p6c_f_n=$(jq -r '.connections | length' <<<"$p6c_list_json")
p6c_fourth=$(curl -s -o "$BODY_FILE" -w '%{http_code}' --max-time 5 -H "Host: ${p6b_host}" "${BASE_URL}/ticks" || true)
for p in "$p6c_miss_pid" "$p6c_p3" "$p6c_p4" "$p6c_p5"; do kill "$p" 2>/dev/null || true; done
for p in "$p6c_miss_pid" "$p6c_p3" "$p6c_p4" "$p6c_p5"; do wait "$p" 2>/dev/null || true; done
p6c_after=$(p6b_wait_mock_open 0 10)
p6c_list "$p6b_ws_id"
p6c_f_left=$(jq -r '.connections | length' <<<"$p6c_list_json")
rm -f "$p6c_f1" "$p6c_f2" "$p6c_f3"
p6c_checks=$((p6c_checks + 1))
if [[ "$p6c_f_open" != "3" || "$p6c_f_cap" != "3" || "$p6c_f_n" != "3" || "$p6c_fourth" != "503" || "$p6c_after" != "0" || "$p6c_f_left" != "0" ]]; then
	echo "FAIL  P6c A3(f): with three readers want open 3 cap 3 rows 3 and a fourth handshake 503, then 0 after they leave; got open ${p6c_f_open} cap ${p6c_f_cap} rows ${p6c_f_n} fourth ${p6c_fourth} after ${p6c_after}/${p6c_f_left}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6c A3(f): the listing reads open 3 / cap 3 while the fourth handshake is refused, and empties when the readers leave"
fi

p6c_want_checks=6
if [[ "$p6c_checks" != "$p6c_want_checks" ]]; then
	echo "FAIL  P6c acceptance (whole block): expected exactly ${p6c_want_checks} checks to have run, ${p6c_checks} actually ran"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6c acceptance (whole block): all ${p6c_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# P6d (decisions.md mocker-p6d-websocket, §3 A3): WebSocket mock endpoints —
# echo, reactive rules, a rule that closes, the timeline over WebSocket, the
# three variables at NON-DEFAULT values (MAX_FRAME=4kb, SEND_BUDGET=1kb,
# ORIGINS=https://allowed.example, written into .env at the top of the file
# and read back from the container), the inbound half of the traffic row,
# the P6c surface on a ws connection, the admin CSRF predicate, both writers'
# refusals. The client is scripts/wsclient.py (python3, standard library
# only): curl can only receive WebSocket frames and the image is distroless.
# --------------------------------------------------------------------------
echo "== P6d: WebSocket mock endpoints =="
p6d_checks=0
p6d_port=${BASE_URL##*:}
p6d_client="python3 scripts/wsclient.py"

p6d_ws() {
	# $1 host, $2 path, then any client flags; prints the client's output.
	local host=$1 path=$2
	shift 2
	# shellcheck disable=SC2086
	$p6d_client --url "ws://127.0.0.1:${p6d_port}${path}" --host "$host" --timeout 6 "$@" 2>&1 || true
}
p6d_frames() { grep -E '^[0-9]+ (text|binary) ' <<<"$1" | sed -E 's/^[0-9]+ (text|binary) //' || true; }
p6d_close() { grep -E '^close ' <<<"$1" | tail -n1 | awk '{print $2}' || true; }
p6d_status() { grep -E '^status ' <<<"$1" | head -n1 | awk '{print $2}' || true; }

echo "== P6d pre-flight: the client compiles, and the three variables reach the container =="
p6d_container=$(docker compose ps -q mocker)
p6d_env=$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$p6d_container" | grep -E '^MOCKER_STREAM_(MAX_FRAME|SEND_BUDGET|ORIGINS)=' | sort | tr '\n' ' ')
p6d_py=$(python3 -m py_compile "scripts/wsclient.py" 2>&1 && echo ok || echo fail)
p6d_checks=$((p6d_checks + 1))
if [[ "$p6d_env" != "MOCKER_STREAM_MAX_FRAME=4kb MOCKER_STREAM_ORIGINS=https://allowed.example MOCKER_STREAM_SEND_BUDGET=1kb " || "$p6d_py" != "ok" ]]; then
	echo "FAIL  P6d pre-flight: want MAX_FRAME=4kb SEND_BUDGET=1kb ORIGINS=https://allowed.example in the container and a compiling client, got '${p6d_env}' / ${p6d_py}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d pre-flight: the container carries the three non-default values; wsclient.py compiles"
fi

echo "== P6d: a workspace and three ws endpoints through create_endpoint =="
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_workspace '{"name":"p6d"}')
p6d_ws_id=$(jq -r '.result.structuredContent.workspace.id // .result.structuredContent.id' "$BODY_FILE")
p6d_slug=$(jq -r '.result.structuredContent.workspace.slug // .result.structuredContent.slug' "$BODY_FILE")
if [[ "$mcp_status" != "200" || -z "$p6d_ws_id" || "$p6d_ws_id" == "null" ]]; then
	echo "FAIL  P6d setup: create_workspace: status ${mcp_status}: $(cat "$BODY_FILE")"
	exit 1
fi
p6d_host="${p6d_slug}.${WORKSPACE_HOST_BASE}"
p6d_chat_req=$(jq -n --argjson wid "$p6d_ws_id" '{workspaceId: $wid, method: "GET", path: "/chat", kind: "ws", stream: {echo: true, reactive: [{when: [{in: "body", name: "op", op: "equals", value: "ping"}], data: {op: "pong"}}]}}')
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_endpoint "$p6d_chat_req")
p6d_chat_id=$(jq -r '.result.structuredContent.endpoint.id' "$BODY_FILE")
p6d_chat_kind=$(jq -r '.result.structuredContent.endpoint.kind' "$BODY_FILE")
if [[ "$mcp_status" != "200" || "$p6d_chat_kind" != "ws" ]]; then
	echo "FAIL  P6d setup: create_endpoint (/chat): status ${mcp_status} kind '${p6d_chat_kind}': $(cat "$BODY_FILE")"
	exit 1
fi
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_endpoint "$(jq -n --argjson wid "$p6d_ws_id" '{workspaceId: $wid, method: "GET", path: "/kick", kind: "ws", stream: {reactive: [{when: [{in: "body", name: "op", op: "equals", value: "bye"}], data: {ok: true}, close: {code: 4001, reason: "bye"}}]}}')")
if [[ "$mcp_status" != "200" ]]; then
	echo "FAIL  P6d setup: create_endpoint (/kick): status ${mcp_status}: $(cat "$BODY_FILE")"
	exit 1
fi
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_endpoint "$(jq -n --argjson wid "$p6d_ws_id" '{workspaceId: $wid, method: "GET", path: "/tl", kind: "ws", stream: {timeline: {frames: [{delayMs: 0, data: {n: 1}}, {delayMs: 300, data: {n: 2}}, {delayMs: 300, data: {n: 3}}]}}}')")
if [[ "$mcp_status" != "200" ]]; then
	echo "FAIL  P6d setup: create_endpoint (/tl): status ${mcp_status}: $(cat "$BODY_FILE")"
	exit 1
fi

echo "== P6d A3(a): echo and reactive, in send order =="
p6d_a=$(p6d_ws "$p6d_host" /chat --send '{"op":"ping"}' --send '{"x":1}' --send hello --expect 3)
p6d_a_frames=$(p6d_frames "$p6d_a" | tr '\n' '|')
p6d_checks=$((p6d_checks + 1))
if [[ "$(p6d_status "$p6d_a")" != "101" || "$p6d_a_frames" != '{"op":"pong"}|{"x":1}|hello|' ]]; then
	echo "FAIL  P6d A3(a): want 101 then frames {\"op\":\"pong\"}|{\"x\":1}|hello| ; got status $(p6d_status "$p6d_a") frames '${p6d_a_frames}' (full: $(tr '\n' ' ' <<<"$p6d_a"))"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(a): the rule answered ping with pong, echo returned the unmatched JSON and the non-JSON text, in order"
fi

echo "== P6d A3(b): a rule closes with its code; the row carries the inbound half =="
p6d_b=$(p6d_ws "$p6d_host" /kick --send '{"op":"bye"}')
p6d_b_frames=$(p6d_frames "$p6d_b" | tr '\n' '|')
p6d_b_close=$(p6d_close "$p6d_b")
sleep 1.5
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6d_ws_id}/traffic?limit=20" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6d_b_notes=$(jq -r '[.rows[] | select(.path == "/kick")][0].notes // ""' "$BODY_FILE")
p6d_b_req=$(jq -r '[.rows[] | select(.path == "/kick")][0].reqBody // ""' "$BODY_FILE")
p6d_checks=$((p6d_checks + 1))
if [[ "$p6d_b_frames" != '{"ok":true}|' || "$p6d_b_close" != "4001" || "$p6d_b_notes" != *"stream:ws"* || "$p6d_b_notes" != *"frames:1"* || "$p6d_b_notes" != *"frames_in:1"* || "$p6d_b_notes" != *"close:4001"* || "$p6d_b_req" != '{"op":"bye"}' ]]; then
	echo "FAIL  P6d A3(b): want data {\"ok\":true} then close 4001 and a row with stream:ws,frames:1,frames_in:1,close:4001 and reqBody {\"op\":\"bye\"}; got frames '${p6d_b_frames}' close '${p6d_b_close}' notes '${p6d_b_notes}' reqBody '${p6d_b_req}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(b): the data frame went out before close 4001; the row reads '${p6d_b_notes}' with the first inbound frame"
fi

echo "== P6d A3(c): the timeline over WebSocket, then close 1000 =="
p6d_c=$(p6d_ws "$p6d_host" /tl)
p6d_c_frames=$(p6d_frames "$p6d_c" | tr '\n' '|')
p6d_c_t2=$(grep -E '^[0-9]+ text \{"n":2\}' <<<"$p6d_c" | awk '{print $1}' || true)
p6d_checks=$((p6d_checks + 1))
if [[ "$p6d_c_frames" != '{"n":1}|{"n":2}|{"n":3}|' || "$(p6d_close "$p6d_c")" != "1000" ]] || ((${p6d_c_t2:-0} < 250)); then
	echo "FAIL  P6d A3(c): want the three frames in order (second at >= 250 ms) then close 1000; got '${p6d_c_frames}' second at ${p6d_c_t2:-?} ms close '$(p6d_close "$p6d_c")'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(c): three timeline frames (second at ${p6d_c_t2} ms), then close 1000"
fi

echo "== P6d A3(d): a 5000-byte inbound frame closes 1009 under MAX_FRAME=4kb =="
p6d_d=$(p6d_ws "$p6d_host" /chat --burst 1:5000)
sleep 1.5
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6d_ws_id}/traffic?limit=20" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6d_d_notes=$(jq -r '[.rows[] | select(.path == "/chat" and (.notes // "" | contains("close:1009")))][0].notes // ""' "$BODY_FILE")
p6d_checks=$((p6d_checks + 1))
if [[ "$(p6d_close "$p6d_d")" != "1009" || -z "$p6d_d_notes" ]]; then
	echo "FAIL  P6d A3(d): want close 1009 and a row with close:1009; got close '$(p6d_close "$p6d_d")' notes '${p6d_d_notes}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(d): the oversized frame closed the connection with 1009 (row: '${p6d_d_notes}')"
fi

echo "== P6d A3(e): ORIGINS refuses before the upgrade; a missing Origin is allowed =="
p6d_e_evil=$(p6d_ws "$p6d_host" /chat --origin https://evil.example)
p6d_e_ok=$(p6d_ws "$p6d_host" /chat --origin https://allowed.example --send '{"a":1}' --expect 1)
p6d_e_none=$(p6d_ws "$p6d_host" /chat --send '{"a":1}' --expect 1)
p6d_checks=$((p6d_checks + 1))
if [[ "$(p6d_status "$p6d_e_evil")" != "403" || "$p6d_e_evil" != *"origin_refused"* || "$(p6d_status "$p6d_e_ok")" != "101" || "$(p6d_status "$p6d_e_none")" != "101" ]]; then
	echo "FAIL  P6d A3(e): evil origin -> '$(head -n1 <<<"$p6d_e_evil")' (want 403 origin_refused); allowed -> $(p6d_status "$p6d_e_ok"); none -> $(p6d_status "$p6d_e_none") (want 101 both)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(e): https://evil.example refused 403 origin_refused; the listed origin and no Origin both upgrade"
fi

echo "== P6d A3(f): the send budget drops and counts, and {\"\$gap\": N} markers announce it =="
# 20000 echoes of 300 bytes: each fits the 1kb budget (a reply LARGER than
# the whole budget can never be queued — the first draft of this clause sent
# 3000-byte frames and observed nothing, correctly), the reader outruns the
# loop's socket writes, and the client does not read for 1.5 s, so drops
# happen in stretches; every stretch followed by a write yields one marker,
# drops after the last write reach the row only. The client sends the burst
# from a second thread and reads from 1.5 s on WHILE it is still sending:
# the loop's echo write is then never blocked past MOCKER_STREAM_FRAME_
# TIMEOUT (4 s here), which on a slow CI runner it was when the whole 6 MB
# had to leave the client before the first read — 1001, zero echoes.
# --idle-ms ends the read once the burst is out and nothing has arrived for
# 1.5 s, well inside MOCKER_STREAM_MAX_LIFETIME=20; --timeout 15 is the
# backstop under it.
p6d_f=$(p6d_ws "$p6d_host" /chat --burst 20000:300 --read-after-ms 1500 --timeout 15 --idle-ms 1500)
p6d_f_gaps=$(grep -cE '^[0-9]+ text \{"\$gap":' <<<"$p6d_f" || true)
# `|| true` on the grep: with no marker at all it exits 1, and under
# pipefail + set -e that ended the whole smoke with no FAIL line (seen on
# the first CI run); the awk still prints 0 for an empty input.
p6d_f_gap_sum=$({ grep -E '^[0-9]+ text \{"\$gap":' <<<"$p6d_f" || true; } | sed -E 's/.*"\$gap":([0-9]+).*/\1/' | awk '{s+=$1} END{print s+0}')
p6d_f_echoes=$(grep -cE '^[0-9]+ text x+$' <<<"$p6d_f" || true)
sleep 1.5
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6d_ws_id}/traffic?limit=30" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6d_f_notes=$(jq -r '[.rows[] | select(.path == "/chat" and (.notes // "" | contains("replies_dropped:")))][0].notes // ""' "$BODY_FILE")
p6d_f_dropped=$(sed -nE 's/.*replies_dropped:([0-9]+).*/\1/p' <<<"$p6d_f_notes")
p6d_checks=$((p6d_checks + 1))
# An empty $p6d_f_dropped is a FAIL, not an arithmetic error: the `:-0`
# keeps the two comparisons that read it from aborting the script.
if ((p6d_f_gaps < 1)) || ((p6d_f_gap_sum < 1)) || [[ -z "$p6d_f_dropped" ]] || ((p6d_f_gap_sum > ${p6d_f_dropped:-0})) || ((p6d_f_echoes + ${p6d_f_dropped:-0} != 20000)); then
	echo "FAIL  P6d A3(f): want >= 1 gap marker, markers' N summing to <= the row's replies_dropped, and echoes + replies_dropped = 20000; got markers ${p6d_f_gaps} sum ${p6d_f_gap_sum} echoes ${p6d_f_echoes} notes '${p6d_f_notes}' (client tail: $(tail -n2 <<<"$p6d_f" | tr '\n' ' '))"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(f): ${p6d_f_dropped} replies dropped under the 1kb budget (${p6d_f_gaps} marker(s) announcing ${p6d_f_gap_sum}), ${p6d_f_echoes} echoes delivered"
fi

echo "== P6d A3(g): the first inbound frame is stored redacted by field name =="
p6d_g=$(p6d_ws "$p6d_host" /chat --send '{"op":"x","token":"s3cr3t"}' --expect 1)
sleep 1.5
http_json GET "$ADMIN_HOST" "/api/workspaces/${p6d_ws_id}/traffic?limit=40" '' -H "X-CSRF-Token: ${csrf}" >/dev/null
p6d_g_req=$(jq -r '[.rows[] | select(.path == "/chat" and ((.reqBody // "") | contains("\"op\":\"x\"")))][0].reqBody // ""' "$BODY_FILE")
p6d_checks=$((p6d_checks + 1))
if [[ "$(p6d_status "$p6d_g")" != "101" || -z "$p6d_g_req" || "$p6d_g_req" == *"s3cr3t"* || "$p6d_g_req" != *'"token"'* ]]; then
	echo "FAIL  P6d A3(g): the row's reqBody must be the first inbound frame with token redacted; got '${p6d_g_req}'"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(g): reqBody '${p6d_g_req}' — the token is redacted, the frame is kept"
fi

echo "== P6d A3(h): the P6c surface on a ws connection: list, push, event refused, close 1001 =="
p6d_h_out=$(mktemp)
$p6d_client --url "ws://127.0.0.1:${p6d_port}/chat" --host "$p6d_host" --send '{"a":1}' --timeout 8 >"$p6d_h_out" 2>&1 &
p6d_h_pid=$!
p6b_wait_mock_open 1 10 >/dev/null
sleep 0.5
mcp_tool "$ADMIN_HOST" "$MCP_KEY" list_stream_connections "$(jq -n --argjson wid "$p6d_ws_id" '{workspaceId: $wid}')" >/dev/null
p6d_h_kind=$(jq -r '.result.structuredContent.connections[0].kind' "$BODY_FILE")
p6d_h_in=$(jq -r '.result.structuredContent.connections[0].framesIn' "$BODY_FILE")
p6d_h_cid=$(jq -r '.result.structuredContent.connections[0].id' "$BODY_FILE")
mcp_tool "$ADMIN_HOST" "$MCP_KEY" push_stream_frame "$(jq -n --argjson wid "$p6d_ws_id" --argjson cid "$p6d_h_cid" '{workspaceId: $wid, connectionId: $cid, data: {srv: 1}}')" >/dev/null
p6d_h_push=$(jq -r '.result.isError // false' "$BODY_FILE")
mcp_tool "$ADMIN_HOST" "$MCP_KEY" push_stream_frame "$(jq -n --argjson wid "$p6d_ws_id" --argjson cid "$p6d_h_cid" '{workspaceId: $wid, connectionId: $cid, event: "e", data: 1}')" >/dev/null
p6d_h_ev_err=$(jq -r '.result.isError // false' "$BODY_FILE")
p6d_h_ev_text=$(jq -r '.result.content[0].text // ""' "$BODY_FILE")
mcp_tool "$ADMIN_HOST" "$MCP_KEY" close_stream_connection "$(jq -n --argjson wid "$p6d_ws_id" --argjson cid "$p6d_h_cid" '{workspaceId: $wid, connectionId: $cid}')" >/dev/null
p6d_h_close=$(jq -r '.result.structuredContent.closed // false' "$BODY_FILE")
wait "$p6d_h_pid" 2>/dev/null || true
p6d_h_frames=$(p6d_frames "$(cat "$p6d_h_out")" | tr '\n' '|')
p6d_h_code=$(p6d_close "$(cat "$p6d_h_out")")
rm -f "$p6d_h_out"
p6d_checks=$((p6d_checks + 1))
if [[ "$p6d_h_kind" != "ws" || "$p6d_h_in" != "1" || "$p6d_h_push" != "false" || "$p6d_h_ev_err" != "true" || "$p6d_h_ev_text" != *"event"* || "$p6d_h_close" != "true" || "$p6d_h_frames" != '{"a":1}|{"srv":1}|' || "$p6d_h_code" != "1001" ]]; then
	echo "FAIL  P6d A3(h): list kind '${p6d_h_kind}' framesIn '${p6d_h_in}'; push err ${p6d_h_push}; event push err ${p6d_h_ev_err} '${p6d_h_ev_text}'; close ${p6d_h_close}; client frames '${p6d_h_frames}' close '${p6d_h_code}' (want ws/1/false/true/true/{\"a\":1}|{\"srv\":1}|/1001)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(h): listed as ws with framesIn 1; the push arrived as a text frame; an event was refused by name; close ended it with 1001"
fi

echo "== P6d A3(i): a GET with Upgrade: websocket on the admin host is state-changing for the CSRF guard =="
p6d_i_plain=$(http_json GET "$ADMIN_HOST" /api/me '')
p6d_i_up=$(http_json GET "$ADMIN_HOST" /api/me '' -H 'Connection: Upgrade' -H 'Upgrade: websocket')
p6d_checks=$((p6d_checks + 1))
if [[ "$p6d_i_plain" != "200" || ("$p6d_i_up" != "415" && "$p6d_i_up" != "403") ]]; then
	echo "FAIL  P6d A3(i): plain GET /api/me ${p6d_i_plain} (want 200); with Connection: Upgrade + Upgrade: websocket ${p6d_i_up} (want the CSRF chain's refusal: 415, the first check a handshake fails, or 403)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(i): the plain GET passes (200); the upgrade GET is refused by the CSRF chain (${p6d_i_up})"
fi

echo "== P6d A3(j): both writers refuse by name =="
p6d_j_sse=$(jq -n '{method: "GET", path: "/bad1", kind: "sse", stream: {tick: {intervalMs: 500, schema: {type: "object"}}, echo: true}}')
p6d_j_admin=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6d_ws_id}/endpoints" "$p6d_j_sse" -H "X-CSRF-Token: ${csrf}")
p6d_j_admin_msg=$(jq -r '.error.message' "$BODY_FILE")
mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_endpoint "$(jq -n --argjson wid "$p6d_ws_id" '{workspaceId: $wid, method: "GET", path: "/bad1", kind: "sse", stream: {tick: {intervalMs: 500, schema: {type: "object"}}, echo: true}}')" >/dev/null
p6d_j_mcp_msg=$(jq -r '.result.content[0].text // .error.message // ""' "$BODY_FILE")
p6d_j_code=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6d_ws_id}/endpoints" "$(jq -n '{method: "GET", path: "/bad2", kind: "ws", stream: {reactive: [{when: [{in: "body", name: "a", op: "exists"}], close: {code: 1005}}]}}')" -H "X-CSRF-Token: ${csrf}")
p6d_j_code_msg=$(jq -r '.error.message' "$BODY_FILE")
p6d_j_empty=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p6d_ws_id}/endpoints" "$(jq -n '{method: "GET", path: "/bad3", kind: "ws", stream: {reactive: [{when: [{in: "body", name: "a", op: "exists"}]}]}}')" -H "X-CSRF-Token: ${csrf}")
p6d_checks=$((p6d_checks + 1))
if [[ "$p6d_j_admin" != "400" || "$p6d_j_admin_msg" != *"echo has no meaning on kind"* || "$p6d_j_mcp_msg" != *"echo has no meaning on kind"* || "$p6d_j_code" != "400" || "$p6d_j_code_msg" != *"4000..4999"* || "$p6d_j_empty" != "400" ]]; then
	echo "FAIL  P6d A3(j): echo on sse: admin ${p6d_j_admin} '${p6d_j_admin_msg}', MCP '${p6d_j_mcp_msg}'; close 1005: ${p6d_j_code} '${p6d_j_code_msg}'; rule with nothing: ${p6d_j_empty} — every one must be 400 naming the rule"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d A3(j): echo on sse, a reserved close code and an empty rule are refused by name on both writers"
fi

p6d_want_checks=11
if [[ "$p6d_checks" != "$p6d_want_checks" ]]; then
	echo "FAIL  P6d acceptance (whole block): expected exactly ${p6d_want_checks} checks to have run, ${p6d_checks} actually ran"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P6d acceptance (whole block): all ${p6d_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# ---------------------------------------------------------------------------
# A6 (decisions.md mocker-a6-assets A11; DESIGN §32): uploaded files a mock
# can serve. The caps were set to 4kb / 6kb at the top of this file. Every
# check counts into a6_checks and the block's own total is asserted at its
# end, outside any guard, for the reason p2c_checks gives. Runs in HOST
# mode, before P1e's switch of the stack to MOCKER_ROUTING=path below:
# every URL here is <slug>.<base>, the shape §32.3's asset_url writes.
# ---------------------------------------------------------------------------
echo "== A6: assets — upload, serve, bodyRef, asset_url, delete =="
a6_checks=0
a6_check() {
	# a6_check <desc> <condition-exit> <detail>
	local desc=$1 rc=$2 detail=$3
	a6_checks=$((a6_checks + 1))
	if ((rc == 0)); then
		echo "PASS  A6 ${desc}"
	else
		echo "FAIL  A6 ${desc}: ${detail}"
		fail_count=$((fail_count + 1))
	fi
}
# raw_put <host> <path> <content-type> <file> — the raw-body PUT with the
# session cookie and the CSRF token, the one admin request that is not JSON.
raw_put() {
	local host=$1 path=$2 ctype=$3 file=$4
	curl -s -c "$COOKIE_JAR" -b "$COOKIE_JAR" -H "Host: ${host}" -H "Origin: http://${host}" \
		-H "X-CSRF-Token: ${csrf}" -H "Content-Type: ${ctype}" -o "$BODY_FILE" -w '%{http_code}' \
		-X PUT --data-binary "@${file}" "${BASE_URL}${path}"
}

# a6_put_variant <variant-json> — a full-replacement PUT of GET /widgets'
# override in the A6 workspace with the current editVersion (A3's CAS), the
# same shape every other block's PUT takes. Prints the status. The variant
# is STRICT JSON (quoted keys): it goes through jq --argjson, which refuses
# jq's own relaxed object syntax — a relaxed literal made jq fail, the body
# came out empty, and http_json's no-data branch sent no Content-Type (415).
a6_widgets_opkey=$(jq -rn --arg s "GET /widgets" '$s | @uri')
a6_put_variant() {
	local variant=$1 ev body
	ev=$(op_edit_version "$a6_ws_id" "$a6_widgets_opkey")
	body=$(jq -n --argjson ev "$ev" --argjson v "$variant" '{overrideOn: true, routeOff: false, responses: {"200": $v}, editVersion: $ev}')
	http_json PUT "$ADMIN_HOST" "/api/workspaces/${a6_ws_id}/operations/${a6_widgets_opkey}" "$body" -H "X-CSRF-Token: ${csrf}"
}

mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" create_workspace "$(jq -n --argjson sid "$spec_id" '{name: "a6-assets", specId: $sid}')")
a6_ws_id=$(jq -r '.result.structuredContent.workspace.id // .result.structuredContent.id' "$BODY_FILE")
a6_slug=$(jq -r '.result.structuredContent.workspace.slug // .result.structuredContent.slug' "$BODY_FILE")
if [[ "$mcp_status" != "200" || -z "$a6_ws_id" || "$a6_ws_id" == "null" ]]; then
	echo "FAIL  A6 setup: create_workspace: status ${mcp_status}: $(cat "$BODY_FILE")"
	exit 1
fi
a6_host="${a6_slug}.${WORKSPACE_HOST_BASE}"
a6_pic=$(mktemp)
a6_big=$(mktemp)
a6_mid=$(mktemp)
a6_got=$(mktemp)
printf '\x89PNG\r\n\x1a\n%s' "a6-smoke-picture-bytes-$$" >"$a6_pic"
head -c 5000 /dev/urandom >"$a6_big"
head -c 4000 /dev/urandom >"$a6_mid"

# 1. Upload: 201, the url is this host's mock-plane address.
a6_status=$(raw_put "$ADMIN_HOST" "/api/workspaces/${a6_ws_id}/assets/pic.png" image/png "$a6_pic")
a6_url=$(jq -r '.url // ""' "$BODY_FILE")
a6_sha=$(jq -r '.sha256 // ""' "$BODY_FILE")
a6_rc=0; { [[ "$a6_status" == "201" && "$a6_url" == "http://${a6_host}/__mocker/assets/pic.png" && ${#a6_sha} == 64 ]]; } || a6_rc=1
a6_check "1: PUT an image/png answers 201 with the mock-plane url" $a6_rc "status ${a6_status}, url '${a6_url}', sha '${a6_sha}'"

# 2. The mock route: bytes, type, ETag; 304 on If-None-Match.
a6_status=$(curl -s -H "Host: ${a6_host}" -o "$a6_got" -D "$HDR_FILE_A6" -w '%{http_code}' "${BASE_URL}/__mocker/assets/pic.png" 2>/dev/null || true)
a6_ct=$(grep -i '^content-type:' "$HDR_FILE_A6" | tr -d '\r' | awk '{print $2}')
a6_etag=$(grep -i '^etag:' "$HDR_FILE_A6" | tr -d '\r' | awk '{print $2}')
a6_rc=0; { [[ "$a6_status" == "200" && "$a6_ct" == "image/png" && "$a6_etag" == "\"${a6_sha}\"" ]] && cmp -s "$a6_pic" "$a6_got"; } || a6_rc=1
a6_check "2: GET {prefix}/assets/pic.png serves the exact bytes as image/png with ETag = sha256" $a6_rc "status ${a6_status}, type '${a6_ct}', etag '${a6_etag}', bytes equal: $(cmp -s "$a6_pic" "$a6_got" && echo yes || echo no)"
a6_status=$(curl -s -H "Host: ${a6_host}" -H "If-None-Match: \"${a6_sha}\"" -o /dev/null -w '%{http_code}' "${BASE_URL}/__mocker/assets/pic.png")
a6_rc=0; { [[ "$a6_status" == "304" ]]; } || a6_rc=1
a6_check "2b: If-None-Match with the ETag answers 304" $a6_rc "status ${a6_status}"

# 3. Refusals: a browser-executable type, a file over the 4kb cap.
a6_status=$(raw_put "$ADMIN_HOST" "/api/workspaces/${a6_ws_id}/assets/evil.html" text/html "$a6_pic")
a6_rc=0; { [[ "$a6_status" == "415" ]]; } || a6_rc=1
a6_check "3: PUT text/html is refused 415 at the CSRF chain" $a6_rc "status ${a6_status}: $(cat "$BODY_FILE")"
a6_status=$(raw_put "$ADMIN_HOST" "/api/workspaces/${a6_ws_id}/assets/big.bin" application/octet-stream "$a6_big")
a6_rc=0; { [[ "$a6_status" == "413" ]] && grep -qF asset_too_large "$BODY_FILE"; } || a6_rc=1
a6_check "3b: a 5000-byte file over MOCKER_MAX_ASSET=4kb is 413 asset_too_large" $a6_rc "status ${a6_status}: $(cat "$BODY_FILE")"
a6_status=$(raw_put "$ADMIN_HOST" "/api/workspaces/${a6_ws_id}/assets/mid.bin" application/octet-stream "$a6_mid")
a6_rc=0; { [[ "$a6_status" == "201" ]]; } || a6_rc=1
a6_check "3c: a 4000-byte file fits the per-file cap" $a6_rc "status ${a6_status}: $(cat "$BODY_FILE")"
a6_status=$(raw_put "$ADMIN_HOST" "/api/workspaces/${a6_ws_id}/assets/mid2.bin" application/octet-stream "$a6_mid")
a6_rc=0; { [[ "$a6_status" == "413" ]] && grep -qF assets_quota "$BODY_FILE"; } || a6_rc=1
a6_check "3d: a second 4000-byte file over MOCKER_MAX_ASSETS_TOTAL=6kb is 413 assets_quota" $a6_rc "status ${a6_status}: $(cat "$BODY_FILE")"

# 4. bodyRef: GET /widgets answers the picture's bytes under image/png.
mcp_status=$(a6_put_variant '{"mode": "pinned", "bodyRef": "asset:pic.png"}')
if [[ "$mcp_status" != "200" ]]; then
	echo "      (PUT the bodyRef variant answered ${mcp_status}: $(head -c 300 "$BODY_FILE"))"
fi
a6_status=$(curl -s -H "Host: ${a6_host}" -o "$a6_got" -D "$HDR_FILE_A6" -w '%{http_code}' "${BASE_URL}/widgets")
a6_ct=$(grep -i '^content-type:' "$HDR_FILE_A6" | tr -d '\r' | awk '{print $2}')
a6_rc=0; { [[ "$mcp_status" == "200" && "$a6_status" == "200" && "$a6_ct" == "image/png" ]] && cmp -s "$a6_pic" "$a6_got"; } || a6_rc=1
a6_check "4: a pinned variant with bodyRef serves the asset's bytes under image/png on GET /widgets" $a6_rc "PUT ${mcp_status}, GET ${a6_status}, type '${a6_ct}'"

# 4b. The same reference on a CUSTOM endpoint (§32.3 "an operation override
# or a custom endpoint"): the endpoint answers the bytes under image/png.
a6_ep_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${a6_ws_id}/endpoints" \
	'{"method":"GET","path":"/logo","bodyRef":"asset:pic.png"}' -H "X-CSRF-Token: ${csrf}")
a6_status=$(curl -s -H "Host: ${a6_host}" -o "$a6_got" -D "$HDR_FILE_A6" -w '%{http_code}' "${BASE_URL}/logo")
a6_ct=$(grep -i '^content-type:' "$HDR_FILE_A6" | tr -d '\r' | awk '{print $2}')
a6_rc=0; { [[ "$a6_ep_status" == "201" && "$a6_status" == "200" && "$a6_ct" == "image/png" ]] && cmp -s "$a6_pic" "$a6_got"; } || a6_rc=1
a6_check "4b: a custom endpoint's pinned bodyRef serves the asset's bytes under image/png on GET /logo" $a6_rc "POST endpoint ${a6_ep_status}, GET ${a6_status}, type '${a6_ct}': $(head -c 200 "$BODY_FILE")"

# 5. asset_url: every items[*].name is a working URL.
mcp_status=$(a6_put_variant '{"mode": "generated", "recipes": {"items[*].name": {"kind": "asset_url", "value": "pic.png"}}}')
a6_status=$(curl -s -H "Host: ${a6_host}" -o "$BODY_FILE" -w '%{http_code}' "${BASE_URL}/widgets")
a6_names=$(jq -r '[.items[].name] | unique | join(",")' "$BODY_FILE" 2>/dev/null || echo '<not a list>')
# The URL names the workspace host, which no DNS here resolves (curl exit 6
# under set -e was this block's first death): fetch its PATH on BASE_URL
# with the Host header, the way every other check in this file reaches a
# workspace.
a6_first=$(jq -r '.items[0].name // ""' "$BODY_FILE" 2>/dev/null || true)
a6_fetch=$(curl -s -H "Host: ${a6_host}" -o /dev/null -w '%{http_code}' "${BASE_URL}${a6_first#http://"$a6_host"}" || true)
a6_rc=0; { [[ "$mcp_status" == "200" && "$a6_status" == "200" && "$a6_names" == "http://${a6_host}/__mocker/assets/pic.png" && "$a6_fetch" == "200" ]]; } || a6_rc=1
a6_check "5: an asset_url recipe puts the absolute URL into every items[*].name, and the URL fetches 200" $a6_rc "set ${mcp_status}, GET ${a6_status}, names '${a6_names}', fetch ${a6_fetch}"

# 6. list_assets via MCP: two rows, the total and both caps.
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" list_assets "$(jq -n --argjson wid "$a6_ws_id" '{workspaceId: $wid}')")
a6_count=$(jq -r '.result.structuredContent.assets | length' "$BODY_FILE")
a6_total=$(jq -r '.result.structuredContent.totalBytes' "$BODY_FILE")
a6_capf=$(jq -r '.result.structuredContent.maxAssetBytes' "$BODY_FILE")
a6_capt=$(jq -r '.result.structuredContent.maxTotalBytes' "$BODY_FILE")
a6_rc=0; { [[ "$mcp_status" == "200" && "$a6_count" == "2" && "$a6_total" == "$(( $(stat -c %s "$a6_pic") + 4000 ))" && "$a6_capf" == "4096" && "$a6_capt" == "6144" ]]; } || a6_rc=1
a6_check "6: list_assets shows 2 rows, the stored total and the caps 4096/6144" $a6_rc "status ${mcp_status}, count ${a6_count}, total ${a6_total}, caps ${a6_capf}/${a6_capt}"

# 7. delete_asset with confirmSlug; the bodyRef variant then answers empty,
# and the traffic row says asset_missing.
a6_put_variant '{"mode": "pinned", "bodyRef": "asset:pic.png"}' >/dev/null
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" delete_asset "$(jq -n --argjson wid "$a6_ws_id" '{workspaceId: $wid, name: "pic.png", confirmSlug: "wrong"}')")
a6_wrong=$mcp_status
mcp_status=$(mcp_tool "$ADMIN_HOST" "$MCP_KEY" delete_asset "$(jq -n --argjson wid "$a6_ws_id" --arg s "$a6_slug" '{workspaceId: $wid, name: "pic.png", confirmSlug: $s}')")
a6_deleted=$(jq -r '.result.structuredContent.deleted // false' "$BODY_FILE")
a6_iserr=$(jq -r '.result.isError // false' "$BODY_FILE")
a6_rc=0; { [[ "$a6_deleted" == "true" && "$a6_iserr" != "true" ]]; } || a6_rc=1
a6_check "7: delete_asset refuses a wrong confirmSlug and deletes with the right one" $a6_rc "wrong-slug status ${a6_wrong}, right-slug deleted=${a6_deleted} isError=${a6_iserr}: $(head -c 200 "$BODY_FILE")"
a6_status=$(curl -s -H "Host: ${a6_host}" -o "$a6_got" -w '%{http_code}' "${BASE_URL}/widgets")
a6_rc=0; { [[ "$a6_status" == "200" && ! -s "$a6_got" ]]; } || a6_rc=1
a6_check "7b: the bodyRef variant now answers its 200 with an EMPTY body" $a6_rc "status ${a6_status}, body bytes $(stat -c %s "$a6_got")"
# The recorder writes in batches off the request path: give it a second,
# then read the tail the way every other block reads notes (the HTTP route,
# where notes is the comma-joined string traffic.Row carries).
sleep 1
mcp_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${a6_ws_id}/traffic")
a6_notes=$(jq -r '[.rows[] | select(.path == "/widgets")][0].notes // ""' "$BODY_FILE")
a6_rc=0; { [[ "$mcp_status" == "200" && "$a6_notes" == *asset_missing* ]]; } || a6_rc=1
a6_check "7c: the newest traffic row carries the asset_missing note" $a6_rc "status ${mcp_status}, notes '${a6_notes}'"
a6_status=$(curl -s -H "Host: ${a6_host}" -o /dev/null -w '%{http_code}' "${BASE_URL}/__mocker/assets/pic.png")
a6_rc=0; { [[ "$a6_status" == "404" ]]; } || a6_rc=1
a6_check "7d: the deleted asset's url answers 404" $a6_rc "status ${a6_status}"

rm -f "$a6_pic" "$a6_big" "$a6_mid" "$a6_got"
a6_want_checks=15
if [[ "$a6_checks" != "$a6_want_checks" ]]; then
	echo "FAIL  A6 acceptance (whole block): expected exactly ${a6_want_checks} checks to have run, ${a6_checks} actually ran"
	fail_count=$((fail_count + 1))
else
	echo "PASS  A6 acceptance (whole block): all ${a6_checks} checks ran to completion"
fi

# --------------------------------------------------------------------------
# P7a: designing an API on top of a workspace (DESIGN §34).
#
# Eight observations against the live stack, in the order the workflow runs:
# a schema on a custom endpoint SERVES a generated body (A5); a dangling
# $ref is refused at write time (A6); the export carries the delta by
# §34.4's rules (A11); schema + pinned body serves the body (A7); a $ref
# into the base resolves (A6); a rebind that would dangle is refused (A9);
# the export RE-IMPORTS and the round trip is a fixed point (A12); and a
# workspace with no spec exports the skeleton (A13).
# --------------------------------------------------------------------------
echo "== P7a: the workspace as a design — schema, export, round trip =="

p7a_checks=0

# A fresh workspace on the same spec, so the delta below is this section's
# own and nothing above it sees a new endpoint appear.
p7a_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces \
	"{\"name\":\"p7a design\",\"specId\":${spec_id}}" -H "X-CSRF-Token: ${csrf}")
p7a_ws_id=$(jq -r '.id' "$BODY_FILE")
p7a_slug=$(jq -r '.slug' "$BODY_FILE")
p7a_host="${p7a_slug}.${WORKSPACE_HOST_BASE}"
if [[ "$p7a_ws_status" != "201" ]]; then
	echo "FAIL  P7a setup: create workspace want 201, got ${p7a_ws_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
fi

# Observation 1: a custom endpoint with a SCHEMA and no pinned body serves
# a GENERATED body that satisfies the schema. Fails the pre-P7a build,
# where the same row answered an empty body at the same status.
p7a_ep_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints" \
	'{"method":"GET","path":"/designed","status":200,
	  "schema":{"type":"object","required":["id","title","tags"],
	            "properties":{"id":{"type":"integer"},"title":{"type":"string"},
	                          "tags":{"type":"array","items":{"type":"string"},"minItems":1}}},
	  "operation":{"summary":"A designed route","operationId":"getDesigned","tags":["design"]}}' \
	-H "X-CSRF-Token: ${csrf}")
p7a_ep_id=$(jq -r '.id' "$BODY_FILE")
p7a_serve_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p7a_host}" "${BASE_URL}/designed")
p7a_body_id=$(jq -r '.id | type' "$BODY_FILE")
p7a_body_title=$(jq -r '.title | type' "$BODY_FILE")
p7a_body_tags=$(jq -r '.tags | length' "$BODY_FILE")
p7a_checks=$((p7a_checks + 1))
if [[ "$p7a_ep_status" != "201" || "$p7a_serve_status" != "200" || "$p7a_body_id" != "number" || "$p7a_body_title" != "string" || "${p7a_body_tags:-0}" -lt 1 ]]; then
	echo "FAIL  P7a observation 1 (a schema generates): want create 201 and a 200 whose body has a numeric id, a string title and a non-empty tags array; got create ${p7a_ep_status}, serve ${p7a_serve_status}, id ${p7a_body_id}, title ${p7a_body_title}, tags ${p7a_body_tags}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a observation 1: a custom endpoint's schema serves a generated body (id ${p7a_body_id}, title ${p7a_body_title}, ${p7a_body_tags} tag(s))"
fi

# Observation 2: a $ref the bound spec does not have is REFUSED at write
# time — never stored dangling (D6). Fails an implementation that stores
# it and only complains at serve time.
p7a_bad_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints" \
	'{"method":"GET","path":"/dangling","status":200,
	  "schema":{"$ref":"#/components/schemas/NoSuchThing"}}' -H "X-CSRF-Token: ${csrf}")
p7a_bad_code=$(jq -r '.error.code // "none"' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints" >/dev/null
p7a_dangling_rows=$(jq '[.endpoints[] | select(.path == "/dangling")] | length' "$BODY_FILE")
p7a_checks=$((p7a_checks + 1))
if [[ "$p7a_bad_status" != "400" || "$p7a_bad_code" != "schema_ref_unresolved" || "$p7a_dangling_rows" != "0" ]]; then
	echo "FAIL  P7a observation 2 (a dangling \$ref is refused): want 400 schema_ref_unresolved and NO stored row; got ${p7a_bad_status} ${p7a_bad_code}, ${p7a_dangling_rows} row(s)"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a observation 2: a \$ref the spec does not have is refused at write time and nothing is stored"
fi

# Observation 3: the export carries the delta by §34.4's rules — the new
# operation with its own fields, the base's operations untouched, and a
# routeOff row marked deprecated rather than deleted.
p7a_off_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints" \
	'{"method":"GET","path":"/retired","status":200,"body":{"gone":true}}' -H "X-CSRF-Token: ${csrf}")
p7a_off_id=$(jq -r '.id' "$BODY_FILE")
p7a_off_ev=$(jq -r '.editVersion' "$BODY_FILE")
p7a_off_put=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints/${p7a_off_id}" \
	"{\"method\":\"GET\",\"path\":\"/retired\",\"routeOff\":true,\"activeStatus\":200,\"responses\":{},\"editVersion\":${p7a_off_ev}}" \
	-H "X-CSRF-Token: ${csrf}")
# An overrideOn:false row is OMITTED from the document (D7.2, the owner's
# call): created, switched off through PUT, and then looked for.
p7a_hidden_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints" \
	'{"method":"GET","path":"/hidden","status":200,"body":{"hidden":true}}' -H "X-CSRF-Token: ${csrf}")
p7a_hidden_id=$(jq -r '.id' "$BODY_FILE")
p7a_hidden_ev=$(jq -r '.editVersion' "$BODY_FILE")
p7a_hidden_put=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints/${p7a_hidden_id}" \
	"{\"method\":\"GET\",\"path\":\"/hidden\",\"overrideOn\":false,\"activeStatus\":200,\"responses\":{\"200\":{\"mode\":\"pinned\",\"body\":{\"hidden\":true}}},\"editVersion\":${p7a_hidden_ev}}" \
	-H "X-CSRF-Token: ${csrf}")
p7a_export_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/openapi.json")
p7a_export_file=$(mktemp)
cp "$BODY_FILE" "$p7a_export_file"
p7a_op_id=$(jq -r '.paths["/designed"].get.operationId // "none"' "$p7a_export_file")
p7a_op_schema=$(jq -r '.paths["/designed"].get.responses["200"].content["application/json"].schema.type // "none"' "$p7a_export_file")
p7a_retired_dep=$(jq -r '.paths["/retired"].get.deprecated // false' "$p7a_export_file")
p7a_base_kept=$(jq -r '.paths["/widgets"].get.operationId // "none"' "$p7a_export_file")
p7a_hidden_in_doc=$(jq -r '.paths | has("/hidden")' "$p7a_export_file")
p7a_version=$(jq -r '.info.version' "$p7a_export_file")
p7a_checks=$((p7a_checks + 1))
if [[ "$p7a_off_status" != "201" || "$p7a_off_put" != "200" || "$p7a_hidden_status" != "201" || "$p7a_hidden_put" != "200" || "$p7a_export_status" != "200" ||
	"$p7a_op_id" != "getDesigned" || "$p7a_op_schema" != "object" ||
	"$p7a_retired_dep" != "true" || "$p7a_base_kept" == "none" || "$p7a_hidden_in_doc" != "false" || "$p7a_version" != *-draft.* ]]; then
	echo "FAIL  P7a observation 3 (the export composes the delta): want the new operation (operationId getDesigned, an object schema), the routeOff row deprecated:true, the base's /widgets kept, the overrideOn:false row absent and a -draft. version; got export ${p7a_export_status}, operationId ${p7a_op_id}, schema ${p7a_op_schema}, deprecated ${p7a_retired_dep}, base ${p7a_base_kept}, /hidden present ${p7a_hidden_in_doc}, version ${p7a_version}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a observation 3: the export carries the delta (getDesigned with its schema, /retired deprecated, /hidden omitted, the base intact, version ${p7a_version})"
fi

# Observation 4 (A7, the mode rule): a row with a schema AND a pinned body
# serves the pinned bytes, and the export carries both — the schema as the
# declared shape, the body as `examples`. Fails an implementation that lets
# the schema replace the pinned body.
p7a_both_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints" \
	'{"method":"GET","path":"/both","status":200,"body":{"pinned":true},
	  "schema":{"type":"object","properties":{"pinned":{"type":"boolean"}}}}' -H "X-CSRF-Token: ${csrf}")
p7a_both_id=$(jq -r '.id' "$BODY_FILE")
p7a_both_serve=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p7a_host}" "${BASE_URL}/both")
p7a_both_body=$(jq -c '.' "$BODY_FILE" 2>/dev/null || echo none)
http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/openapi.json" >/dev/null
p7a_both_schema=$(jq -r '.paths["/both"].get.responses["200"].content["application/json"].schema.type // "none"' "$BODY_FILE")
p7a_both_example=$(jq -r '.paths["/both"].get.responses["200"].content["application/json"].examples[0].pinned // "none"' "$BODY_FILE")
p7a_checks=$((p7a_checks + 1))
if [[ "$p7a_both_status" != "201" || "$p7a_both_serve" != "200" || "$p7a_both_body" != '{"pinned":true}' || "$p7a_both_schema" != "object" || "$p7a_both_example" != "true" ]]; then
	echo "FAIL  P7a observation 4 (the mode rule): want the pinned body served and both schema+example exported; got create ${p7a_both_status}, serve ${p7a_both_serve} ${p7a_both_body}, schema ${p7a_both_schema}, example.pinned ${p7a_both_example}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a observation 4: schema + pinned body serves the pinned bytes and exports both"
fi

# Observation 5 (A6): a $ref INTO the bound spec resolves — the row is
# accepted and the served body carries the component's required
# properties (Widget requires id and name).
p7a_me_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints" \
	'{"method":"GET","path":"/me","status":200,"schema":{"$ref":"#/components/schemas/Widget"}}' -H "X-CSRF-Token: ${csrf}")
p7a_me_id=$(jq -r '.id' "$BODY_FILE")
p7a_me_serve=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${p7a_host}" "${BASE_URL}/me")
p7a_me_shape=$(jq -r 'if (.id|type)=="number" and (.name|type)=="string" then "widget" else "other" end' "$BODY_FILE" 2>/dev/null || echo none)
p7a_checks=$((p7a_checks + 1))
if [[ "$p7a_me_status" != "201" || "$p7a_me_serve" != "200" || "$p7a_me_shape" != "widget" ]]; then
	echo "FAIL  P7a observation 5 (a \$ref into the base): want 201 and a body shaped like Widget; got ${p7a_me_status}, serve ${p7a_me_serve}, shape ${p7a_me_shape}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a observation 5: a \$ref into the bound spec's Widget generates a Widget-shaped body"
fi

# Observation 6 (A9): rebinding the workspace to a spec WITHOUT Widget is
# refused 409 endpoint_ref_unresolved naming the row, and the binding does
# not move. Fails an implementation that writes the bind before the check.
p7a_other_doc='{"openapi":"3.1.0","info":{"title":"p7a other","version":"1.0.0"},"paths":{"/other":{"get":{"responses":{"200":{"description":"ok"}}}}}}'
p7a_other_body=$(jq -n --arg name "p7a-other" --arg source "upload" --arg document "$p7a_other_doc" '{name: $name, source: $source, document: $document}')
http_json POST "$ADMIN_HOST" /api/specs "$p7a_other_body" -H "X-CSRF-Token: ${csrf}" >/dev/null
p7a_other_id=$(jq -r '.id' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}" >/dev/null
p7a_ws_ev=$(jq -r '.editVersion' "$BODY_FILE")
p7a_rebind_status=$(http_json PATCH "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}" \
	"{\"specId\":${p7a_other_id},\"editVersion\":${p7a_ws_ev}}" -H "X-CSRF-Token: ${csrf}")
p7a_rebind_code=$(jq -r '.error.code // "none"' "$BODY_FILE")
p7a_rebind_row=$(jq -r '.error.details[0].endpointId // "none"' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}" >/dev/null
p7a_spec_after=$(jq -r '.specId' "$BODY_FILE")
p7a_checks=$((p7a_checks + 1))
if [[ "$p7a_rebind_status" != "409" || "$p7a_rebind_code" != "endpoint_ref_unresolved" || "$p7a_rebind_row" != "$p7a_me_id" || "$p7a_spec_after" != "$spec_id" ]]; then
	echo "FAIL  P7a observation 6 (a rebind that would dangle is refused): want 409 endpoint_ref_unresolved naming endpoint ${p7a_me_id} and specId still ${spec_id}; got ${p7a_rebind_status} ${p7a_rebind_code} row ${p7a_rebind_row}, specId ${p7a_spec_after}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a observation 6: a rebind onto a spec without Widget is refused and the binding stays on spec ${spec_id}"
fi

# Observation 7 (A12, the round trip — D8 stated exactly): A′ ← export;
# import_spec(A′) is a NEW spec; PATCH specId; the drift report names every
# custom row as shadowed (each now has a base operation at its shape) and
# no orphaned override (the fixture has none); delete the delta; A″ ←
# export; A′ and A″ are byte-equal once info.version is dropped. Fails if
# a normalizer rewrite differs between the two, or if the export writes
# anything the import drops.
# D7.3's respelling: a custom row at the base's GET /widgets/{id} shape
# spelled {widgetId} REPLACES that operation under its own spelling, and an
# override keyed on the base's literal path is what the drift report must
# call orphaned once the export is accepted (D8: "exactly the override(s)
# on an operation the export respelled").
p7a_twin_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints" \
	'{"method":"GET","path":"/widgets/{widgetId}","status":200,"schema":{"$ref":"#/components/schemas/Widget"},
	  "operation":{"summary":"A widget, respelled","operationId":"getWidgetRespelled"}}' -H "X-CSRF-Token: ${csrf}")
p7a_twin_id=$(jq -r '.id' "$BODY_FILE")
p7a_twin_opkey=$(jq -rn --arg s "GET /widgets/{id}" '$s | @uri')
p7a_twin_ev=$(op_edit_version "$p7a_ws_id" "$p7a_twin_opkey")
p7a_twin_put=$(http_json PUT "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/operations/${p7a_twin_opkey}" \
	"{\"overrideOn\":true,\"routeOff\":false,\"responses\":{\"200\":{\"mode\":\"generated\",\"recipes\":{\"name\":{\"kind\":\"const\",\"value\":\"RESPELLED\"}}}},\"editVersion\":${p7a_twin_ev}}" \
	-H "X-CSRF-Token: ${csrf}")
p7a_export_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/openapi.json")
cp "$BODY_FILE" "$p7a_export_file"
p7a_twin_base_present=$(jq -r '.paths | has("/widgets/{id}")' "$p7a_export_file")
p7a_twin_in_doc=$(jq -r '.paths["/widgets/{widgetId}"].get.operationId // "none"' "$p7a_export_file")
p7a_doc=$(cat "$p7a_export_file")
p7a_import_body=$(jq -n --arg name "p7a-accepted" --arg source "upload" --arg document "$p7a_doc" \
	'{name: $name, source: $source, document: $document}')
p7a_import_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p7a_import_body" -H "X-CSRF-Token: ${csrf}")
p7a_new_spec=$(jq -r '.id' "$BODY_FILE")
p7a_reimport_status=$(http_json POST "$ADMIN_HOST" /api/specs "$p7a_import_body" -H "X-CSRF-Token: ${csrf}")
p7a_dup=$(jq -r '.duplicate // false' "$BODY_FILE")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}" >/dev/null
p7a_ws_ev=$(jq -r '.editVersion' "$BODY_FILE")
p7a_accept_status=$(http_json PATCH "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}" \
	"{\"specId\":${p7a_new_spec},\"editVersion\":${p7a_ws_ev}}" -H "X-CSRF-Token: ${csrf}")
http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/drift" >/dev/null
p7a_shadowed=$(jq -r '.shadowedEndpoints | length' "$BODY_FILE")
p7a_orphaned=$(jq -r '.orphanedOverrides | length' "$BODY_FILE")
p7a_delete_fail=0
for eid in "$p7a_ep_id" "$p7a_off_id" "$p7a_both_id" "$p7a_me_id" "$p7a_twin_id" "$p7a_hidden_id"; do
	del_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/endpoints/${eid}" '{}' -H "X-CSRF-Token: ${csrf}")
	[[ "$del_status" == "204" ]] || p7a_delete_fail=$((p7a_delete_fail + 1))
done
del_status=$(http_json DELETE "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/operations/${p7a_twin_opkey}" '{}' -H "X-CSRF-Token: ${csrf}")
[[ "$del_status" == "200" || "$del_status" == "204" ]] || p7a_delete_fail=$((p7a_delete_fail + 1))
p7a_export2_status=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_ws_id}/openapi.json")
p7a_export2_file="${p7a_export_file%.json}-2.json"
cp "$BODY_FILE" "$p7a_export2_file"
p7a_diff=$(diff <(jq -S 'del(.info.version)' "$p7a_export_file") <(jq -S 'del(.info.version)' "$p7a_export2_file") | head -20)
p7a_checks=$((p7a_checks + 1))
if [[ "$p7a_twin_status" != "201" || "$p7a_twin_put" != "200" || "$p7a_twin_base_present" != "false" || "$p7a_twin_in_doc" != "getWidgetRespelled" ||
	"$p7a_export_status" != "200" || "$p7a_import_status" != "201" || "$p7a_reimport_status" != "200" || "$p7a_dup" != "true" ||
	"$p7a_accept_status" != "200" || "$p7a_shadowed" != "5" || "$p7a_orphaned" != "1" || "$p7a_delete_fail" != "0" ||
	"$p7a_export2_status" != "200" || -n "$p7a_diff" ]]; then
	echo "FAIL  P7a observation 7 (the round trip): want the respelled twin in the document and the base's /widgets/{id} gone, export 200, import 201 then duplicate:true, PATCH 200, drift shadowed 5 (the overrideOn:false row is in no base) / orphaned 1, six 204s and a 200/204, and an empty diff; got twin ${p7a_twin_status}/${p7a_twin_put} base-present=${p7a_twin_base_present} twin=${p7a_twin_in_doc}, export ${p7a_export_status}, import ${p7a_import_status}/${p7a_reimport_status} dup=${p7a_dup}, PATCH ${p7a_accept_status}, shadowed ${p7a_shadowed}, orphaned ${p7a_orphaned}, delete failures ${p7a_delete_fail}, export2 ${p7a_export2_status}; diff:"
	echo "$p7a_diff"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a observation 7: the export re-imports as spec ${p7a_new_spec}, the drift names the 5 shadowed rows and the 1 orphaned override, and the export after accepting equals the one before it"
fi

# Observation 8 (A13): a workspace with NO spec exports the skeleton — "a
# design from nothing" — titled with its own name and versioned
# 0.0.0-draft.<revision>, and that document imports as a spec with zero
# operations.
p7a_bare_status=$(http_json POST "$ADMIN_HOST" /api/workspaces '{"name":"p7a scratch"}' -H "X-CSRF-Token: ${csrf}")
p7a_bare_id=$(jq -r '.id' "$BODY_FILE")
p7a_bare_export=$(http_json GET "$ADMIN_HOST" "/api/workspaces/${p7a_bare_id}/openapi.json")
p7a_bare_openapi=$(jq -r '.openapi' "$BODY_FILE")
p7a_bare_paths=$(jq -r '.paths | length' "$BODY_FILE")
p7a_bare_title=$(jq -r '.info.title' "$BODY_FILE")
p7a_bare_version=$(jq -r '.info.version' "$BODY_FILE")
p7a_bare_doc=$(cat "$BODY_FILE")
p7a_bare_import_body=$(jq -n --arg name "p7a-skeleton" --arg source "upload" --arg document "$p7a_bare_doc" '{name: $name, source: $source, document: $document}')
p7a_bare_import=$(http_json POST "$ADMIN_HOST" /api/specs "$p7a_bare_import_body" -H "X-CSRF-Token: ${csrf}")
p7a_checks=$((p7a_checks + 1))
if [[ "$p7a_bare_status" != "201" || "$p7a_bare_export" != "200" || "$p7a_bare_openapi" != 3.1.* || "$p7a_bare_paths" != "0" || "$p7a_bare_title" != "p7a scratch" || "$p7a_bare_version" != 0.0.0-draft.* || "$p7a_bare_import" != "201" ]]; then
	echo "FAIL  P7a observation 8 (a design from nothing): want a 200 export (openapi 3.1.x, no paths, title 'p7a scratch', version 0.0.0-draft.N) that imports 201; got ${p7a_bare_export}, openapi ${p7a_bare_openapi}, ${p7a_bare_paths} path(s), title ${p7a_bare_title}, version ${p7a_bare_version}, import ${p7a_bare_import}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a observation 8: a workspace with no spec exports the titled 3.1 skeleton (${p7a_bare_version}) and it imports"
fi

p7a_want_checks=8
if [[ "$p7a_checks" != "$p7a_want_checks" ]]; then
	echo "FAIL  P7a acceptance (whole block): expected exactly ${p7a_want_checks} checks to have run, ${p7a_checks} actually ran"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P7a acceptance (whole block): all ${p7a_checks} checks ran to completion"
fi

# P1e task 2: MOCKER_ROUTING=path had no UI before this phase — servePath's
# default branch answered a diagnostic 404 for "/", "/login" and every SPA
# asset. This block proves the fix against the real compose stack, the same
# way every check above it does: no teardown of its own, the existing trap
# already restores .env and brings the stack down on exit.
# --------------------------------------------------------------------------
# A18: endpoint functions (Lua) — the deployed artefact.
#
# Seven observations, all through the IMAGE and over the real mock host, on
# the one question a unit test cannot answer: does the binary that ships
# actually carry the VM and reach the workspace's own settings with it. The
# sign-in shape is the feature's own motivating example (D1), so it leads.
# --------------------------------------------------------------------------
echo "== A18: endpoint functions — sign-in, guards, and a Lua tick =="

a18_checks=0

a18_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces \
	'{"name":"a18 functions"}' -H "X-CSRF-Token: ${csrf}")
a18_ws_id=$(jq -r '.id' "$BODY_FILE")
a18_slug=$(jq -r '.slug' "$BODY_FILE")
a18_host="${a18_slug}.${WORKSPACE_HOST_BASE}"
if [[ "$a18_ws_status" != "201" ]]; then
	echo "FAIL  A18: create workspace: want 201, got ${a18_ws_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	# The workspace needs a signing key for mock.jwt to answer at all — the
	# refusal on an unconfigured one is observation (c) below, and this is
	# the settings write that makes (a) possible. PATCH takes the WHOLE
	# settings object, so it is read first.
	http_json GET "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}" >/dev/null
	a18_ev=$(jq -r '.editVersion' "$BODY_FILE")
	a18_settings=$(jq -c '.settings | .auth = {"alg":"HS256","signingKey":"smoke-secret","jwtTtlSec":3600}' "$BODY_FILE")
	http_json PATCH "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}" \
		"{\"settings\":${a18_settings},\"editVersion\":${a18_ev}}" -H "X-CSRF-Token: ${csrf}" >/dev/null

	# (a) The sign-in shape of D1: one function variant that BRANCHES on the
	# request body and mints a token with the workspace's own key, beside a
	# pinned sibling on another status — the mixed row D5 makes legal.
	a18_fn='if req.body.password == "hunter2" then return 200, { token = mock.jwt({ sub = 42 }) } end return 401, { error = "bad credentials" }'
	a18_body=$(jq -n --arg fn "$a18_fn" \
		'{method:"POST", path:"/sign-in", status:200, function:$fn}')
	a18_create=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}/endpoints" \
		"$a18_body" -H "X-CSRF-Token: ${csrf}")
	if [[ "$a18_create" != "201" ]]; then
		echo "FAIL  A18(a): create a function endpoint: want 201, got ${a18_create}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		a18_ok=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${a18_host}" \
			-H 'Content-Type: application/json' --data '{"password":"hunter2"}' \
			"${BASE_URL}/sign-in")
		# Three segments with a real signature: the token came from the
		# workspace's own settings.auth through the same signer the jwt
		# recipe uses, not from a placeholder.
		a18_tok=$(jq -r '.token // ""' "$BODY_FILE")
		if [[ "$a18_ok" == "200" && "$(awk -F. '{print NF}' <<<"$a18_tok")" == "3" && -n "$(awk -F. '{print $3}' <<<"$a18_tok")" ]]; then
			echo "PASS  A18(a): the right password answers 200 with a signed token"
			a18_checks=$((a18_checks + 1))
		else
			echo "FAIL  A18(a): want 200 and a three-segment signed token, got ${a18_ok}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		fi

		a18_bad=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${a18_host}" \
			-H 'Content-Type: application/json' --data '{"password":"nope"}' \
			"${BASE_URL}/sign-in")
		if [[ "$a18_bad" == "401" ]] && grep -qF 'bad credentials' "$BODY_FILE"; then
			echo "PASS  A18(b): the wrong password answers the function's own 401"
			a18_checks=$((a18_checks + 1))
		else
			echo "FAIL  A18(b): want 401 from the function, got ${a18_bad}: $(cat "$BODY_FILE")"
			fail_count=$((fail_count + 1))
		fi
	fi

	# (c) Unparseable Lua is refused at WRITE time with the parser's own
	# words — this plane always answers, so a deferred parse would be a 500
	# on the first request instead (D8).
	a18_badsrc=$(jq -n '{method:"POST", path:"/broken", status:200, function:"return 200, }"}')
	a18_refused=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}/endpoints" \
		"$a18_badsrc" -H "X-CSRF-Token: ${csrf}")
	if [[ "$a18_refused" == "400" ]] && grep -qF 'near' "$BODY_FILE" && grep -qF '"bad_function"' "$BODY_FILE"; then
		echo "PASS  A18(c): unparseable Lua is a 400 bad_function carrying the parser's own words"
		a18_checks=$((a18_checks + 1))
	else
		echo "FAIL  A18(c): want 400 naming the token, got ${a18_refused}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	fi

	# (d) A variant carrying two producers is refused BY NAME — one producer
	# per variant, so there is no precedence to document (D5).
	a18_both=$(jq -n '{method:"POST", path:"/both", status:200, body:{a:1}, function:"return 200, {}"}')
	a18_both_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}/endpoints" \
		"$a18_both" -H "X-CSRF-Token: ${csrf}")
	if [[ "$a18_both_status" == "400" ]] && grep -qF '"function_and_body"' "$BODY_FILE"; then
		echo "PASS  A18(d): a function beside a body is refused by name (function_and_body)"
		a18_checks=$((a18_checks + 1))
	else
		echo "FAIL  A18(d): want 400 naming the exclusivity, got ${a18_both_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	fi

	# (e) The browser-executable Content-Type refusal, applied to a type the
	# function computes per request — the one rule both planes share, and
	# the one no write-time check can see here.
	a18_html=$(jq -n '{method:"GET", path:"/xss", status:200, function:"return 200, \"<script>x</script>\", {[\"Content-Type\"] = \"text/html\"}"}')
	http_json POST "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}/endpoints" \
		"$a18_html" -H "X-CSRF-Token: ${csrf}" >/dev/null
	a18_xss=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${a18_host}" "${BASE_URL}/xss")
	if [[ "$a18_xss" == "500" ]] && ! grep -qF '<script>' "$BODY_FILE"; then
		echo "PASS  A18(e): a browser-executable Content-Type refuses the whole response"
		a18_checks=$((a18_checks + 1))
	else
		echo "FAIL  A18(e): want 500 and no script byte, got ${a18_xss}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	fi

	# (f) The 2 s budget is real in the shipped binary: SetContext interrupts
	# the VM between instructions, and the answer is a 503 and not a 500 —
	# the two classes are separate on purpose (D6).
	a18_loop=$(jq -n '{method:"GET", path:"/spin", status:200, function:"while true do end"}')
	http_json POST "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}/endpoints" \
		"$a18_loop" -H "X-CSRF-Token: ${csrf}" >/dev/null
	a18_spin_started=$(date +%s)
	a18_spin=$(curl -s -o "$BODY_FILE" -w '%{http_code}' --max-time 20 -H "Host: ${a18_host}" "${BASE_URL}/spin")
	a18_spin_took=$(($(date +%s) - a18_spin_started))
	if [[ "$a18_spin" == "503" && "$a18_spin_took" -le 10 ]]; then
		echo "PASS  A18(f): an infinite loop is cut at the budget and answers 503 (${a18_spin_took}s)"
		a18_checks=$((a18_checks + 1))
	else
		echo "FAIL  A18(f): want 503 within the budget, got ${a18_spin} after ${a18_spin_took}s: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	fi

	# (g) D10.1's tick.lua over a real SSE connection: the frame bodies come
	# from the hook, and `schema` is not required beside it (D8b(2)).
	a18_tick=$(jq -n '{method:"GET", path:"/lua-events", kind:"sse",
		stream:{tick:{intervalMs:100, event:"n", lua:"return { n = ordinal }"}}}')
	a18_tick_status=$(http_json POST "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}/endpoints" \
		"$a18_tick" -H "X-CSRF-Token: ${csrf}")
	if [[ "$a18_tick_status" != "201" ]]; then
		echo "FAIL  A18(g): create a tick.lua stream: want 201, got ${a18_tick_status}: $(cat "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	else
		a18_frames=$(curl -s --max-time 3 -H "Host: ${a18_host}" "${BASE_URL}/lua-events" || true)
		if grep -qF 'data: {"n":1}' <<<"$a18_frames" && grep -qF 'data: {"n":2}' <<<"$a18_frames"; then
			echo "PASS  A18(g): a tick.lua stream sends the hook's own frames"
			a18_checks=$((a18_checks + 1))
		else
			echo "FAIL  A18(g): want the hook's frames, got: ${a18_frames}"
			fail_count=$((fail_count + 1))
		fi
	fi

	# (h) The traffic row says which of the four outcomes happened — the only
	# place an operator sees a function ran at all.
	http_json GET "$ADMIN_HOST" "/api/workspaces/${a18_ws_id}/traffic?limit=100" >/dev/null
	# The notes JOIN — a request can be both redacted and function-served, and
	# the sign-in row IS both, because its body carried a password field. So
	# each token is matched as a whole COMMA ENTRY (traffic.Row.HasNote's own
	# rule) and never as the whole string: the first draft of this check
	# compared `.notes` to "function" and went red against a correct build.
	if jq -e '[.rows[] | select(.path == "/sign-in") | .notes | split(",")] | any(index("function"))' "$BODY_FILE" >/dev/null &&
		jq -e '[.rows[] | select(.path == "/spin") | .notes | split(",")] | any(index("function_timeout"))' "$BODY_FILE" >/dev/null; then
		echo "PASS  A18(h): the traffic rows carry function and function_timeout"
		a18_checks=$((a18_checks + 1))
	else
		echo "FAIL  A18(h): want function and function_timeout notes: $(jq -c '[.rows[] | {path, notes}]' "$BODY_FILE")"
		fail_count=$((fail_count + 1))
	fi
fi

echo "      A18: ${a18_checks}/8 observations passed"

# --------------------------------------------------------------------------
echo "== P1e: MOCKER_ROUTING=path — the admin UI is also reachable there =="

grep -v '^MOCKER_ROUTING=' "$ENV_FILE" >"${ENV_FILE}.tmp"
mv "${ENV_FILE}.tmp" "$ENV_FILE"
printf 'MOCKER_ROUTING=path\n' >>"$ENV_FILE"

echo "== recreating the compose stack under path mode =="
docker compose up -d --force-recreate

echo "== waiting for ${BASE_URL} (path mode) =="
wait_for_port

path_index_body=$(curl -s -H "Host: ${ADMIN_HOST}" "${BASE_URL}/")
if ! grep -qE '<script[^>]+src=' <<<"$path_index_body"; then
	echo "FAIL  path mode GET /: no <script src=...> tag in the body — looks like a placeholder, not the built app: ${path_index_body}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  path mode GET /: body carries a <script src=...> tag"
fi

path_ws_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${ADMIN_HOST}" "${BASE_URL}/workspaces/1")
if [[ "$path_ws_status" != "200" ]]; then
	echo "FAIL  path mode GET /workspaces/1: want status 200 (SPA fallback for a client route), got ${path_ws_status}"
	fail_count=$((fail_count + 1))
elif [[ "$(cat "$BODY_FILE")" != "$path_index_body" ]]; then
	echo "FAIL  path mode GET /workspaces/1: body differs from GET / — a reloaded deep link would not get the app shell"
	fail_count=$((fail_count + 1))
else
	echo "PASS  path mode GET /workspaces/1: 200, byte-identical to / (a reloaded deep link works)"
fi

# P7b (B7): the tenth tab is a client route like any other — a reloaded
# deep link to /workspaces/1/contract gets the app shell, byte for byte.
path_contract_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${ADMIN_HOST}" "${BASE_URL}/workspaces/1/contract")
if [[ "$path_contract_status" != "200" ]]; then
	echo "FAIL  path mode GET /workspaces/1/contract: want status 200 (SPA fallback for the P7b tab), got ${path_contract_status}"
	fail_count=$((fail_count + 1))
elif [[ "$(cat "$BODY_FILE")" != "$path_index_body" ]]; then
	echo "FAIL  path mode GET /workspaces/1/contract: body differs from GET / — a reloaded «Контракт» tab would not get the app shell"
	fail_count=$((fail_count + 1))
else
	echo "PASS  path mode GET /workspaces/1/contract: 200, byte-identical to / (the P7b tab reloads)"
fi

path_nope_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${ADMIN_HOST}" "${BASE_URL}/api/definitely-nope")
if [[ "$path_nope_status" != "404" ]]; then
	echo "FAIL  path mode GET /api/definitely-nope: want status 404, got ${path_nope_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
elif grep -qE '<script[^>]+src=' "$BODY_FILE"; then
	echo "FAIL  path mode GET /api/definitely-nope: body looks like the SPA shell, not net/http's own plain 404: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  path mode GET /api/definitely-nope: 404, not the SPA shell (the /api/ prefix is still admin's, not the SPA's, catch-all)"
fi

check "path mode GET /w/ is the diagnostic 404 naming a workspace slug" 404 "$ADMIN_HOST" /w/ "/w/{slug}"

path_wnope_status=$(curl -s -o "$BODY_FILE" -w '%{http_code}' -H "Host: ${ADMIN_HOST}" "${BASE_URL}/w/nope/anything")
if [[ "$path_wnope_status" != "404" ]]; then
	echo "FAIL  path mode GET /w/nope/anything: want status 404 (the mock plane's own not-found for an unknown workspace), got ${path_wnope_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
elif grep -qE '<script[^>]+src=' "$BODY_FILE"; then
	echo "FAIL  path mode GET /w/nope/anything: body looks like the SPA shell — /w/{slug}/... belongs to the mock plane, never the admin UI"
	fail_count=$((fail_count + 1))
else
	echo "PASS  path mode GET /w/nope/anything: 404 from the mock plane (unknown workspace), not the SPA"
fi

# --------------------------------------------------------------------------
# MCP observation 11, path-mode half. This is the ONE piece of the MCP block
# above that belongs INSIDE the path-mode switch rather than before it: it
# is specifically testing that /w/{slug}/mcp (mock plane) and /mcp (admin
# plane) stay separated once path routing puts both under the same origin --
# a distinction that does not exist at all in host mode, where they are
# already on different hosts. mcp_call, MCP_KEY and mcp_tools_list_req are
# all still in scope: plain script-level assignments made above, and
# MOCKER_MCP_KEY was only ever ADDED to .env, never removed by this block's
# own MOCKER_ROUTING rewrite.
# --------------------------------------------------------------------------
echo "== MCP observation 11 (path mode half): the mock plane never serves /mcp =="

mcp_status=$(mcp_call POST "$ADMIN_HOST" /mcp "$MCP_KEY" "$mcp_tools_list_req")
if [[ "$mcp_status" != "200" ]] || ! grep -qF '"jsonrpc"' "$BODY_FILE"; then
	echo "FAIL  path mode POST /mcp (admin host): want status 200 with a JSON-RPC body, got ${mcp_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  path mode POST /mcp (admin host): still answers as MCP (routing mode never moves where /mcp is mounted)"
fi

mcp_w_status=$(mcp_call POST "$ADMIN_HOST" "/w/${slug}/mcp" "$MCP_KEY" "$mcp_tools_list_req")
if [[ "$mcp_w_status" != "404" ]] || grep -qF '"jsonrpc"' "$BODY_FILE" || grep -qE '<script[^>]+src=' "$BODY_FILE"; then
	echo "FAIL  path mode POST /w/${slug}/mcp: want status 404 from the mock plane (not an MCP response, not the SPA), got ${mcp_w_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  path mode POST /w/${slug}/mcp: 404 from the mock plane -- the two planes stay separated sharing one origin"
fi

# --------------------------------------------------------------------------
# P2d observation 13 (its own stack, LAST of all): the debounce window
# RE-ARMS, so MOCKER_CHECKPOINT_DEBOUNCE is actually read at request time
# and not just consulted once, at boot, to decide whether to install the
# wrapper. This is the FOURTH `docker compose up` in this script and the
# THIRD `--force-recreate` (the other two: the MCP key above, and
# MOCKER_ROUTING=path in the P1e block just above this one). .env is
# rewritten CUMULATIVELY, so this stack comes up with MOCKER_MCP_KEY set
# and MOCKER_ROUTING=path already in force -- neither matters here, because
# every call below is admin-plane and the admin host is reachable in both
# routing modes (the P1e block directly above proves that for itself). The
# named volume survives --force-recreate, so $spec_id is still a spec this
# stack knows about.
#
# Window=5s, sleep=6s -- "five and six, not two and two" (§5's own
# phrasing): created_at is an INTEGER of unix seconds, so a two-second
# window is one tick wide, and a round trip slower than a second would
# compute now-max=2 and write a row, red against otherwise-correct code.
# Five and six leave slack a slow container start cannot eat.
# --------------------------------------------------------------------------
echo "== P2d observation 13 (own stack, last of all): the debounce window RE-ARMS =="

grep -v '^MOCKER_CHECKPOINT_DEBOUNCE=' "$ENV_FILE" >"${ENV_FILE}.tmp"
mv "${ENV_FILE}.tmp" "$ENV_FILE"
printf 'MOCKER_CHECKPOINT_DEBOUNCE=5\n' >>"$ENV_FILE"

echo "== recreating the compose stack with MOCKER_CHECKPOINT_DEBOUNCE=5 =="
docker compose up -d --force-recreate

echo "== waiting for ${BASE_URL} (debounce window = 5s) =="
wait_for_port

p2d_rearm_ws_status=$(http_json POST "$ADMIN_HOST" /api/workspaces "$(jq -cn --arg n "p2d-rearm" --argjson s "$spec_id" '{name:$n,specId:$s}')" -H "X-CSRF-Token: ${csrf}")
p2d_rearm_ws_id=$(jq -r '.id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_rearm_ws_status" != "201" || -z "$p2d_rearm_ws_id" || "$p2d_rearm_ws_id" == "null" ]]; then
	echo "FAIL  observation 13 setup: create p2d_rearm_ws (single POST, specId) on the recreated stack: want status 201, got ${p2d_rearm_ws_status}: $(cat "$BODY_FILE")"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13 setup: p2d_rearm_ws (id ${p2d_rearm_ws_id}) created under the 5s window -- the pre-existing session/CSRF still authenticate after --force-recreate (sessions are a SQLite table, on the volume that survived it)"
fi

# Call 1: the first labelled call on this workspace -- writes auto row A.
p2d_rearm1_status=$(p2c_set_settings "$p2d_rearm_ws_id" '.identity.name = "P2d Rearm 1"')
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_rearm_ws_id}/checkpoints" >/dev/null
p2d_rearm_count1=$(jq '[.checkpoints[] | select(.kind == "auto")] | length' "$BODY_FILE")
p2d_rearm_a_id=$(jq -r '[.checkpoints[] | select(.kind == "auto")][0].id // empty' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_rearm1_status" != "200" || "$p2d_rearm_count1" != "1" ]]; then
	echo "FAIL  observation 13 (call 1): the first labelled call under the 5s window -- want PATCH 200 and exactly one auto row, got PATCH ${p2d_rearm1_status} auto count ${p2d_rearm_count1}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13 (call 1): the first labelled call writes auto row A=${p2d_rearm_a_id}"
fi

# Call 2: a second labelled call, IMMEDIATELY after -- still inside the
# window, so it must write NONE. Suppression observed as an unchanged
# population AND an unchanged id, exactly like observation 10 on the main
# stack.
p2d_rearm2_status=$(p2c_set_settings "$p2d_rearm_ws_id" '.identity.name = "P2d Rearm 2"')
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_rearm_ws_id}/checkpoints" >/dev/null
p2d_rearm_count2=$(jq '[.checkpoints[] | select(.kind == "auto")] | length' "$BODY_FILE")
p2d_rearm2_id=$(jq -r '[.checkpoints[] | select(.kind == "auto")][0].id // empty' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_rearm2_status" != "200" || "$p2d_rearm_count2" != "1" || "$p2d_rearm2_id" != "$p2d_rearm_a_id" ]]; then
	echo "FAIL  observation 13 (call 2): a second labelled call immediately after call 1 -- want PATCH 200, still exactly one auto row, the SAME id (${p2d_rearm_a_id}); got PATCH ${p2d_rearm2_status} auto count ${p2d_rearm_count2} id ${p2d_rearm2_id}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13 (call 2): the immediate second call writes nothing -- still just row A=${p2d_rearm_a_id}"
fi

echo "== observation 13: sleeping 6s past the 5s window =="
sleep 6

# Call 3, AFTER the window: this is the whole point of the observation --
# the window RE-ARMS and a THIRD labelled call writes a SECOND auto row.
# Fails an implementation whose window never re-arms (one that writes an
# auto row only when NONE exists at all, treating any non-zero window as
# permanent): that bug passes observations 8, 9 and 10 on the main stack
# untouched, because none of them wait past a window -- this sleep is the
# one place in the whole run that actually does, on a window (5s) short
# enough to wait out without turning this script into a multi-minute one.
p2d_rearm3_status=$(p2c_set_settings "$p2d_rearm_ws_id" '.identity.name = "P2d Rearm 3"')
http_json GET "$ADMIN_HOST" "/api/workspaces/${p2d_rearm_ws_id}/checkpoints" >/dev/null
p2d_rearm_count3=$(jq '[.checkpoints[] | select(.kind == "auto")] | length' "$BODY_FILE")
p2d_rearm_b_id=$(jq -r '[.checkpoints[] | select(.kind == "auto")] | max_by(.id) | .id' "$BODY_FILE")
p2d_checks=$((p2d_checks + 1))
if [[ "$p2d_rearm3_status" != "200" || "$p2d_rearm_count3" != "2" || "$p2d_rearm_b_id" == "$p2d_rearm_a_id" ]]; then
	echo "FAIL  observation 13 (call 3, after 6s): the window should have RE-ARMED -- want PATCH 200, TWO auto rows now, a fresh id distinct from A=${p2d_rearm_a_id}; got PATCH ${p2d_rearm3_status} auto count ${p2d_rearm_count3} newest id ${p2d_rearm_b_id}"
	fail_count=$((fail_count + 1))
else
	echo "PASS  observation 13 (call 3, after 6s): the window re-armed -- a SECOND auto row B=${p2d_rearm_b_id} appeared, distinct from A=${p2d_rearm_a_id}"
fi

# The WHOLE P2d section's own total (observations 1-12's own 21 plus
# observation 13's own 4) -- outside any guard, at the true end of the
# section, the same silent-skip protection p2c_want_checks and this
# section's own earlier p2d_want_checks give their own blocks.
p2d_want_checks_all=25
if [[ "$p2d_checks" != "$p2d_want_checks_all" ]]; then
	echo "FAIL  P2d acceptance (whole section, observation 13 included): expected exactly ${p2d_want_checks_all} checks to have run, ${p2d_checks} actually ran -- observation 13's block was short-circuited somewhere before reaching here"
	fail_count=$((fail_count + 1))
else
	echo "PASS  P2d acceptance (whole section, observation 13 included): all ${p2d_checks} checks ran to completion"
fi


if ((fail_count > 0)); then
	echo "== ${fail_count} check(s) FAILED =="
	exit 1
fi

echo "== all checks PASSED =="
