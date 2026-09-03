# Developer entry points for mocker. Kept deliberately dumb: every target is
# a couple of commands, and anything with real logic (the smoke test) lives
# in its own script instead of growing inside this file.

BINARY := bin/mocker

# Keep compose from ALSO auto-loading .env as the project env file (it is
# already loaded, raw, as the service's env_file — see docker-compose.yml).
# When compose loads it that second way it interpolates the file against
# itself and prints a warning per `$` in the argon2id hash — cosmetic, since
# nothing in docker-compose.yml substitutes a MOCKER_* variable, but the text
# ("The \"argon2id\" variable is not set") reads exactly like the real
# password-mangling bug it is not.
export COMPOSE_ENV_FILES := /dev/null

# CAP runs a heavy target inside its own memory-capped systemd user scope, so
# that when something has to die under memory pressure the kernel takes THIS
# command and not the login session around it.
#
# Measured on the 8 GB dev box, 2026-08-18: one package under -race peaks at
# ~650 MB RSS, `go test ./...` runs up to GOMAXPROCS package binaries at once
# (~2.6 GB on 4 cores), `docker build` compiles the same module again inside
# BuildKit, and several editor/agent processes sit on another ~1.5 GB. The
# kernel OOM killer resolved that twice in one day by wiping the whole user
# slice — tmux, dbus, and every background service with it (journalctl:
# "session.slice: The kernel OOM killer killed some processes in this unit").
# A capped scope turns "the desktop session vanished" into "this target
# failed", which is a bug report instead of a mystery.
#
# It is applied to `test` and `ui`, and NOT to `docker`/`smoke`, on purpose.
# yarn install and vite build (ui) are ordinary client processes in this same user
# slice — the exact OOM exposure `go test` has — so they get the same cap. A
# compose build (docker/smoke) instead spends its memory inside dockerd,
# which is a SYSTEM service in its own slice, so a cap on the client process
# would protect nothing while looking like it did. Capping that side needs a
# MemoryMax on docker.service (root), or BuildKit's own limits — not this
# file.
#
# Degrades to nothing where systemd-run is absent (macOS, most CI images) or
# where the user slice has no delegated memory controller, so the targets keep
# working unchanged; check delegation with
#   cat /sys/fs/cgroup/user.slice/user-$$(id -u).slice/cgroup.controllers
CAP_MEM ?= 3G
CAP_SWAP ?= 1G
CAP := $(shell command -v systemd-run >/dev/null 2>&1 && \
	grep -qw memory /sys/fs/cgroup/user.slice/user-$$(id -u).slice/cgroup.controllers 2>/dev/null && \
	echo systemd-run --user --scope --quiet -p MemoryMax=$(CAP_MEM) -p MemorySwapMax=$(CAP_SWAP) --)

.PHONY: build run release dist ui ui-dev ui-gen ui-lint ui-test plugin-test plugin-build plugin-pack guide-sync test lint fmt docker init up down up-tls down-tls tls-root smoke smoke-tls hash-password clean

build: ## Build the mocker binary into ./bin
	mkdir -p bin
	go build -trimpath -o $(BINARY) ./cmd/mocker

run: build ## Run mocker locally; export the vars from .env.example first
	./$(BINARY)

# `build` deliberately does NOT depend on `ui`: the inner dev loop rebuilds Go
# dozens of times an hour and has no reason to re-run yarn each time, and a
# binary with only .gitkeep under internal/webui/dist still starts and answers
# one honest 503 for the UI.
#
# `release` exists because that honesty runs out for a binary handed to someone
# else: an internal/webui/dist left over from an older `make ui` is embedded
# silently, and since the API client is now GENERATED from api/openapi.json, a
# stale dist means a UI built against an older contract with nothing to say so.
# Depending on `ui` makes the distributable path rebuild the SPA from source
# every time; `make docker` gets the same property from its own node stage plus
# the .dockerignore rule that keeps any local dist out of the build context.
release: ui build ## Build a distributable binary, rebuilding the SPA from source first

