# Trusted Orders reference service

This standalone test-only service implements the frozen `orders-replay-v1`
protocol. Payment is a SQLite-backed mock; order insertion is real fixture SQL.
It never imports Mocker store/replay code or executes imported application code.

Build from the repository root with the repository's Go toolchain:

```sh
GOCACHE=/tmp/b53-b-go-cache go run ./cmd/orders-reference/build buggy /tmp/b53-b-build
GOCACHE=/tmp/b53-b-go-cache go run ./cmd/orders-reference/build fixed /tmp/b53-b-build
```

Each output has a `.manifest.json` containing sorted source-file hashes, the
shared source-tree hash policy, build descriptor/build hash and separate binary
SHA256. The selected variant source file is included. Tests, oracles, build-tool
source and output are not compiled into the service and are excluded. Module
versions/checksums are pinned by go.mod/go.sum. The build tool uses `go list
-deps` to include all local compiled production inputs and embedded schema.
Build identity substitutions use explicit placeholders to avoid circular hashes.
Default `go build` binaries lack embedded hashes and refuse startup.

Use a dedicated private directory; the database must be an absolute path, with
no other application data. The service rejects an existing foreign DB, wrong
isolation, non-private file or symlink. A retained `.lock` file prevents a second
process from opening the same DB. Do not remove the lock file while running.
Unix/macOS are supported; Windows is not supported by the file-lock implementation.

```sh
# Obtain a >=32-byte secret in ORDERS_REFERENCE_TOKEN through your secret manager.
# No secret values belong in manifests, commands saved to docs, or logs.
mkdir -m 700 /tmp/orders-reference-buggy-private
export ORDERS_REFERENCE_ADDR=127.0.0.1:18091
export ORDERS_REFERENCE_DB=/tmp/orders-reference-buggy-private/orders.db
export ORDERS_REFERENCE_ISOLATION_ID=81c9935e-03bb-45fa-89c9-f16299ed4d39
export ORDERS_REFERENCE_TARGET_ID=orders-buggy
export ORDERS_REFERENCE_CONFIG_VERSION=1
/tmp/b53-b-build/orders-reference-buggy
```

Fixed: use `/tmp/b53-b-build/orders-reference-fixed`, port `18092`, a separate
private directory/DB, target `orders-fixed`, and isolation UUID
`4f074ee0-5e95-49a0-85a0-e17b8c724d04`. Configure the matching operator target in
Mocker through lane A's registry. There is no runtime variant selector.

Only the five frozen endpoints are served, all authenticated. Reset requires
matching identity/target/config/isolation and explicit versioned consent. It
advances global epoch by CAS and preserves old runs. An instance UUID changes
on each Open/start; historical receipts remain immutable and readable, but
new operations cannot attach to runs from the previous instance.

A mutex and a private SQLite transaction serialize mutations. Delivery lookup
precedes identity/epoch validation, returning the original bytes/status without
new events. Charge, trigger consumption, events and receipt commit together.
The actual order INSERT uses a SAVEPOINT; the first failure rolls it back while
retaining the mocked charge and 503 receipt. A second business attempt inserts
one order. Fixed payment uses the business key within the run's unique epoch;
buggy uses the delivery key and charges twice. Each run accepts only the closed
two-attempt fixture and one failure arm.

Short verification:

```sh
GOCACHE=/tmp/b53-b-go-cache go test -count=1 ./internal/ordersreference ./internal/ordersprotocol ./cmd/orders-reference/...
```

Tests read the frozen hand-authored buggy/fixed JSON oracles directly, exercise
HTTP with httptest and real temporary SQLite files, and inspect actual rows.
They cover duplicate/concurrent delivery, reset CAS, stale epochs, retained
consent, reopen recovery, and a final receipt-write failure rolling back the
outer transaction. This is not a process-kill/power-loss test or an engine to
service acceptance run. No browser/live Mocker/full-suite QA is required here.
