# Diagnostics policy v1

The immutable report binds exact graph/artifact inputs, rule version and optional diagram scope. Fingerprints identify stable rule subjects; basisHash identifies evidence. Labels do not identify objects.

| Rule | Required evidence and counterexample |
|---|---|
| relational_drift | Known differing SQL/ORM/migration facets; unknown values are gaps. No deployment truth is selected. |
| unused_table | Complete current supported access inventory including column accesses. Incomplete SQL or dispatch cannot prove unused. |
| transaction_expectation | Explicit typed transaction criterion; a legitimate standalone write without one creates no finding. |
| possible_n_plus_one | Current local loop control cycle through query/call; a query after a loop is not a loop-body witness. Batching remains unknown. |
| possible_emit_before_commit | Current local transaction and emit→commit source-control witness; stale witness is insufficient. No delivery/atomicity claim. |
| unhandled_outcome | Supported raise/terminal and complete exit/dispatch inventory; a declared exit is not missing. |
| ambiguous_dispatch | Explicit unresolved call/consumer/job remainder; no name matching. |
| diagram_broken_reference | Server-retained missing implementation ref and original diagram pin; partial source absence is unknown. |
| lifecycle_forbidden_transition | Positive current source assertion and exact from/to/trigger mapping against authored forbidden intent. Opaque guard means possible, ambiguous/historical mapping means unknown. |

Read-only inspection creates neither a diagnostic job nor a review. Diagnostic job success means the bounded computation terminated; inspect complete, gaps and truncation. Review history is mutable metadata returned separately; saved reports never change. Identical receipts precede CAS. The next wave B5.2 is not part of this workflow.
