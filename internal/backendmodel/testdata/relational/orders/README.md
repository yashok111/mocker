# Orders relational source fixtures

These synthetic sources describe `users`, `orders`, `order_items` and `payments`
in PostgreSQL and SQLite. They are input data for B1.1 imports and inspection.
Never run the SQL, migrations, stored bodies, Go declarations, ORM migrations or
application callbacks. No database service or built-in source parser is needed.

Each dialect has `v1/` and `v2/` containing `schema.sql`, static `models.go`,
ordered `migrations/` and an independent `expected.json`. The SQL file declares
the desired source facet independently of migration-derived state. Migration
002 is deliberately unsupported; its dependent orders schema stays incomplete
even after the handled v2 delta in 003. The SQLite FK action change uses a raw
replacement-table migration; these files contain no claim that it ran.

Expected manifests contain hand-authored facts, never importer/projector output.
They are assertion data, not import-command JSON. `nodes` and `edges` are keyed
by provider external key; `facets` use the fixed keys `sql`, `orm`, `migration`.
Only scalar properties use the documented known/unknown wrappers. Missing
expected fields mean no assertion, not null or absent source data. Native
definitions come from the referenced original UTF-8 bytes; quoted expressions
and native types are compared verbatim. The source manifest lists SHA-256,
byte lengths and line counts of source files only. Source locations are 1-based
inclusive literal line anchors. Relative source paths start at this directory.

All fixture runs use logical repository `orders-fixture` and provider namespace
`orders-fixtures`. Import one dialect per repository. External keys follow
`database:orders`, `schema:public|main`, `table:<name>`,
`column:<table>:<name>`, `constraint:<table>:<role>`,
`index:<table>:<role>`, `view:<name>`, `symbol:<role>`,
`migration:<ordered-name>` and `references:<table>:<fk-role>`.
Keys are assertions of collector identity; names never merge graph objects.
Constraint/FK column arrays, pairs, index terms and migration parents retain
their authored order.

V2 requires explicit identity mapping from `column:orders:status` to
`column:orders:state` before target upsert. `sameAs` identifies the v1 subject
symbolically; tests substitute actual returned UUIDs. Other surviving subject
keys retain their UUIDs. Proved removal of `legacy_note` and its index repairs
the view and retained migration CREATE targets. `revisionPin: "v1"` inside
expected history means the actual immutable v1 revision/object IDs, not literal
wire values. A CREATE can keep a historical target after deletion. The transient
`scratch_status` CREATE→DROP chain uses `source_only`, allocates no graph UUID or
identity reservation, and never appears as an active ER column.

The ORM facet intentionally differs on `orders.user_id` nullability, status/state
default, amount native precision/type and FK DELETE actions. Timestamp callback
tags leave SQL DEFAULT unknown. ORM constraints and deferrability stay incomplete
where declarations omit physical details. Explicit complete SQL facets establish
the independent expected ER bounds; these describe declared source semantics,
not row counts or observed runtime enforcement. The partial expression index
does not supply an unqualified column-key uniqueness basis.

`scenarios.json` adds symbolic reconciliation expectations for source-only and
historical history, proof reuse conflict/repair, shared FK endpoint conflict,
partial omission, conservative cardinality and untrusted source comments. The
two small `scenarios/*_fk_target_change.sql` files provide explicit source proof
for the endpoint change; command construction, real pins, receipts and behavior
tests belong to the domain/acceptance callers.
