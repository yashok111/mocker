#!/usr/bin/env bash
# Ensure the STABLE local CA root exists: ./.tls-ca/root.crt + root.key,
# generated once per checkout and bind-mounted read-only into Caddy
# (docker-compose.tls.yml), which provisions it as the `tls internal`
# CA root via the `pki` block in deploy/Caddyfile.
#
# Why a host-side root at all: Caddy's own root lives in the caddy-data
# volume, and a volume is mortal (`docker volume prune`, `down -v`, a disk
# cleanup). A fresh volume made Caddy mint a FRESH root, and every browser,
# keychain and NODE_EXTRA_CA_CERTS that trusted the old one started failing
# with "unable to get local issuer certificate" — and every such re-issue
# also left one more trusted entry piling up in the keychain. A root that
# lives outside every volume never changes, so trust is installed ONCE.
#
# Idempotent: existing files are left exactly as they are. Called by
# `make tls-init`, by compose-tls.sh before any `up`, and mirrored in Go by
# ensureLocalCA (cmd/mocker/setup_ca.go) for `mocker setup`, which cannot
# assume bash.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

dir=.tls-ca
if [[ -f $dir/root.crt && -f $dir/root.key ]]; then
	exit 0
fi

mkdir -p "$dir"
# EC P-256, 10 years: a dev root outlives any volume and any laptop battery;
# the intermediate and the leaf certificates stay Caddy-managed in the
# volume and are re-issued as needed — only the ROOT has to be stable,
# because the root is the only thing anyone trusts.
openssl ecparam -name prime256v1 -genkey -noout -out "$dir/root.key"
openssl req -x509 -new -key "$dir/root.key" -sha256 -days 3650 \
	-subj "/CN=mocker Local Authority - stable root" \
	-addext "basicConstraints=critical,CA:TRUE" \
	-addext "keyUsage=critical,keyCertSign,cRLSign" \
	-out "$dir/root.crt"
chmod 600 "$dir/root.key"
