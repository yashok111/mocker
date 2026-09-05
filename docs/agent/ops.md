# Operations: CI, test speed, the smokes, compose and TLS, the install wizard, publication, the linter census, goleak — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

All seven run on every push and pull request as `.github/workflows/ci.yml`
(`A17`, "Architecture"): six jobs (lint and the race suite are two — the
suite is the critical path), the non-docker ones inside a memory- and
CPU-capped systemd scope through `scripts/ci-cap.sh` — pass `CAP=` to make
there so the Makefile's own cap does not nest. A release of the plugin is a
tag `plugin-v<version>` (`.github/workflows/release-plugin.yml`).

**`make test` is CPU-bound under `-race`, and three things keep it at ~1
minute instead of two (measured 2026-09-03: 127 s → 57 s wall, 552 → 100
CPU-seconds locally; CI's step went from 278 s).** `-gcflags='modernc.org/
...=-race=false'` leaves sqlite's C-translated code uninstrumented — 72% of
the admin package's samples were the race runtime, mostly there — while
`internal/store` and every repository stay instrumented (the Makefile's
comment says why the pattern must NOT grow to `argon2`: false positives).
`internal/testauth` is the one owner of the fixture password and a
PRE-MINTED argon2id hash at m=4 MiB: hashing at fixture time cost ~110 ms
per test server under `-race`, and the PHC string is self-describing, so
`VerifyPassword` reads the cheaper parameters from it and no production
code knows. And `TEST_P` (`-p`, 2 on the dev box, 4 in CI where the cap is
10G) is what lets the linker and the small packages fill the cores the
big ones leave idle.

**The two smokes are the critical path now (measured 2026-09-03: the http
smoke job 332 s → 227 s).** CI builds `mocker:local` once per job through
buildx with a cross-run layer cache (`cache-from/to: type=gha`) and hands it
to the script under `SMOKE_PREBUILT=1`, which skips `docker compose build`
and insists the image is loaded; the Dockerfile's `--mount=type=cache`
directories are not layers, so `buildkit-cache-dance` carries the Go
module and build caches in and out of the builder's mounts under a
go.sum-keyed `actions/cache` (warm: `go build` 32 s → 0.5 s, the whole
image step 59 s → 37 s; what remains is the SPA build, which reruns even
with every layer above it a hit — unexplained, 17 s). Inside the
script, 120 of its 328 s were one `curl -X HEAD` waiting for a body that a
HEAD never sends — `--head` now — and what is left (~85 s of its 155) is
real time the checks are ABOUT: the 30 s `WriteTimeout` a stream must
outlive, `MOCKER_STREAM_MAX_LIFETIME=20` closing a tick stream, the ping
and debounce windows. When a smoke section looks slow, take the CI log's
timeline between consecutive `==`/`PASS` lines first: that is how both of
these were found.

**`A5` (2026-09-02) made the compose stack one command and the reverse-proxy
contract a test.** Three things about it are not visible from the files.
`up` depends on the FILE `.env`, so a fresh clone's first `make up` runs
`scripts/init-env.sh` (copy the example, build, mint the hash with the
image's own `hash-password`, print the generated password once) and an
existing `.env` is never rewritten by any target — `make init` on one says so
and exits 0. `docker-compose.tls.yml` is an OVERLAY, never run bare: it needs
`MOCKER_BASE_DOMAIN` and `MOCKER_ADMIN_HOST` exported for `${…}`
interpolation, and `.env` is loaded `format: raw` (not interpolated) as
mocker's env_file only — `scripts/compose-tls.sh` reads the two names out of
the file (always the file, never the caller's shell, so Caddy's site blocks
cannot disagree with the names mocker started with) and refuses to give
Caddy the whole `.env`, which would hand the password hash to a second
container. The overlay forces `MOCKER_DEV=0`, gives Caddy a STATIC address on a
FIXED /24 (`MOCKER_TLS_SUBNET`, 172.30.10.0/24; gateway .1, Caddy .254 —
mocker starts first and takes .2 dynamically — both derived in
`compose-tls.sh` and nowhere else) and sets `MOCKER_TRUST_PROXY`
to that ONE address — never the subnet: an address or CIDR is the only
shape that variable takes, a service name is not one, and the subnet's .1
is the docker HOST, from which mocker's unpublished :8080 is still
reachable on the bridge, so trusting the subnet would let any local
process forge `X-Forwarded-For` into the traffic log and rotate the login
limiter's key per attempt (the security review's one major finding) — and
empties mocker's published ports with YAML `!reset` (compose ≥ 2.24; an
overlay otherwise APPENDS, leaving a plain-http door with a Secure cookie
nobody could log in over). `deploy/Caddyfile` pins `X-Forwarded-For`/
`-Proto` with `header_up` rather than relying on Caddy's default, and
carries no HSTS on purpose (`CARVE-OUTS.md`, `A5`). `deploy/Caddyfile` replaced `Caddyfile.example` — the old file said
"ILLUSTRATIVE ONLY — not exercised" and it was; the new one is what
`make smoke-tls` runs, `tls internal` issuing the wildcard from Caddy's own CA
at startup. Two facts the first run paid for: the dev box exports
`HTTPS_PROXY` (a local egress proxy), and a `curl --resolve` to
loopback still sends a CONNECT for `mocker.local:8443` through it — every
curl in `smoke-tls.sh` carries `--noproxy '*'`, and any new https check
must too; and `docker compose port` answers `invalid IP:0` with exit 0 for
an UNPUBLISHED port (compose v5.5), so "is :8080 still published" is asked
of `docker port <cid>`, which prints nothing.

