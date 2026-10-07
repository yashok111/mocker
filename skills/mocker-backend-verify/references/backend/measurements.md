# Exact scenario measurements and observed sequences

Negotiate verify4 and capabilities backend-scenario-measurements,
backend-observed-impact, backend-observed-sequences. All operations use saved
versions. No measurement job starts imported code, SQL, jobs or a broker.

1. Import externally collected trusted observations; retain each returned
   setId/version/contentHash/recordCount. Correlate each exact set against the
   actual SOURCE revision/hash/targetGraphHash, retain correlation version/hash.
   Unknown builds can be measured but never confirm source impact.
2. Save this complete request before `start_backend_analysis` (replace every
   symbolic pin with a value from exact reads):

```json
{
  "projectId": "PROJECT_UUID",
  "kind": "scenario_measurement",
  "beforeRevisionId": "SOURCE_REVISION_UUID",
  "observationPins": [{
    "observationSetId": "SET_UUID", "version": 1, "contentHash": "SET_SHA256",
    "correlationVersion": 1, "correlationHash": "CORRELATION_SHA256", "side": "before"
  }],
  "measurements": [{
    "side": "before", "executionIds": ["EXECUTION_ID"],
    "rootSpanIds": ["EXACT_ROOT_SPAN_ID"],
    "basis": {"kind": "spans"}, "policy": "backend-scenario-measures-v1"
  }],
  "limits": {}, "idempotencyKey": "SAVED_UNIQUE_KEY"
}
```

`scenario_comparison` additionally requires afterRevisionId, after-side pins and
a second measurements element with side=after. Each side retains its complete
source/build, environment, configuration, input, window, sampling, instrumentation,
producer/scenario and mocked-dependency vector. A difference is a confounder,
not an attribution to the code change. An execution selection is mandatory,
max1000 executions; max20 exact pins, existing job input/result limits apply.
For provider counts explicitly choose
`basis:{kind:"measurements",basis:"PRODUCER_BASIS",scope:"EXACT_SCOPE"}`.
It substitutes span counts; never add both. Missing measurements are null with
reasons. A partial count is an observed lower bound. SQL counts designated client
operations; client/server duplicates and overlapping exports are deduplicated.
Retry requires explicit instrumentation; names alone prove nothing.

Latency uses the selected complete root sample. With no selected roots, a single
terminal passed/failed test duration may supply a sample; skipped/unknown tests
are not successes. Percentiles use nearest rank on original duration samples,
never averages of p95s. Samples remain in the result for independent recomputation;
samples[].executionId is the selected execution ID. Value is the total over the
sampled executions, so a comparison delta is null when before and after differ in
sampleCount or missingSamples, and a limitation names the metric.
Sampled failures, particularly tail/unknown sampling, are not population rates.

3. Poll `get_backend_analysis` at the recommended interval; retain jobId,
analysisInputHash and a terminal resultVersion. Read exact result pages using
`get_backend_analysis_results {projectId,analysisId,resultVersion,section}`:
checks with kind=scenario_measurement or scenario_comparison; witnesses with
kind=observed_sequence. Existing Store26 section names are preserved. Retain returned cursors within
that resultVersion. Later imports, correlation edits and diagram saves do not
rewrite the saved input/report. Retry a lost start reply with identical key/body.

4. For one diagram overlay, add exact `diagramScope:{pin,selectors}` to
scenario_measurement; all correlations must pin that same resolved scope.
C4 projected relations include policy/level/root. Observed sequences apply to
architecture, interactions, lifecycle and business maps without changing their
source/intent alternatives. Parent links mean containment, generic links mean
association. Only matching orders-message-causal-v1 send/receive witnesses give
event precedence. Timestamp order across services proves nothing. Missing peers,
ambiguous mappings/build mismatches and cycles are gaps. One run cannot establish
all branches, unreachable transitions or whole lifecycle coverage. Opening another
diagram must not display a previous diagram's overlay.

5. Impact keeps observationMode=none by default. Explicit pinned mode requires
nonempty before/after pins, exact source-compatible correlation and matching
side target. After-side desired proposals are unsupported; before-side source
observations may coexist with desired after intent. Read witnesses with kind=observed_evidence separately
from structural witnesses: an observed object does not promote inferred paths.
scope.kind and scope.service (id or name) filter observed evidence too; a
filtered row leaves gap scope_omitted_observation. An artifact ref is addressed
as recordType=artifact_object with the digest of the exact ref; read its ref.

Benchmark tools: `scripts/backend-workbench-benchmark.py` defaults to NOT RUN;
only `--run` performs long measurements. See the product benchmark guide. Local
unit tests, counts or one run are not runtime/performance/program acceptance.
B6.3 immutable storage rebuild and ordinary/live-agent acceptance remain separate.