# The six targets colleagues run `mocker setup` from. CGO_ENABLED=0 is not
# a choice here: modernc sqlite is pure Go, and a cgo build would need a C
# cross-toolchain per target. Depends on ui because the SPA is embedded.
DIST_TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
dist: ui ## Cross-compile bin/mocker-<os>-<arch>[.exe] for every colleague OS (Linux, macOS, Windows; amd64 and arm64)
	mkdir -p bin
	@for t in $(DIST_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		echo "building bin/mocker-$$os-$$arch$$ext"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -o bin/mocker-$$os-$$arch$$ext ./cmd/mocker || exit 1; \
	done

# yarn 4 through corepack, not a globally installed yarn: web/package.json
# pins packageManager, and corepack is what makes that pin actually decide
# which yarn runs. `--immutable` is npm ci's equivalent — it installs exactly
# what yarn.lock pins and fails on any mismatch instead of silently updating
# it, which is why the lockfile is committed at all.
#
# `cd web &&` rather than an --cwd/--prefix flag: yarn 4 has no such flag, and
# each recipe line is its own shell, so the cd cannot leak into another target.
ui: ## Build the SPA (yarn install + gen + vite build under web/) and embed it via go:embed; memory-capped (see CAP above)
	$(CAP) sh -c 'cd web && corepack yarn install --immutable'
	$(CAP) sh -c 'cd web && corepack yarn build'
	mkdir -p internal/webui/dist
	touch internal/webui/dist/.gitkeep

ui-dev: ## Run Vite's dev server, proxying /api,/healthz,/readyz to a mocker already running on :8080 (VITE_ADMIN_HOST, default mocker.local, rewrites Host+Origin — see web/vite.config.ts)
	cd web && corepack yarn dev

ui-gen: ## Regenerate the API client from api/openapi.json (orval) and the route tree (tsr). Run after changing the contract or adding a route file.
	cd web && corepack yarn gen

ui-lint: ## oxlint + oxfmt --check over web/
	cd web && corepack yarn lint && corepack yarn format:check

guide-sync: ## Copy skills/mocker/ (the one owner of the agent guide) into internal/guide/ for go:embed; internal/guide's test fails on drift
	cp skills/mocker/SKILL.md internal/guide/overview.md
	for f in tools shapes cookbook http design; do cp skills/mocker/references/$$f.md internal/guide/$$f.md; done

ui-test: ## vitest + tsc --noEmit over web/
	$(CAP) sh -c 'cd web && corepack yarn typecheck && corepack yarn test'

plugin-test: build ## @yashok111/mocker-test (packages/mocker-test): install, typecheck, lint, vitest against ./bin/mocker; memory-capped
	$(CAP) sh -c 'cd packages/mocker-test && corepack yarn install --immutable && corepack yarn typecheck && corepack yarn lint && corepack yarn test'

plugin-build: ## Build @yashok111/mocker-test into packages/mocker-test/dist via tsdown (ESM + CJS + d.ts)
	$(CAP) sh -c 'cd packages/mocker-test && corepack yarn install --immutable && corepack yarn build'

plugin-pack: ## Tarball of @yashok111/mocker-test for hand distribution: packages/mocker-test/yashok111-mocker-test-<version>.tgz (npm pack runs the tsdown build itself through prepack)
	$(CAP) sh -c 'cd packages/mocker-test && corepack yarn install --immutable && npm pack'

# test/lint/fmt are scoped to ./cmd/... and ./internal/..., NEVER ./... or a
# bare ".": web/node_modules now sits inside this Go module, nothing stops
# the go tool walking it, and one .go file shipped by a transitive npm
# dependency would fail `go build ./...`/`go vet ./...`/`gofmt -l .` for
# every developer here — unfixably, since it isn't this project's file to
# fix — while `gofmt -w .` would go further and REWRITE that dependency's
# own files in place. The scope also can't come from `git ls-files`: a run
# that hasn't committed yet leaves its new .go files untracked, and a
# file-list form is blind to those — measured, and it means an unformatted
# new file would pass the bar.
# TEST_P is `go test -p`: how many package test binaries build and run at
# once. 2 is the dev box's number (see CAP above: ~650 MB per package under
# -race); CI passes 4, its cap is 10G and the suite is CPU-bound there.
#
# The -gcflags leaves modernc.org/... (sqlite and its libc) UNINSTRUMENTED
# by the race detector. That package is C translated to Go, so every C
# memory access becomes an instrumented Go read, and a CPU profile of
# internal/admin under -race put 72% of all samples inside the race
# runtime, most of it there — measured 2026-09-03, the whole suite went
# 127 s → 91 s wall and CPU −30% with the flag. Our own concurrency around
# sqlite (internal/store's single writer and reader pool, every repository)
# stays fully instrumented; a data race inside sqlite's own translated
# code is upstream's to find. Do NOT extend the pattern to
# golang.org/x/crypto/argon2: tried, the detector then reports FALSE races
# on the hash goroutines' results in three packages.
TEST_P ?= 2
test: ## Test suite scoped to ./cmd ./internal, race detector, memory-capped (see CAP above and the comment above)
	$(CAP) go test ./cmd/... ./internal/... -race -count=1 -p $(TEST_P) -gcflags='modernc.org/...=-race=false'

lint: ## go vet, gofmt and golangci-lint, scoped to ./cmd ./internal, memory-capped (see CAP above and the comment above test)
	$(CAP) go vet ./cmd/... ./internal/...
	@unformatted="$$(gofmt -l ./cmd ./internal)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt -l found unformatted files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@# golangci-lint v2 (.golangci.yml), the same set the owner's other backend runs.
	@# Explicit package paths for the reason `test` has them: this module
	@# contains web/node_modules, and ./... would walk it. Skipped with a loud
	@# line rather than a hard failure when the binary is absent, so a fresh
	@# clone can still run `make lint` for vet+gofmt. Under $(CAP) since
	@# P6c: measured 2026-09-02, an uncapped golangci-lint run right after a
	@# capped `make test` took the whole tmux scope to a 5.8 GB peak and the
	@# kernel OOM killer took the scope — the session included — twice in
	@# one afternoon (journalctl --user: "tmux-spawn-….scope: Failed with
	@# result 'oom-kill'", 10:54 and 11:10). The cap turns that into a
	@# failed lint, which is the failure a bar is allowed to have.
	@if command -v golangci-lint >/dev/null 2>&1; then \
		$(CAP) golangci-lint run ./cmd/... ./internal/...; \
	else \
		echo "golangci-lint not found — skipping (install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)"; \
	fi

fmt: ## Reformat ./cmd and ./internal with gofmt, never the whole tree (see the comment above test)
	gofmt -w ./cmd ./internal

docker: ## Build the docker-compose image (mocker:local), UI included — see the Dockerfile's webbuilder stage
	docker compose build

# `up` depends on the FILE .env, so a fresh clone's first `make up` runs
# init-env.sh (copy .env.example, mint a real hash with the image's own
# hash-password, print the generated password once) and then brings the
# stack up — one command, docker the only requirement. An existing .env is
# never rewritten by any target here; `make init` on one says so and exits 0.
# PASSWORD goes through the environment, quoted: as a bare argument a
# password with a space would be split into two and only its first word
# hashed, with the script then reporting "the one you supplied".
init: ## Create .env from .env.example with a fresh password hash. Usage: make init [PASSWORD=…]; without PASSWORD one is generated and printed once
	MOCKER_PASSWORD='$(PASSWORD)' ./scripts/init-env.sh

.env:
	MOCKER_PASSWORD='$(PASSWORD)' ./scripts/init-env.sh

up: .env ## Bring the stack up in the background (creates .env first if missing — see init; stops a TLS stack first)
	docker compose down --remove-orphans
	docker compose up -d --build

down: ## Stop the stack, keeping the data volume (add -v by hand to drop it)
	docker compose down

# The HTTPS overlay (docker-compose.tls.yml): Caddy in front with its own
# local CA, mocker's :8080 unpublished, MOCKER_DEV=0 and MOCKER_TRUST_PROXY
# set to Caddy's one static address on a fixed /24. Every target goes
# through scripts/compose-tls.sh because the overlay needs the two host
# names out of .env exported for interpolation — see that script. Knobs:
# MOCKER_TLS_PORT (8443), MOCKER_TLS_SUBNET (172.30.10.0/24, an IPv4 /24
# ending in .0).
#
# Both `up` and `up-tls` start with a `down --remove-orphans` of the same
# project (volumes kept): the two are one compose project with two
# networks in mind, and a network left over from the other one is
# reused silently — Caddy would then sit on a subnet MOCKER_TRUST_PROXY
# does not name. In the base project `--remove-orphans` is what takes the
# overlay's caddy container down too.
up-tls: .env ## Bring the stack up behind Caddy on https://127.0.0.1:8443 (creates .env first if missing; stops a plain stack first)
	docker compose down --remove-orphans
	./scripts/compose-tls.sh up -d --build

down-tls: ## Stop the HTTPS stack, keeping the data and CA volumes (add -v by hand to drop them)
	./scripts/compose-tls.sh down

tls-root: ## Export Caddy's local CA root as ./mocker-root.crt, the file to trust in a browser or OS (stack must be up)
	./scripts/compose-tls.sh cp caddy:/data/caddy/pki/authorities/local/root.crt mocker-root.crt
	@echo "wrote mocker-root.crt — import it into the browser/OS trust store, and point $$(sed -n 's/^MOCKER_ADMIN_HOST=//p' .env) and *.$$(sed -n 's/^MOCKER_BASE_DOMAIN=//p' .env) at 127.0.0.1 in /etc/hosts"

smoke: ## Bring up the compose stack, run every acceptance check, tear down
	./scripts/smoke.sh

smoke-tls: ## Bring up the HTTPS stack, check TLS, the proxy contract, SSE and WebSocket through Caddy, tear down
	./scripts/smoke-tls.sh

hash-password: ## Print an argon2id hash for MOCKER_SHARED_PASSWORD_HASH. Usage: make hash-password PASSWORD=your-password (omit PASSWORD to type it on stdin instead)
	go run ./cmd/mocker hash-password $(PASSWORD)

clean: ## Remove local build artefacts (does not touch the docker volume)
	rm -rf bin dist mocker *.test *.out
