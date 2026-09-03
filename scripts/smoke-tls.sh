#!/usr/bin/env bash
# The HTTPS acceptance check (A5): docker-compose.tls.yml's Caddy in front of
# mocker, the whole reverse-proxy contract of DESIGN §15/§16 that
# scripts/smoke.sh deliberately does not exercise (it talks plain http with
# a Host header). What this run observes, each as a numbered check:
#
#   1. the admin host answers over TLS with a chain that verifies against
#      Caddy's own root (curl --cacert fails the request otherwise);
#   2. login's Set-Cookie carries Secure, AND the container's environment
#      says MOCKER_DEV=0 — a trusted X-Forwarded-Proto alone would turn
#      the flag on, so the cookie by itself cannot prove the overlay's
#      override reached the process;
#   3. the workspace record's url is https://<slug>.<base>:<port> — the
#      scheme proves mocker BELIEVED X-Forwarded-Proto from Caddy, the port
#      that it read r.Host through the proxy unchanged;
#   4. the workspace host answers under the WILDCARD certificate, and the
#      leaf Caddy presents for it carries the `*.<base>` SAN — one
#      hostname answering could otherwise be a per-name leaf;
#   5. that request's traffic row carries fwdIp = the real client (the
#      host's gateway on the stack's subnet) and peerIp = Caddy's one
#      static address, the only peer MOCKER_TRUST_PROXY names — the
#      compensating control §15 wants behind a trusted proxy;
#   6. mocker's own :8080 is NOT published (`ports: !reset []` held);
#   7. a tick SSE stream delivers frames through Caddy before the client
#      hangs up (a buffering proxy would deliver none inside --max-time),
#      over HTTP/1.1 and over HTTP/2 separately — the two Caddy speaks on
#      the TCP port; HTTP/3 is not published (CARVE-OUTS.md);
#   8. a WebSocket upgrade over wss round-trips a reactive rule;
#   9. compose reports mocker healthy, and the RENDERED overlay still gates
#      Caddy on service_healthy — a healthy container alone would stay
#      green with the dependency deleted.
#
# Requires: docker compose >= 2.30, curl, jq, yq, openssl, python3
# (scripts/wsclient.py).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

export COMPOSE_ENV_FILES=/dev/null
export MOCKER_TLS_SUBNET="${MOCKER_TLS_SUBNET:-172.30.10.0/24}"
export MOCKER_TLS_PORT="${MOCKER_TLS_PORT:-8443}"
# The same /24 rule scripts/compose-tls.sh applies: .1 is the gateway (the
# host, as Caddy sees a connection from a published port), .254 is Caddy.
GATEWAY="${MOCKER_TLS_SUBNET%.0/24}.1"
CADDY_IP="${MOCKER_TLS_SUBNET%.0/24}.254"

for tool in jq yq curl python3 openssl; do
	command -v "$tool" >/dev/null || {
		echo "smoke-tls.sh requires ${tool}" >&2
		exit 1
	}
done

COMPOSE=./scripts/compose-tls.sh
ADMIN_HOST=$(sed -n 's/^MOCKER_ADMIN_HOST=//p' .env.example | tail -n 1)
BASE_DOMAIN=$(sed -n 's/^MOCKER_BASE_DOMAIN=//p' .env.example | tail -n 1)
PORT=$MOCKER_TLS_PORT
SMOKE_PASSWORD="mocker-smoke-tls-$$"

ENV_FILE=.env
ENV_BACKUP=""
# Set only once the developer's .env has been backed up (or was absent) AND
# our own copy is in place: cleanup must never rm a real .env it has no
# copy of, which is what an early failure would otherwise do.
env_replaced=0
ROOT_CRT=$(mktemp)
COOKIE_JAR=$(mktemp)
BODY_FILE=$(mktemp)
HDR_FILE=$(mktemp)
fail_count=0

