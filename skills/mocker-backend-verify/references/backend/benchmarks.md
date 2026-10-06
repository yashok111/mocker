# Reproducible Workbench benchmark procedure

Use an explicitly isolated server/database and preserve its source/build/config
manifest. Never point the runner at the user's running Mocker. Tools do not
start/stop Docker, upgrade stores or execute imported applications.

`python3 scripts/backend-workbench-benchmark.py --output /tmp/b62-plan`
creates a NOT RUN ledger. `--run --source-manifest BUILD_MANIFEST.json` opts into 3 warmups and at least30 measured
repetitions with raw logs, samples, nearest-rank p95 and allocation counts.
Go cases cover500-record import/query,1000-execution evaluation, raw quantiles,
and source diff10000nodes/30000edges. These are warm-process local measurements,
not cold-start, public impact, browser responsiveness or4CPU/8GiB acceptance.
Supply RAM/local-SSD/CPU quota/OS/browser/build metadata in the workload manifest.

Public requests: pass `--workload FILE --isolated-base-url http://127.0.0.1:PORT --admin-host admin.ISOLATED_DOMAIN`;
the MCP key comes only from MOCKER_BENCHMARK_MCP_KEY and is never written to logs.
The JSON file has `cases:[{name,metadata,requests:[{name,arguments},...]}]`, exactly
33requests per case at default repetitions. Keep all source/diagram/view/set/
correlation/result pins explicit. Correlation mutations require distinct keys and
correct CAS versions for each repeat. Persist raw responses for audit. Admission
time is not job completion time: collect terminal timestamps separately. Declare
set sizes, retained versions, truncation/results limits and cold/warm grouping.

Cover page/search (target p95≤500ms), first subgraph≤1s, impact≤10s, observation
correlation/query and diagram projection/member paging on10000/30000 data. Capture
first canvas after data load≤2s and200/600 layout≤2s using browser Performance
marks and the browser harness; retain raw samples/long tasks. No>100ms input stall
is a separate browser gate. No browser sample means NOT RUN, never PASS.

Orders: build with `go run ./cmd/orders-reference/build fixed OUTPUT` to retain
actual source/build/binary hashes. Use private temporary ORDERS_REFERENCE_DB and
explicit isolated configuration from internal/ordersreference/README.md. Set exact
ORDERS_MEASUREMENT_SERVICE_ID and ORDERS_MEASUREMENT_REPOSITORY_ID, then invoke
`orders-reference-fixed --measure-read=n_plus_one` or `--measure-read=batched`.
This executes the trusted fixture locally without binding an HTTP port. It seeds
50items outside the measured scope and outputs real timings/records, exact build
identity, root IDs and import-ready batches across two linked traces. Actual SQL
counts are51and2with identical result data; recompute from client SQL spans.
Notification send/receive is an executed in-memory channel, not a real broker.
Response bytes measure JSON encoding, not network transfer. Do not claim payment
traffic: payment is not invoked by read-order; legacy replay's payment substitute
is explicitly mocked. Authenticated GET /__mocker_test/runs/{run}/observations
exports legacy business instrumentation; it is process-lifetime data and SQL
coverage is partial, excluding control/journal/savepoint operations. Preserve it
before restart. Never substitute journal/model counters for executed timings.

Only compare main/baseline on the same declared hardware/build/config/input
profile. Keep local results separate from the target4CPU/8GiB/local-SSD gate.
Do not optimize from invented results or silently increase queue/cap limits.

In an isolated Vite development session import
`/src/components/backend-workbench/backendMeasurementBenchmark.ts` and call
`benchmarkWorkbenchUI(name, action, {nodes:200,edges:600,buildHash,cache:"warm",browser})`.
`action` must perform the real pinned navigation/layout and resolve after its
visible result is ready. Repeat with10000/30000and separately defined cold runs;
do not label warmed repeated navigation cold. Save the returned JSON unchanged.
The helper waits two animation frames, retains3warmups/30samples and long tasks,
and explicitly marks unsupported long-task APIs NOT RUN. Input latency still
requires its own browser trace. Do not call it on the user's live workspace.
