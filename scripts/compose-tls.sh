#!/usr/bin/env bash
# `docker compose` with the HTTPS overlay applied: the one place the overlay
# is ever invoked from, because docker-compose.tls.yml needs three values
# exported into compose's interpolation environment that nothing else
# provides. MOCKER_BASE_DOMAIN and MOCKER_ADMIN_HOST are read out of .env —
# ALWAYS from the file, never from the caller's shell, so Caddy's site
# blocks cannot disagree with the names mocker itself was started with —
# and MOCKER_TLS_SUBNET / MOCKER_TLS_PORT are the overlay's own two knobs,
# taken from the shell with defaults (the gateway and Caddy's static
# address are derived from the subnet here, the one place that arithmetic
# is written). Everything after the script name goes
# to compose verbatim: `scripts/compose-tls.sh up -d --build`, `… down -v`,
# `… cp caddy:/data/caddy/pki/authorities/local/root.crt mocker-root.crt`.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# The same reason the Makefile and smoke.sh set it: .env is the service's
# raw env_file already, and compose's second, interpolating read of it
# prints one bogus "variable is not set" warning per `$` in the hash.
export COMPOSE_ENV_FILES=/dev/null

if [[ ! -f .env ]]; then
	echo "compose-tls.sh: no .env — run 'make init' first" >&2
	exit 1
fi

env_value() {
	# Last assignment wins, like the shell; a value is everything after the
	# first '='. No quoting is honoured, because none is in .env.example and
	# docker's own env_file parser honours none either.
	sed -n "s/^$1=//p" .env | tail -n 1
}

MOCKER_BASE_DOMAIN=$(env_value MOCKER_BASE_DOMAIN)
MOCKER_ADMIN_HOST=$(env_value MOCKER_ADMIN_HOST)
if [[ -z "$MOCKER_BASE_DOMAIN" || -z "$MOCKER_ADMIN_HOST" ]]; then
	echo "compose-tls.sh: .env must set MOCKER_BASE_DOMAIN and MOCKER_ADMIN_HOST" >&2
	exit 1
fi
# Both land in deploy/Caddyfile as {$VAR} at PARSE time, so a value with
# whitespace or a brace would become Caddy configuration, not a host name.
for name in MOCKER_BASE_DOMAIN MOCKER_ADMIN_HOST; do
	if ! [[ "${!name}" =~ ^[A-Za-z0-9.-]+$ ]]; then
		echo "compose-tls.sh: ${name}='${!name}' is not a bare host name" >&2
		exit 1
	fi
done
export MOCKER_BASE_DOMAIN MOCKER_ADMIN_HOST

# A /24 only: the gateway is its .1 and Caddy's static address its .254,
# and MOCKER_TRUST_PROXY is set to that ONE address — the subnet would
# include the gateway, which is the docker host, and mocker's unpublished
# :8080 is still reachable from the host on the bridge. .254 and not .2
# because docker hands dynamic addresses out from the low end: mocker
# starts first (Caddy depends on it) and took .2 on the first run, and
# Caddy then failed with "Address already in use".
export MOCKER_TLS_SUBNET="${MOCKER_TLS_SUBNET:-172.30.10.0/24}"
if ! [[ "$MOCKER_TLS_SUBNET" =~ ^([0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3})\.0/24$ ]]; then
	echo "compose-tls.sh: MOCKER_TLS_SUBNET='${MOCKER_TLS_SUBNET}' must be an IPv4 /24 ending in .0" >&2
	exit 1
fi
export MOCKER_TLS_GATEWAY="${BASH_REMATCH[1]}.1"
export MOCKER_TLS_CADDY_IP="${BASH_REMATCH[1]}.254"
export MOCKER_TLS_PORT="${MOCKER_TLS_PORT:-8443}"

exec docker compose -f docker-compose.yml -f docker-compose.tls.yml "$@"