**`A16` (2026-09-03) is `mocker setup`, the install wizard for a
colleague's own machine — the owner's choice after `A15`: every colleague
runs their own mocker for local development, all three OS, the image
built from the checkout, the wizard a subcommand of the one binary.**
`cmd/mocker/setup.go` (+ `setup_env.go`, `setup_compose.go`,
`setup_platform.go`) does what README's "HTTPS" section did by hand:
docker/compose ≥ 2.30 checked; `.env` rendered from `.env.example` with
`auth.HashPassword` in-process (no `docker run` to mint the hash),
`MOCKER_ROUTING=path` fixed (one machine has no wildcard DNS), an
optional `MOCKER_MCP_KEY`, and an existing `.env` never touched
(`scripts/init-env.sh`'s rule); `scripts/compose-tls.sh` re-implemented
as `composeEnv` (the same subnet arithmetic, the same bare-host check,
`COMPOSE_ENV_FILES` pointed at the null device) because bash is not a
given on macOS or Windows; `docker compose up -d --build` through the
overlay pair; the CA root exported with a retry while Caddy starts;
readiness through `probe.ReadinessTLS` — the probe package's THIRD
caller, the same client with one trusted root and the admin host as TLS
ServerName, because the wizard dials loopback; the hosts line and the
trust-store install as per-OS PLANS (`trustPlan`, `hostsCommand`:
Debian/RHEL anchors, `security add-trusted-cert`, `certutil -addstore`)
that are tested on every OS from this one and degrade to a printed
manual line when sudo/admin is missing. The process `chdir`s into the
checkout so no `-dir` value reaches a file operation or an argv. Three
verbs (`up`, `down`, `status`), `make dist` cross-compiles the six
targets (CGO_ENABLED=0; modernc sqlite is pure Go). Found on its first
live runs, TWO ways `make up` at HEAD had been red with nothing green
noticing: `web/src/test/fixtures.ts` lacked A14's two `limits` fields
(the local generated client was stale in the opposite direction, so
`make ui-test` passed — `make ui-gen` first is what would have caught
it), and the Dockerfile's webbuilder stage never copied
`docs/USER-GUIDE.md`, which `GuidePage.tsx` imports `?raw` since A7 and
which `.dockerignore`'s `*.md` kept out of the context (one named `COPY`
and one `!` exception now; `make smoke` would have said so the same
evening). No route, no tool, no variable, no migration; nolint 29 → 32.