cleanup() {
	local status=$?
	if ((status != 0)); then
		echo "== container logs (last 60 lines each), because the smoke failed =="
		$COMPOSE logs --tail 60 2>&1 || true
	fi
	echo "== tearing down =="
	$COMPOSE down -v || true
	if [[ -n "$ENV_BACKUP" ]]; then
		mv -f "$ENV_BACKUP" "$ENV_FILE"
	elif ((env_replaced)); then
		rm -f "$ENV_FILE"
	fi
	rm -f "$ROOT_CRT" "$COOKIE_JAR" "$BODY_FILE" "$HDR_FILE"
}
trap cleanup EXIT

ok() { echo "ok    $*"; }
fail() {
	echo "FAIL  $*"
	fail_count=$((fail_count + 1))
}

if [[ -f "$ENV_FILE" ]]; then
	ENV_BACKUP=$(mktemp)
	cp "$ENV_FILE" "$ENV_BACKUP"
fi
cp .env.example "$ENV_FILE"
env_replaced=1

# A tick every 500 ms is what check 7 counts; nothing else in .env.example
# is changed — the overlay itself is what turns MOCKER_DEV off and trust on,
# and proving THAT is the point of leaving the file at its defaults.

$COMPOSE down -v >/dev/null 2>&1 || true

echo "== building the image =="
docker compose build

echo "== generating a throwaway password hash =="
# Through the overlay wrapper too: a bare `docker compose run` would create
# the project's default network WITHOUT the overlay's fixed subnet first.
HASH=$($COMPOSE run --rm -T mocker hash-password "$SMOKE_PASSWORD")
grep -v '^MOCKER_SHARED_PASSWORD_HASH=' "$ENV_FILE" >"${ENV_FILE}.tmp"
mv "${ENV_FILE}.tmp" "$ENV_FILE"
printf 'MOCKER_SHARED_PASSWORD_HASH=%s\n' "$HASH" >>"$ENV_FILE"

echo "== bringing up the HTTPS stack =="
$COMPOSE up -d

echo "== waiting for Caddy's local root =="
tries=0
until $COMPOSE cp caddy:/data/caddy/pki/authorities/local/root.crt "$ROOT_CRT" >/dev/null 2>&1 && [[ -s "$ROOT_CRT" ]]; do
	tries=$((tries + 1))
	if ((tries >= 60)); then
		echo "FAIL  Caddy never wrote its local CA root in 60 s"
		exit 1
	fi
	sleep 1
done
ok "root.crt exported after ${tries} s"

# Every request below resolves the stack's names to loopback the way curl
# --resolve does, and verifies the chain against the exported root: a
# request that reaches the server with the wrong certificate FAILS here
# instead of being answered. --noproxy '*' because an HTTPS_PROXY in the
# environment (a dev box behind an egress proxy) would otherwise take the
# CONNECT for mocker.local:8443 and answer for the stack — the first run of
# this script hung on exactly that, with the stack up and healthy.
CURL=(curl -s --noproxy '*' --cacert "$ROOT_CRT" --resolve "${ADMIN_HOST}:${PORT}:127.0.0.1")
ADMIN="https://${ADMIN_HOST}:${PORT}"

echo "== waiting for ${ADMIN} =="
tries=0
until "${CURL[@]}" -f -o /dev/null "${ADMIN}/healthz"; do
	tries=$((tries + 1))
	if ((tries >= 60)); then
		echo "FAIL  ${ADMIN}/healthz never answered over TLS in 60 s"
		exit 1
	fi
	sleep 1
done

