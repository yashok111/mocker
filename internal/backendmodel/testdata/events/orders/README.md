# Orders event source fixture

These five `*.ts.txt` files are inert source evidence. Read them as text. They use
a small illustrative TypeScript DSL and have no runtime or dependencies.

`expected.json` is an independent declarative oracle, not an import request. Its
keys are stable provider external keys; UUIDs are supplied by the eventual import.
`witnesses` contain exact physical line ranges and text. `sourceManifest` contains
the SHA-256 of every source member. `parentKey` states expected containment;
`values` describe ports by local semantic keys without choosing a wire DTO.

The baseline scope contains `orders.ts.txt`, `wiring.ts.txt`, `billing.ts.txt` and
`jobs.ts.txt`. The resolved scope adds `fraud-plugin.ts.txt`. Apply `resolvedDelta`
to the baseline expectations, while preserving all unrelated keys and evidence.
The plugin's explicit registration supplies the fraud handler. The audit dynamic
dispatch remainder and missing private-ledger module remain unresolved.

The oracle expects six declared producer/subscription tuples, one orphan
subscription, two jobs and one explicit service call. The primary and secondary
routes use one message and one consumer; their `event_field` addresses differ by
endpoint and route. The returns route repeats display labels with distinct keys.
The status path is database column → query result → serialization → explicit
transport → deserialization. The reason transform has two inputs. Publications
precede local commit, and configured retry/DLQ links carry no delivery claim.

`semantics`, `phase`, expected view keys and assertions are oracle vocabulary.
They must be translated into reviewed source5 DTOs by the domain/public fixture
implementer. Manifest/analyzed-member declarations, contains/control graph
records and actual import commands are built there after DTOs are frozen.
Neither the expectation JSON nor the source files are implementation output.

Limitations and both gap outcomes are documented in
`docs/backend-workbench-b32-fixture-oracle.md`. No source file, broker, job or SQL
is executed during fixture preparation or verification.

Task 2 appended an explicit PostgreSQL database configuration and bindings to
`orders.ts.txt` (lines29–31), after root ruled that the strict relational model
requires a concrete dialect. Its hash and the oracle's `database.config` witness
were updated. Earlier witnesses and all event expectations remain unchanged.
SQL schema and column definitions remain unknown.