**`A17` (2026-09-03) is publication: the repository public on GitHub, the
bars on GitHub Actions, the plugin on npm — no product change at all.**
Four things about it cannot be guessed from the tree. **The history is one
commit and the origin is GitHub** (`git@github.com:yashok111/mocker.git`,
the private Gitea forge deleted the same day; "Hard rules" below): the
144-commit pre-publication history was written against a private forge
and a private document, and lives on as a bundle on the owner's machine
only. It was squashed and force-pushed several times during the day — and
**from the first release tag on it is never rewritten again**: a tag
points at a commit, and a squash after it leaves the tag on an orphan.
**The acceptance corpus is embedded** (`internal/testspec/testdata/
acceptance.json`, "Hard rules") because the alternative — a gitignored
private document every acceptance test skipped without and the golden
guard refused to run without — made `make test` red on every fresh clone,
which is the one thing a public repository cannot have. **CI is
`.github/workflows/ci.yml`: the make bars as five jobs** (go: lint + the
race suite; web; the plugin against the real binary; `smoke`;
`smoke-tls`), every action pinned by commit SHA, Go from `go.mod`, Node 24
with yarn 4 through corepack and a lockfile-keyed cache, a hard
`timeout-minutes` on each, `concurrency` cancelling a superseded run. The
non-docker jobs run every heavy command through `scripts/ci-cap.sh` — a
transient SYSTEM scope (`sudo -E systemd-run --scope`, `MemoryMax`,
`MemorySwapMax=0` so an overrun is a kill and not thrashing into the
timeout, `CPUQuota=300%` of the runner's four cores) with the Makefile's
own user-scope `CAP` passed empty so the two never nest, `GOMAXPROCS`,
`GOMEMLIMIT` and `NODE_OPTIONS` set to match, and a "prove the cap" step
that reads `memory.max`/`cpu.max` from INSIDE the scope and asserts
`GITHUB_ACTIONS` is visible there, failing the job otherwise — three
runs paid for that step: the first left `HOME=/root` through the sudo
chain (go wrote its cache under `/root`), the second proved the cap but
the OUTER sudo lacked `-E`, so every soft limit and npm's OIDC pair
silently never arrived while `memory.max` read exactly right. The docker
jobs are not capped from the client (their memory is dockerd's, as the
Makefile's `CAP` comment says) and get a timeout and an unconditional
teardown. CI also found one real smoke defect: `scripts/wsclient.py`
sent its whole 6 MB burst, THEN slept, THEN read, so on a slow runner the
server's echo write sat blocked past `MOCKER_STREAM_FRAME_TIMEOUT` (4 s
in the smoke) and P6d A3(f) saw a 1001 with zero echoes; the burst now
runs on a thread with the reader starting at `--read-after-ms` while it
is still going, every socket write through one locked `send()`, and
`--idle-ms` ending the read once the burst is out. **The plugin is
`@yashok111/mocker-test` on npm** (the `@mocker` scope was not ours;
`@yashok111` is an npm ORG the owner created for it — the account itself
is `yashok1111`, renamed to free the name — and the org owns the scope):
the first version went up by hand (`npm login` and `npm publish --access
public --otp=…` — 2FA is mandatory on publish now, and npm answers a WRONG
otp with a 404, not a 401), every later one goes up by a tag —
`.github/workflows/release-plugin.yml` on `plugin-v<version>` refuses a
tag that does not equal `package.json`'s version, runs `make plugin-test`
under the cap, and publishes with `--provenance` through npm Trusted
Publishing (`id-token: write`; the package's "Trusted publisher" on
npmjs.com names this repository and this workflow file, with "Allow npm
publish" ticked so the release goes live without a manual stage step);
there is no npm token anywhere in the repository, and the publish step
runs OUTSIDE the cap because the OIDC exchange is one more thing a
wrapper can get in the way of. The registry's read side lags a publish by
several minutes (404 on `npm view` twice today, 7 minutes each) — wait,
do not republish. The release recipe is in `packages/mocker-test/README.md`
("Installing"): bump `version`, `corepack yarn install` (the workspace's
own name and version live in `yarn.lock`, `--immutable` refuses the
mismatch), commit, tag, push both. On the dev Mac every npm command needs
`env -u HTTPS_PROXY`: the session's egress proxy breaks TLS to the
registry's login and publish endpoints.

**The linter is golangci-lint v2**, `.golangci.yml` carried over from another backend of the owner's,
so that the set is one across two repositories. Keep it at zero. Exceptions are only
pinpoint `//nolint:<linter> // reason` at the site of the trigger, not widening the
config. There are **39** of them now (`rg -c '//nolint' cmd internal`, summed), and each
carries the reason right in the line. **The breakdown below is a list of REASONS,
not a census: the one command that counts them accurately is
`grep -rho '//nolint:[a-z,]*' cmd internal | sed 's|//nolint:||' | tr ',' '\n' | sort | uniq -c`,
because a tag may name two linters after one colon and a per-linter grep
undercounts exactly those.** Measured 2026-09-05: 15 `gosec`, 15 `gocyclo`,
3 `nilerr`, 2 `nilnil`, 2 `errcheck`, 1 `errorlint`, 1 `contextcheck`.
`gocyclo` sits on functions where the branching is the
specification (a branch per schema keyword, per recipe kind, per refusal
reason, plus P3d's own `rollbackTx` — a branch per D7 refusal, and A18's
`overrides.ValidateVariant` once `function` joined the exclusivity set);
`gosec` — cookie `Secure`
being a parameter for the sake of `MOCKER_DEV`, serving
a body by the mock plane (its media types are already filtered out on write), reading a fixture in
a test helper, an intentionally oversized test write, P3c's `ref` recipe's modulo-by-length
entity pick, P3f's `dumpTableRows` test helper interpolating a table name that comes from
`sqlite_master` itself and never from a request, and A6's asset route writing an operator's
own upload (its type refused at upload and again at serve), and A16's three in
`cmd/mocker/setup.go` — two G204 on the wizard's docker/sudo exec (running
those IS the job; every variable argument is validated first) and one G703
on writing `.env` (the path is a constant, the taint is the content), and
A18's on the function branch's own body write (the media types that would
make it an XSS vector are refused three statements above, in
`functionHeaders`, which taint analysis cannot see from there); one `errorlint` — `recover()` gives `any`, not an error chain; one `contextcheck` — P6d's `wsLoop.run` takes `conn.Context()`, Registry.Open's child of the request context, because the registry's cancel is what must end a WebSocket loop and the linter cannot see the derivation (P6d's two `gocyclo` are `wsLoop.run` and `wsLoop.loop`, one select case per producer);
two `errcheck`, one of which (the oversized test write above) shares its line with a `gosec` tag
rather than adding a nineteenth line, the other `defer tx.Rollback()`; two `nilnil` — P7a's `resolverForSpec` ("no spec is bound" IS the answer, and every caller reads a nil resolver as exactly that) and `operationFromJSON` (a snapshot that declares no operation). Two relaxations in the config, both along the
way: `gocyclo` does not count `_test.go` (table-driven acceptance reaches 74 and stays
readable) and G115 does not count `internal/{gen,recipes,auth}` — there a uint64 truncation
of a deterministic PRNG is the algorithm, and the 419-body golden guards it.

**goleak is in every package with tests** (36 of them since `A18` added `internal/luafn`, after `P7a` added `internal/design`, after `A8` added `internal/yamlx`, after `A7` added `internal/guide`, after `A6` added `internal/assets`, after `A5` gave `cmd/mocker` its first test file beside `P6d`'s `internal/wsmock`, `internal/*/main_test.go` and `cmd/mocker/main_test.go`
— three lines per package, the whole ignore list with reasons once in
`internal/testleak`; `internal/store` — `store.go` since P0, but without a single test
until A3, whose `AllocateEditVersion` gave it both its first file and its first harness
at once). A goroutine that outlives a package's tests fails it. Do not extend the
ignore list: it holds only what the runtime parks for the whole process
(`database/sql` opener/resetter, sqlite VFS). Everything else is a finding, and
usually it is a `Run`/`Close` protocol that returns earlier than its own
goroutines; exactly that already lost the last traffic records at shutdown.

