#!/usr/bin/env bash
# Create .env for the compose stack in one step: copy .env.example and
# replace the placeholder password hash with a real argon2id one, minted by
# the image's own `hash-password` subcommand — so the quick start needs
# docker and nothing else, no Go toolchain on the host. `make up` runs this
# itself when .env is missing; `make init [PASSWORD=…]` runs it by hand.
#
# The password comes from the first argument, else $MOCKER_PASSWORD, else
# it is generated and printed ONCE at the end — there is no way to read it
# back out of the hash, and that is the point of storing a hash.
#
# An existing .env is never touched: the file holds a deployment's real
# configuration and its hash, and overwriting it because someone typed
# `make init` twice is the wrong default. Delete it, or edit it, on purpose.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# .env holds the argon2id verifier of the admin password — an offline
# cracking target for any other local user if it were world-readable.
umask 077

export COMPOSE_ENV_FILES=/dev/null

if [[ -f .env ]]; then
	echo "init-env.sh: .env already exists — leaving it alone."
	echo "  To change the password: make hash-password PASSWORD=… and paste the hash into .env."
	echo "  To start over: rm .env && make init"
	exit 0
fi

password=${1:-${MOCKER_PASSWORD:-}}
generated=0
if [[ -z "$password" ]]; then
	# 18 random bytes → 24 base64 chars; the three URL-unfriendly characters
	# are dropped so the value pastes anywhere without quoting.
	password=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')
	generated=1
fi

cp .env.example .env
# From here on .env exists with the PLACEHOLDER hash, and compose needs the
# file to exist before `run` can mint the real one (env_file). If either
# of the next two steps fails, a half-made .env must not survive: the
# server would start (config only checks the hash is non-empty), every
# login would 401, `make up` would consider the file done and `make init`
# would refuse to touch it. Remove it on any failure, so the next `make up`
# starts over.
trap '[[ $? -eq 0 ]] || { rm -f .env .env.tmp; echo "init-env.sh: failed — .env removed, run make init again" >&2; }' EXIT

echo "== building the image (needed once, to mint the hash) =="
docker compose build

echo "== hashing the password =="
# On stdin, never as an argument: an argument sits in the argv of the
# docker CLI and of the container process for the whole argon2 run, and
# `ps` on the host shows both. hash-password reads one line from stdin
# when given none (its "Password: " prompt goes to stderr, not the hash —
# stderr is left alone so a failing build or run says why).
hash=$(printf '%s\n' "$password" | docker compose run --rm -T mocker hash-password)
# shellcheck disable=SC2016 # the literal `$argon2id$` prefix is the point
if [[ "$hash" != '$argon2id$'* ]]; then
	echo "init-env.sh: hash-password printed something that is not a PHC string: ${hash}" >&2
	exit 1
fi

# In place, so the value stays under its own comment in the file; awk -v
# would interpret backslashes in the value, but a PHC string has none
# (base64 plus $ , = only), and neither sd nor sed is safe with a value
# that is mostly `$` signs.
awk -v h="$hash" '/^MOCKER_SHARED_PASSWORD_HASH=/ { print "MOCKER_SHARED_PASSWORD_HASH=" h; next } { print }' .env >.env.tmp
mv .env.tmp .env

echo
echo ".env written."
if ((generated)); then
	echo "Admin password (generated, shown once): ${password}"
else
	echo "Admin password: the one you supplied."
fi
echo "Next: make up        (http://127.0.0.1:8080, Host: $(sed -n 's/^MOCKER_ADMIN_HOST=//p' .env | tail -n 1))"
echo "      make up-tls    (https on 127.0.0.1:8443 through Caddy — see README, 'HTTPS')"
