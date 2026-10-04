# Actual prior-binary B4.1 proof oracle

`prior-store19.db.gz` is a lossless gzip copy of the stopped Store19 database produced by the actual prior `mocker-b32-final` binary at commit `c41cd099eaad84458fb167643575dedf885c642a`. It was not seeded or exported again by the current implementation.

- Original DB SHA-256: `3b8ff09b51a1deccf64fd0dd67c89f9736c0d715e864e0b400e5e5b258c0f3ee`.
- Prior binary SHA-256: `a5947af3e0104bd43339028022012896390b2a9e79c8c903c9e99efd1b59b558`.
- Original frozen path: `/private/tmp/mocker-b41-baseline-J0G0y1/mocker.db`.
- Provenance report and successful old→old oracle: `docs/backend-workbench-b41-baseline-report.md` and `docs/backend-workbench-b41-baseline-selfcheck-v2-result.json`.

The complete DB preserves history, sessions and receipt bytes. Tests only decompress it to their own temporary directory, verify the pinned digest, then open/migrate that copy. The compressed fixture and original oracle are never opened for database writes.

`prior-store19.json` contains the seven original proof pointers and raw-document hashes from the prior seed, plus the one pre-existing broad record proof already attached to that service. The seven designated cases remain a separate list. Its additional bootstrap-base hashes were read from the same immutable old DB before migration: public Begin uses that project's actual later pin-only source5 head, rather than rewriting its head to an earlier revision. Both the original seed revision and actual bootstrap base remain independently pinned. No current education-platform data or current-generated replacement proof was added.