# curl's status/body split, with the cookie jar and the CSRF-relevant
# Origin (the admin host, https, port and all — originAllowed compares the
# hostname only, but a browser would send exactly this).
http_json() {
	local method=$1 path=$2 data=${3:-}
	shift $((($# < 3) ? $# : 3))
	local out status
	if [[ -n "$data" ]]; then
		out=$("${CURL[@]}" -c "$COOKIE_JAR" -b "$COOKIE_JAR" -D "$HDR_FILE" \
			-H 'Content-Type: application/json' -H "Origin: ${ADMIN}" \
			-w '\n%{http_code}' -X "$method" "$@" --data "$data" "${ADMIN}${path}")
	else
		out=$("${CURL[@]}" -c "$COOKIE_JAR" -b "$COOKIE_JAR" -D "$HDR_FILE" \
			-H "Origin: ${ADMIN}" -w '\n%{http_code}' -X "$method" "$@" "${ADMIN}${path}")
	fi
	status=${out##*$'\n'}
	printf '%s' "${out%$'\n'"$status"}" >"$BODY_FILE"
	printf '%s' "$status"
}

# 1. TLS on the admin host, verified against the local root.
status=$("${CURL[@]}" -o "$BODY_FILE" -w '%{http_code}' "${ADMIN}/healthz")
if [[ "$status" == "200" ]] && grep -qF '"ok":true' "$BODY_FILE"; then
	ok "1. ${ADMIN}/healthz answers 200 over TLS, chain verified against Caddy's root"
else
	fail "1. ${ADMIN}/healthz over TLS: status ${status}: $(cat "$BODY_FILE")"
fi

# 2. Login, and the cookie is Secure.
status=$(http_json POST /api/auth/login "{\"password\":\"${SMOKE_PASSWORD}\",\"name\":\"smoke-tls\"}")
if [[ "$status" != "200" ]]; then
	echo "FAIL  admin login over TLS: want 200, got ${status}: $(cat "$BODY_FILE")"
	exit 1
fi
csrf=$(jq -r '.csrfToken' "$BODY_FILE")
mocker_cid=$($COMPOSE ps -q mocker)
mocker_env=$(docker inspect --format '{{join .Config.Env "\n"}}' "$mocker_cid" 2>/dev/null || true)
if ! grep -i '^set-cookie:' "$HDR_FILE" | grep -qi 'secure'; then
	fail "2. login's Set-Cookie lacks Secure: $(grep -i '^set-cookie:' "$HDR_FILE")"
elif ! grep -qx 'MOCKER_DEV=0' <<<"$mocker_env"; then
	fail "2. Set-Cookie is Secure but the container's environment does not say MOCKER_DEV=0 (the trusted X-Forwarded-Proto alone would set the flag): $(grep '^MOCKER_DEV=' <<<"$mocker_env" || echo '<unset>')"
else
	ok "2. login's Set-Cookie carries Secure, and the container runs with MOCKER_DEV=0 (the overlay's override reached the process)"
fi

# 3. The workspace url is built from the trusted scheme and the proxied Host.
status=$(http_json POST /api/workspaces '{"name":"alex"}' -H "X-CSRF-Token: ${csrf}")
if [[ "$status" != "201" ]]; then
	echo "FAIL  create workspace: want 201, got ${status}: $(cat "$BODY_FILE")"
	exit 1
fi
slug=$(jq -r '.slug' "$BODY_FILE")
ws_id=$(jq -r '.id' "$BODY_FILE")
ws_url=$(jq -r '.url' "$BODY_FILE")
WS_HOST="${slug}.${BASE_DOMAIN}"
want_url="https://${WS_HOST}:${PORT}"
if [[ "$ws_url" == "$want_url" ]]; then
	ok "3. workspace url is ${ws_url} — X-Forwarded-Proto believed, Host passed through with its port"
else
	fail "3. workspace url is ${ws_url}, want ${want_url}"
fi

# 4. The workspace host under the wildcard certificate.
WCURL=(curl -s --noproxy '*' --cacert "$ROOT_CRT" --resolve "${WS_HOST}:${PORT}:127.0.0.1")
WS="https://${WS_HOST}:${PORT}"
status=$("${WCURL[@]}" -o "$BODY_FILE" -w '%{http_code}' "${WS}/__mocker/health")
# The SAN of the leaf Caddy presents for the workspace host, read with
# openssl rather than curl: curl verifies the chain (that is check 4's
# request succeeding at all) but does not say WHICH name the leaf carries,
# and a per-hostname leaf would verify just as well as the wildcard.
leaf_san=$(openssl s_client -connect "127.0.0.1:${PORT}" -servername "$WS_HOST" </dev/null 2>/dev/null |
	openssl x509 -noout -ext subjectAltName 2>/dev/null || true)
if [[ "$status" != "200" ]] || ! grep -qF "\"workspace\":\"${slug}\"" "$BODY_FILE"; then
	fail "4. ${WS}/__mocker/health: status ${status}: $(cat "$BODY_FILE")"
elif ! grep -qF "DNS:*.${BASE_DOMAIN}" <<<"$leaf_san"; then
	fail "4. ${WS} answered, but the leaf's SAN does not carry *.${BASE_DOMAIN}: ${leaf_san}"
else
	ok "4. ${WS}/__mocker/health answers 200 under the wildcard certificate (SAN carries *.${BASE_DOMAIN})"
fi

# 5. The traffic row names the real client, not the proxy. /__mocker/health
# is a control route and is not recorded, so make one ordinary request.
"${WCURL[@]}" -o /dev/null "${WS}/anything-at-all" || true
sleep 1
status=$(http_json GET "/api/workspaces/${ws_id}/traffic")
row_peer=$(jq -r '.rows[0].peerIp // ""' "$BODY_FILE")
row_fwd=$(jq -r '.rows[0].fwdIp // ""' "$BODY_FILE")
if [[ "$status" == "200" && "$row_fwd" == "$GATEWAY" && "$row_peer" == "$CADDY_IP" ]]; then
	ok "5. traffic row: fwdIp=${row_fwd} (the client), peerIp=${row_peer} (Caddy's static address) — MOCKER_TRUST_PROXY=${CADDY_IP} believed"
else
	fail "5. traffic row: status ${status}, peerIp='${row_peer}', fwdIp='${row_fwd}', want fwdIp=${GATEWAY} and peerIp=${CADDY_IP}"
fi

# 6. No plain-http door beside the TLS one. `docker port` prints one line
# per published port and nothing for a container with none; `docker compose
# port` is not usable here — it answers "invalid IP:0" with exit 0 for an
# unpublished port (measured, compose v5.5), which reads as a binding.
mocker_cid=$($COMPOSE ps -q mocker)
if [[ -z "$mocker_cid" ]]; then
	fail "6. no mocker container to inspect — an absence check must not pass on a missing subject"
elif published=$(docker port "$mocker_cid") && [[ -n "$published" ]]; then
	fail "6. mocker's :8080 is still published (${published}) — the overlay's ports: !reset [] did not hold"
else
	ok "6. mocker's :8080 is not published; Caddy is the only door"
fi

# 7. SSE through the proxy: frames must arrive before the client gives up.
tick=$(jq -n '{method: "GET", path: "/ticks", kind: "sse", stream: {tick: {intervalMs: 500, event: "price", schema: {type: "object", properties: {price: {type: "number"}}, required: ["price"]}}}}')
status=$(http_json POST "/api/workspaces/${ws_id}/endpoints" "$tick" -H "X-CSRF-Token: ${csrf}")
if [[ "$status" != "201" ]]; then
	fail "7. create the sse endpoint: want 201, got ${status}: $(cat "$BODY_FILE")"
else
	# Once per protocol Caddy speaks on the TCP port: a proxy can flush on
	# one and buffer on the other (h2 frames are multiplexed and easier to
	# batch), and curl's own negotiation would otherwise pick one silently.
	for proto in --http1.1 --http2; do
		"${WCURL[@]}" "$proto" -N --max-time 3 -o "$BODY_FILE" "${WS}/ticks" || true
		frames=$(grep -c '^event: price' "$BODY_FILE" || true)
		if ((frames >= 3)); then
			ok "7. SSE (${proto#--}): ${frames} tick frames arrived through Caddy within 3 s (no proxy buffering)"
		else
			fail "7. SSE (${proto#--}): only ${frames} tick frames arrived through Caddy within 3 s: $(head -c 300 "$BODY_FILE")"
		fi
	done
fi

# 8. WebSocket through the proxy, over wss.
chat=$(jq -n '{method: "GET", path: "/chat", kind: "ws", stream: {reactive: [{when: [{in: "body", name: "op", op: "equals", value: "ping"}], data: {op: "pong"}}]}}')
status=$(http_json POST "/api/workspaces/${ws_id}/endpoints" "$chat" -H "X-CSRF-Token: ${csrf}")
if [[ "$status" != "201" ]]; then
	fail "8. create the ws endpoint: want 201, got ${status}: $(cat "$BODY_FILE")"
else
	ws_out=$(python3 scripts/wsclient.py --url "wss://${WS_HOST}:${PORT}/chat" --connect 127.0.0.1 --cacert "$ROOT_CRT" \
		--send '{"op":"ping"}' --expect 1 --timeout 5 2>&1 || true)
	if grep -q '^status 101' <<<"$ws_out" && grep -q 'text {"op":"pong"}' <<<"$ws_out"; then
		ok "8. WebSocket: wss upgrade through Caddy, ping answered by the reactive rule"
	else
		fail "8. WebSocket through Caddy: ${ws_out}"
	fi
fi

# 8b (A6, decisions.md mocker-a6-assets A12): an asset uploaded through
# Caddy reports an https url with the proxy's port, and that url fetches
# 200 through Caddy with the strong ETag — the one thing the plain smoke
# cannot observe about assets, the scheme and port a real deployment's
# frontend would be handed.
a6_status=$("${CURL[@]}" -c "$COOKIE_JAR" -b "$COOKIE_JAR" -o "$BODY_FILE" -w '%{http_code}' -X PUT \
	-H 'Content-Type: image/png' -H "Origin: ${ADMIN}" -H "X-CSRF-Token: ${csrf}" \
	--data-binary 'PNG-through-caddy' "${ADMIN}/api/workspaces/${ws_id}/assets/logo.png")
a6_url=$(jq -r '.url // ""' "$BODY_FILE")
a6_sha=$(jq -r '.sha256 // ""' "$BODY_FILE")
if [[ "$a6_status" != "201" || "$a6_url" != "https://${WS_HOST}:${PORT}/__mocker/assets/logo.png" ]]; then
	fail "8b. upload through Caddy: status ${a6_status}, url '${a6_url}', want 201 and https://${WS_HOST}:${PORT}/__mocker/assets/logo.png"
else
	a6_get=$("${WCURL[@]}" -o "$BODY_FILE" -D "$HDR_FILE" -w '%{http_code}' "$a6_url")
	a6_etag=$(grep -i '^etag:' "$HDR_FILE" | tr -d '\r' | awk '{print $2}')
	if [[ "$a6_get" == "200" && "$(cat "$BODY_FILE")" == "PNG-through-caddy" && "$a6_etag" == "\"${a6_sha}\"" ]]; then
		ok "8b. the asset's url fetches 200 through Caddy with ETag ${a6_etag}"
	else
		fail "8b. GET ${a6_url} through Caddy: status ${a6_get}, etag '${a6_etag}', body '$(head -c 60 "$BODY_FILE")'"
	fi
fi

# 9. Compose's own health view — the state Caddy's depends_on waited for —
# and the dependency itself, read off the RENDERED config: the state alone
# would stay healthy with the depends_on line deleted.
health_state=$(docker inspect --format '{{.State.Health.Status}}' "$mocker_cid" 2>/dev/null || true)
dep_cond=$($COMPOSE config 2>/dev/null | yq -r '.services.caddy.depends_on.mocker.condition // ""' 2>/dev/null || true)
if [[ "$health_state" != "healthy" ]]; then
	fail "9. compose reports mocker '${health_state}', want healthy"
elif [[ "$dep_cond" != "service_healthy" ]]; then
	fail "9. mocker is healthy, but the rendered overlay gates caddy on '${dep_cond}', want service_healthy"
else
	ok "9. compose reports mocker healthy (mocker healthcheck), and the rendered overlay gates caddy on service_healthy"
fi

echo
if ((fail_count > 0)); then
	echo "${fail_count} check(s) FAILED"
	exit 1
fi
echo "all HTTPS checks passed"
