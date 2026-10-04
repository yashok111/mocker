# Import and pinned database examples

This topic is import7-owned. Verify its actual import workflow identity/contentHash
in the selected global guide set. Database7 inspection may load it through that
owner without starting import writes. The foundation procedure below remains
schema1; the relational captures afterward use schema2. SQL/ORM/migration input
is source data and is never executed.

These excerpts use the same two-version Go foundation fixture as the B0.3
protocol checks. They run through the unchanged MCP contract. `tool(name,input)`
means a call over your configured connection, after compatible guide selection
and reading backend-model/import-protocol/recovery from that selected set.
Persist each named input before sending; variables below show what to retain.
Actual UUIDs, versions and hashes always come from responses, never these names.
Fixture strings are source data; no source application, SQL or package script runs.

## B4.2 executable public workflow

`internal/mcp/tools_backend_b42_examples_test.go` runs `TestBackendB42SDKPublicWorkflowExample` through the actual MCP SDK, real authenticated REST/session/CSRF handlers, temporary SQLite and the real static worker. Source is inert Go declaration text. The example negotiates current sync2/change2 alongside the retained owners and verifies each fetched topic hash. It preserves legacy artifact-context wire bytes instead of decoding them as a new writable context.

Run from the repository root, retaining artifacts:

```sh
mkdir -p /private/tmp/mocker-b42-example-artifacts
GOCACHE=/private/tmp/mocker-b42-analysis-cache go test ./internal/mcp \
  -run '^TestBackendB42SDKPublicWorkflowExample$' -count=1 -artifacts \
  -outputdir=/private/tmp/mocker-b42-example-artifacts -v
```

The retained `sdk-protocol.json` includes actual requests, returned IDs/hashes and expected refusals; `REST POST ...` entries show the session-authenticated route calls. Fresh executions obtain fresh IDs. Do not substitute IDs from an older transcript into a different project.

The executable sequence demonstrates:

1. Import B named Before; save O named Desired; reconcile the same partition into N named NewSource. Preview returns all three presence-aware values and no candidateHash while unresolved. Keep O with an explicit reason, preview again and apply exactly that candidate. Old source and old desired reads stay byte-identical.
2. Start source-to-source diff over REST, cancel through MCP, and replay REST Start with the original key/body: the queued start receipt stays exact despite later cancellation. A new-key terminal cancellation is refused.
3. Admit a second queued job, close/reopen the actual store and run startup recovery. Poll returns interrupted with an explicit frozen resultVersion. Old-key Start replays the old receipt; deliberate new-key Retry creates a distinct queued job with the original analysisInputHash. The original interrupted manifest remains readable after the retried job completes.
4. Run the real worker, poll through `get_backend_analysis`, and page `changes` at one chosen resultVersion with limit1 and all returned cursors. Re-read each frozen page byte-for-byte and reconcile the complete count against its manifest. No page silently follows newer work.
5. Start impact from the rebased saved draft's exact base to its exact full proposal revision. Require completed/complete, all changed addresses covered and runtimeVerified false. A wrong resultHash is refused. Acknowledge the exact gap set and mark ready with the returned job/resultVersion/inputHash/resultHash. Reopen the store again and replay rebase/ready/start/retry/cancel receipts and saved source/draft/report/context reads unchanged.

The ready request is constructed from returned values, not client assertions:

```text
report = {jobId:job.id, resultVersion:job.resultVersion,
          inputHash:job.analysisInputHash, resultHash:page.manifest.semanticResultHash}
acknowledgedGapIds = sorted(unique(page.manifest.gaps.map(gap => gap.id)))
apply_backend_change_proposal_lifecycle({projectId,proposalId,
  expectedVersion:savedProposal.version,proposalRevisionId:savedDraft.id,
  action:"ready",report,acknowledgedGapIds,idempotencyKey:savedReadyKey})
```

Persist that complete request before transport. The example's accelerated poll timing is for tests; production clients honor recommendedPollIntervalMs. These examples validate developer protocol usage. Ordinary/live-agent acceptance remains deferred, and static ready never means runtime tested or deployed. For complete request/limit/recovery contracts read change2's `backend-analysis-jobs` and `backend-change-rebase` in this set.

## B4.1 executable SDK captures

`internal/mcp/tools_backend_b41_examples_test.go` executes these five episodes through the real SDK, admin handlers and temporary SQLite. The capture contains 195 calls, including 23 expected refusals. Source strings and the prior Store19 database are inert fixtures; imported code and SQL never execute. The complete request/response witness is emitted as `sdk-protocol.json` for each test. To preserve artifacts, create an output directory, then run `go test ./internal/mcp -run '^TestBackendB41SDK' -count=1 -artifacts -outputdir=/absolute/output/directory -v`.

The excerpts below contain actual captured request IDs, hashes and returned pins. Do not replay those historical UUIDs into a different project. The executable examples use each response to construct the next request, select import7/sync2/change2/project2/inspect7/database7 from current capabilities in one exact guide set, verify topic hashes, and refuse unavailable old guide sets. These protocol captures preceded guide registration, so they contain no invented future guide identity. Responses below project relevant fields and show at most two array entries; omitted fields and entries remain in the full witness.

The tests additionally verify source5/full5 assertion refusal, a foreign project's exact UUID, candidate invalidation after batch/commit, candidate specialized-query and SavedView rejection, read purity across all backend tables, unchanged source proof, and full command reuse after restore/server restart. Original request strings and original receipts are compared byte-for-byte. No-op and overwritten commands remain reserved even when their semantic effect disappears.

### Source5 extension, annotations and exact CAS

Executable episode: `TestBackendB41SDKRetainedPartitionExamples`.

```json
{
  "tool": "begin_backend_import",
  "request": {
    "profile": "composed-source-v1",
    "mode": "composed",
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968",
    "sourceScope": {
      "kind": "add_repository"
    },
    "syncPolicy": "whole-source-v1",
    "profileExtension": {
      "fromProfile": "events-service-v1",
      "toProfile": "composed-source-v1"
    },
    "expectedVersion": 2,
    "baseRevisionId": "01a103dc-12c7-79ca-9a04-ca983ebdf45d",
    "idempotencyKey": "begin-4",
    "manifest": {
      "repositoryName": "remote",
      "provider": {
        "name": "sdk-example-collector",
        "version": "1",
        "namespace": "provider-remote",
        "method": "ast",
        "profiles": [
          "foundation-graph-v1",
          "relational-graph-v1",
          "runtime-flow-v1",
          "field-lineage-v1",
          "events-service-v1",
          "composed-source-v1"
        ],
        "limitations": []
      },
      "snapshot": {
        "dirty": false,
        "consistency": "verified",
        "capturedAt": "2026-10-03T10:00:00Z",
        "files": [
          {
            "path": "source.go",
            "contentHash": "6adca43308968e94257e950af2f383219c4e9425eb1e4a67b4f81bf82e4594ff",
            "fileType": "go",
            "analysisStatus": "analyzed"
          }
        ]
      }
    },
    "inventory": [
      {
        "category": "files",
        "status": "complete",
        "knownCount": 1,
        "denominator": 1,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "endpoints",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "datastores",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "migrations",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "producers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "consumers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "jobs",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "contracts",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "tests",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      }
    ],
    "scopeStatus": {
      "status": "complete",
      "gaps": []
    }
  },
  "responseExcerpt": {
    "syncPolicy": "whole-source-v1",
    "id": "01a103dc-12ca-7683-8ff5-dbefda7cb986",
    "baseRevisionId": "01a103dc-12c7-79ca-9a04-ca983ebdf45d",
    "repositoryId": "01a103dc-12ca-73fb-b063-5b2af8773dfa",
    "snapshotId": "01a103dc-12ca-7687-93ba-0d4b6559c9b5",
    "state": "collecting",
    "version": 1,
    "candidateHash": null
  }
}
```

```json
{
  "tool": "get_backend_coverage",
  "request": {
    "revisionId": "01a103dc-12d2-7153-9132-d09259b3f96c",
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968"
  },
  "responseExcerpt": {
    "target": {},
    "pins": {
      "viewSchemaVersion": "6",
      "baseRevisionId": "01a103dc-12d2-7153-9132-d09259b3f96c",
      "baseSemanticHash": "f61163715013f76e6c0dfd210b8a8bc3999cd92b9453bed317798605e4f5db24"
    },
    "source": {
      "sourceVector": {
        "documentVersion": "source-vector-v1",
        "partitions": [
          {
            "repositoryId": "01a103dc-12c0-7a83-a307-d789c732ae31",
            "snapshotId": "01a103dc-12c0-7b4f-b524-5e1d8c9bc51c",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "repositoryId": "01a103dc-12ca-73fb-b063-5b2af8773dfa",
            "snapshotId": "01a103dc-12ca-7687-93ba-0d4b6559c9b5",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-remote",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ],
        "snapshots": [
          {
            "id": "01a103dc-12c0-7b4f-b524-5e1d8c9bc51c",
            "repositoryId": "01a103dc-12c0-7a83-a307-d789c732ae31",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "id": "01a103dc-12ca-7687-93ba-0d4b6559c9b5",
            "repositoryId": "01a103dc-12ca-73fb-b063-5b2af8773dfa",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-remote",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ]
      },
      "identities": [],
      "currentness": [],
      "legacyProofBases": []
    },
    "viewSchemaVersion": "6",
    "snapshots": [
      {
        "id": "01a103dc-12c0-7b4f-b524-5e1d8c9bc51c",
        "repositoryId": "01a103dc-12c0-7a83-a307-d789c732ae31",
        "provider": {
          "name": "sdk-example-collector",
          "version": "1",
          "namespace": "provider-a",
          "profiles": [
            "foundation-graph-v1",
            "relational-graph-v1"
          ]
        }
      },
      {
        "id": "01a103dc-12ca-7687-93ba-0d4b6559c9b5",
        "repositoryId": "01a103dc-12ca-73fb-b063-5b2af8773dfa",
        "provider": {
          "name": "sdk-example-collector",
          "version": "1",
          "namespace": "provider-remote",
          "profiles": [
            "foundation-graph-v1",
            "relational-graph-v1"
          ]
        }
      }
    ]
  }
}
```

```json
{
  "tool": "begin_backend_import",
  "request": {
    "idempotencyKey": "begin-8",
    "manifest": {
      "repositoryName": "primary",
      "provider": {
        "name": "sdk-example-collector",
        "version": "1",
        "namespace": "provider-a",
        "method": "ast",
        "profiles": [
          "foundation-graph-v1",
          "relational-graph-v1",
          "runtime-flow-v1",
          "field-lineage-v1",
          "events-service-v1",
          "composed-source-v1"
        ],
        "limitations": []
      },
      "snapshot": {
        "dirty": false,
        "consistency": "verified",
        "capturedAt": "2026-10-03T10:00:00Z",
        "files": [
          {
            "path": "source.go",
            "contentHash": "ef83f7cf9ebbff9cad3a1d31f76b1a4c494e8dc6229af2a07a7964f0eb5ae43c",
            "fileType": "go",
            "analysisStatus": "analyzed"
          }
        ]
      }
    },
    "inventory": [
      {
        "category": "files",
        "status": "complete",
        "knownCount": 1,
        "denominator": 1,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "endpoints",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "datastores",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "migrations",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "producers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "consumers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "jobs",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "contracts",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "tests",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      }
    ],
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968",
    "sourceScope": {
      "kind": "reconcile",
      "repositoryId": "01a103dc-12c0-7a83-a307-d789c732ae31",
      "providerNamespace": "provider-a"
    },
    "scopeStatus": {
      "status": "complete",
      "gaps": []
    },
    "profileExtension": {
      "fromProfile": "events-service-v1",
      "toProfile": "composed-source-v1"
    },
    "expectedVersion": 3,
    "baseRevisionId": "01a103dc-12d2-7153-9132-d09259b3f96c",
    "syncPolicy": "whole-source-v1",
    "profile": "composed-source-v1",
    "mode": "composed"
  },
  "responseExcerpt": {
    "syncPolicy": "whole-source-v1",
    "id": "01a103dc-12d6-713f-b4f4-1dfab8f6938e",
    "baseRevisionId": "01a103dc-12d2-7153-9132-d09259b3f96c",
    "repositoryId": "01a103dc-12c0-7a83-a307-d789c732ae31",
    "snapshotId": "01a103dc-12d6-7140-b287-a86019f5d1da",
    "state": "collecting",
    "version": 1,
    "candidateHash": null
  }
}
```

```json
{
  "tool": "get_backend_coverage",
  "request": {
    "revisionId": "01a103dc-12dc-7e93-8273-5d677b81d10b",
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968"
  },
  "responseExcerpt": {
    "target": {},
    "pins": {
      "viewSchemaVersion": "6",
      "baseRevisionId": "01a103dc-12dc-7e93-8273-5d677b81d10b",
      "baseSemanticHash": "51e77f2e04bdbf2783339ac7c9989d86ce2842865dc9221fe3d6eafadb8bf936"
    },
    "source": {
      "sourceVector": {
        "documentVersion": "source-vector-v1",
        "partitions": [
          {
            "repositoryId": "01a103dc-12c0-7a83-a307-d789c732ae31",
            "snapshotId": "01a103dc-12d6-7140-b287-a86019f5d1da",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "repositoryId": "01a103dc-12ca-73fb-b063-5b2af8773dfa",
            "snapshotId": "01a103dc-12ca-7687-93ba-0d4b6559c9b5",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-remote",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ],
        "snapshots": [
          {
            "id": "01a103dc-12ca-7687-93ba-0d4b6559c9b5",
            "repositoryId": "01a103dc-12ca-73fb-b063-5b2af8773dfa",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-remote",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "id": "01a103dc-12d6-7140-b287-a86019f5d1da",
            "repositoryId": "01a103dc-12c0-7a83-a307-d789c732ae31",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ]
      },
      "identities": [],
      "currentness": [],
      "legacyProofBases": []
    },
    "viewSchemaVersion": "6",
    "snapshots": [
      {
        "id": "01a103dc-12ca-7687-93ba-0d4b6559c9b5",
        "repositoryId": "01a103dc-12ca-73fb-b063-5b2af8773dfa",
        "provider": {
          "name": "sdk-example-collector",
          "version": "1",
          "namespace": "provider-remote",
          "profiles": [
            "foundation-graph-v1",
            "relational-graph-v1"
          ]
        }
      },
      {
        "id": "01a103dc-12d6-7140-b287-a86019f5d1da",
        "repositoryId": "01a103dc-12c0-7a83-a307-d789c732ae31",
        "provider": {
          "name": "sdk-example-collector",
          "version": "1",
          "namespace": "provider-a",
          "profiles": [
            "foundation-graph-v1",
            "relational-graph-v1"
          ]
        }
      }
    ]
  }
}
```

```json
{
  "tool": "apply_backend_project_commands",
  "request": {
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968",
    "expectedVersion": 4,
    "idempotencyKey": "notes-12",
    "commands": [
      {
        "type": "create_annotation",
        "annotationId": "01a103dc-12ed-7018-8d47-cfea16dfca2d",
        "target": {
          "recordType": "node",
          "id": "01a103dc-12c1-7cd4-9f58-cdf7643aa212"
        },
        "body": "<em>Literal metadata</em>\nKeep exact whitespace.  "
      },
      {
        "type": "create_annotation",
        "annotationId": "01a103dc-12ed-701c-9066-88c52312dcd9",
        "target": {
          "recordType": "node",
          "id": "01a103dc-12c1-7e9b-a080-ba6f4383212c"
        },
        "body": "Retain after proved deletion"
      }
    ]
  },
  "responseExcerpt": {
    "id": "01a103dc-12bf-7e0c-a0d2-006b3d881968",
    "name": "B4.1 public examples",
    "version": 5,
    "currentRevisionId": "01a103dc-12dc-7e93-8273-5d677b81d10b"
  }
}
```

```json
{
  "tool": "apply_backend_project_commands",
  "request": {
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968",
    "expectedVersion": 5,
    "idempotencyKey": "update-note-13",
    "commands": [
      {
        "type": "update_annotation",
        "annotationId": "01a103dc-12ed-7018-8d47-cfea16dfca2d",
        "target": {
          "recordType": "node",
          "id": "01a103dc-12c1-7cd4-9f58-cdf7643aa212"
        },
        "body": "<em>Literal metadata</em>\nKeep exact whitespace.  \nReviewed"
      }
    ]
  },
  "responseExcerpt": {
    "id": "01a103dc-12bf-7e0c-a0d2-006b3d881968",
    "name": "B4.1 public examples",
    "version": 6,
    "currentRevisionId": "01a103dc-12dc-7e93-8273-5d677b81d10b"
  }
}
```

```json
{
  "tool": "list_backend_annotations",
  "request": {
    "limit": 1,
    "cursor": "eyJraW5kIjoiYW5ub3RhdGlvbnMiLCJwcm9qZWN0SWQiOiIwMWExMDNkYy0xMmJmLTdlMGMtYTBkMi0wMDZiM2Q4ODE5NjgiLCJwcm9qZWN0VmVyc2lvbiI6NSwicmV2aXNpb25JZCI6IjAxYTEwM2RjLTEyZGMtN2U5My04MjczLTVkNjc3YjgxZDEwYiIsImZpbHRlckhhc2giOiIxMjExNzJjYjE5OGEzNWRkZDg1NmFiMTVhN2VmMWE0OWRlNTIwNDI3MzZjMDNjOWQzNzVjYjQxMjA2MTRhYjJkIiwiYWZ0ZXIiOiIwMWExMDNkYy0xMmVkLTcwMTgtOGQ0Ny1jZmVhMTZkZmNhMmQifQ",
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968"
  },
  "error": "HTTP 409: {\"error\":{\"code\":\"backend_annotation_page_conflict\",\"message\":\"Project changed; restart annotation pagination\",\"retryable\":false,\"currentVersion\":6}}"
}
```

```json
{
  "tool": "commit_backend_import",
  "request": {
    "candidateHash": "b27dfc38294b00c3903fa128c67f4154d0241362662f4e9520529cf0e73b985b",
    "idempotencyKey": "old-project-cas-17",
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968",
    "importId": "01a103dc-12f1-7620-a419-0b2c5b5cba80",
    "expectedVersion": 6,
    "expectedImportVersion": 3
  },
  "error": "HTTP 409: {\"error\":{\"code\":\"backend_version_conflict\",\"message\":\"Project changed; read before retrying\",\"retryable\":false,\"currentVersion\":7}}"
}
```

```json
{
  "tool": "list_backend_annotations",
  "request": {
    "orphaned": true,
    "projectId": "01a103dc-12bf-7e0c-a0d2-006b3d881968"
  },
  "responseExcerpt": {
    "items": [
      {
        "id": "01a103dc-12ed-701c-9066-88c52312dcd9",
        "target": {
          "recordType": "node",
          "id": "01a103dc-12c1-7e9b-a080-ba6f4383212c"
        },
        "body": "Retain after proved deletion",
        "targetStatus": "orphaned"
      }
    ],
    "nextCursor": ""
  }
}
```

### Qualified providers, conflict selection and stale dependency

Executable episode: `TestBackendB41SDKProviderAndDependencyExamples`.

```json
{
  "tool": "put_backend_import_batch",
  "request": {
    "batchId": "batch-5",
    "expectedImportVersion": 1,
    "payloadHash": "c5d1c5c0b79d4bcfb31935d8474a7dd2eca9c5380014e3bc016e79b7b85aec19",
    "commands": [
      {
        "claimIdentity": {
          "decisionId": "01a103dc-12cc-77f3-8d29-decb2e7a789a",
          "recordType": "node",
          "externalKey": "handler-b",
          "target": {
            "repositoryId": "01a103dc-12c0-7aa3-a830-9418eb447ec7",
            "providerNamespace": "provider-a",
            "recordType": "node",
            "externalKey": "handler-a",
            "expectedId": "01a103dc-12c2-71ba-a6fe-44c09e535539",
            "assertionHash": "5f994ee41d82dd1eda7175c514402d752b82483e7a9023c5243016767c19c40c"
          },
          "reason": "Independent provider identifies this exact source declaration",
          "evidenceKeys": [
            "proof-handler-b"
          ]
        },
        "op": "claim_identity"
      },
      {
        "op": "upsert_node",
        "node": {
          "externalKey": "handler-b",
          "kind": "handler",
          "name": "Alternate",
          "attributes": {},
          "evidenceKeys": [
            "proof-handler-b"
          ]
        }
      },
      {
        "op": "upsert_evidence",
        "evidence": {
          "externalKey": "proof-handler-b",
          "subjectType": "node",
          "subjectKey": "handler-b",
          "method": "ast",
          "status": "explicit",
          "source": {
            "repositoryId": "01a103dc-12c0-7aa3-a830-9418eb447ec7",
            "snapshotId": "01a103dc-12cb-7cee-95d6-46d466687cd1",
            "file": "source.go",
            "contentHash": "026a97327acdc2b676b5451b7f1f1e1391e3ba2ee324ee16271ee7339f7fe85c",
            "startLine": 3,
            "endLine": 3
          },
          "explanation": "The named function is declared at this exact source line"
        }
      }
    ],
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05",
    "importId": "01a103dc-12cb-7ced-8b0a-8b9683271929"
  },
  "responseExcerpt": {
    "acceptedVersion": 2,
    "identities": [
      {
        "recordType": "node",
        "externalKey": "handler-b",
        "id": "01a103dc-12c2-71ba-a6fe-44c09e535539"
      },
      {
        "recordType": "evidence",
        "externalKey": "proof-handler-b",
        "id": "01a103dc-12cd-7887-b6a7-93861caea13a"
      }
    ]
  }
}
```

```json
{
  "tool": "preview_backend_import",
  "request": {
    "expectedImportVersion": 2,
    "baseRevisionId": "01a103dc-12c8-7768-8163-0d3b570039de",
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05",
    "importId": "01a103dc-12cb-7ced-8b0a-8b9683271929"
  },
  "responseExcerpt": {
    "version": 3,
    "state": "needs_resolution",
    "candidateHash": null
  }
}
```

```json
{
  "tool": "get_backend_import_changes",
  "request": {
    "importId": "01a103dc-12cb-7ced-8b0a-8b9683271929",
    "previewVersion": 3,
    "recordType": "assertion_conflict",
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05"
  },
  "responseExcerpt": {
    "candidateHash": null,
    "recordType": "assertion_conflict",
    "items": [
      {
        "recordType": "assertion_conflict"
      }
    ],
    "nextCursor": ""
  }
}
```

```json
{
  "tool": "put_backend_import_batch",
  "request": {
    "expectedImportVersion": 3,
    "payloadHash": "3c0b6312ee74f0ab72e65a9f80f5484177260c366a9fd39bafab6fde1c066fd7",
    "commands": [
      {
        "resolution": {
          "decisionId": "01a103dc-12d0-7497-b724-c00131156eb2",
          "recordType": "node",
          "id": "01a103dc-12c2-71ba-a6fe-44c09e535539",
          "property": {
            "kind": "name"
          },
          "conflictHash": "36c4acecd76e2bd32eff4d7971213f88203e6d32f3be6bbcbc983f2c56ab2657",
          "select": {
            "repositoryId": "01a103dc-12c0-7aa3-a830-9418eb447ec7",
            "providerNamespace": "provider-b",
            "assertionHash": "c0eaee803215a9d2dbc18425ea7ae3894d1bf4c8cac08aa770aecdb4e95c8346"
          },
          "reason": "Reviewed both declarations and chose provider-b for this exact conflict"
        },
        "op": "resolve_assertion"
      }
    ],
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05",
    "importId": "01a103dc-12cb-7ced-8b0a-8b9683271929",
    "batchId": "batch-6"
  },
  "responseExcerpt": {
    "acceptedVersion": 4,
    "identities": []
  }
}
```

```json
{
  "tool": "preview_backend_import",
  "request": {
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05",
    "importId": "01a103dc-12cb-7ced-8b0a-8b9683271929",
    "expectedImportVersion": 4,
    "baseRevisionId": "01a103dc-12c8-7768-8163-0d3b570039de"
  },
  "responseExcerpt": {
    "version": 5,
    "state": "ready",
    "candidateHash": "81f9302dc97ba81e41bd9d5b9e6e2a08e5bffb31842618c9d59ff67163b450fc"
  }
}
```

```json
{
  "tool": "begin_backend_import",
  "request": {
    "inventory": [
      {
        "category": "files",
        "status": "complete",
        "knownCount": 1,
        "denominator": 1,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "endpoints",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "datastores",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "migrations",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "producers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "consumers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "jobs",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "contracts",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "tests",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      }
    ],
    "syncPolicy": "whole-source-v1",
    "profile": "composed-source-v1",
    "idempotencyKey": "begin-8",
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05",
    "sourceScope": {
      "kind": "migrate_provider",
      "repositoryId": "01a103dc-12c0-7aa3-a830-9418eb447ec7",
      "fromProviderNamespace": "provider-b",
      "fromSnapshotId": "01a103dc-12cb-7cee-95d6-46d466687cd1",
      "reason": "Reviewed collector migration; old claims stay visible"
    },
    "scopeStatus": {
      "status": "complete",
      "gaps": []
    },
    "mode": "composed",
    "expectedVersion": 3,
    "baseRevisionId": "01a103dc-12d7-78a7-a6e1-d973117fc203",
    "manifest": {
      "repositoryName": "primary",
      "provider": {
        "name": "replacement-collector",
        "version": "2",
        "namespace": "provider-c",
        "method": "ast",
        "profiles": [
          "foundation-graph-v1",
          "relational-graph-v1",
          "runtime-flow-v1",
          "field-lineage-v1",
          "events-service-v1",
          "composed-source-v1"
        ],
        "limitations": []
      },
      "snapshot": {
        "dirty": false,
        "consistency": "verified",
        "capturedAt": "2026-10-03T10:00:00Z",
        "files": [
          {
            "path": "source.go",
            "contentHash": "32dd87de0d396c3a63a2e15b62c670389de761478f765928af29772772b38199",
            "fileType": "go",
            "analysisStatus": "analyzed"
          }
        ]
      }
    }
  },
  "responseExcerpt": {
    "syncPolicy": "whole-source-v1",
    "id": "01a103dc-12da-7c7e-9dcd-188da4c01928",
    "baseRevisionId": "01a103dc-12d7-78a7-a6e1-d973117fc203",
    "repositoryId": "01a103dc-12c0-7aa3-a830-9418eb447ec7",
    "snapshotId": "01a103dc-12da-7c83-a0e3-65e847e80cf0",
    "state": "collecting",
    "version": 1,
    "candidateHash": null
  }
}
```

```json
{
  "tool": "put_backend_import_batch",
  "request": {
    "batchId": "batch-15",
    "expectedImportVersion": 1,
    "payloadHash": "5281b3cf9da53e6d6d0a9b81824ea2a807396d2d67b662c05a72e16d4e526504",
    "commands": [
      {
        "op": "upsert_node",
        "node": {
          "externalKey": "caller",
          "kind": "handler",
          "name": "Caller",
          "attributes": {},
          "evidenceKeys": [
            "proof-caller"
          ]
        }
      },
      {
        "op": "upsert_evidence",
        "evidence": {
          "externalKey": "proof-caller",
          "subjectType": "node",
          "subjectKey": "caller",
          "method": "ast",
          "status": "explicit",
          "source": {
            "repositoryId": "01a103dc-12fc-7f60-8d4d-7626328e6db1",
            "snapshotId": "01a103dc-12fd-7527-b095-263d32d73a0b",
            "file": "source.go",
            "contentHash": "4df9211499a048cd6c55eeb1e764207d836654a4ea79cbd00d9be523e4080be7",
            "startLine": 3,
            "endLine": 3
          },
          "explanation": "The named function is declared at this exact source line"
        }
      },
      {
        "op": "upsert_edge",
        "edge": {
          "fromRef": {
            "localKey": "caller"
          },
          "toRef": {
            "base": {
              "repositoryId": "01a103dc-12e8-7056-aa86-989bccc97a16",
              "providerNamespace": "provider-remote",
              "recordType": "node",
              "externalKey": "remote",
              "expectedId": "01a103dc-12ee-7fd7-ae5c-c4c39c3bfd12",
              "assertionHash": "2bbd86c4d0f255c25937bb6f609ada502699be7187b4c92300f6bfafa2a334e4"
            }
          },
          "externalKey": "remote-call",
          "kind": "calls",
          "attributes": {},
          "evidenceKeys": [
            "proof-call"
          ]
        }
      },
      {
        "op": "upsert_evidence",
        "evidence": {
          "externalKey": "proof-call",
          "subjectType": "edge",
          "subjectKey": "remote-call",
          "method": "ast",
          "status": "explicit",
          "source": {
            "repositoryId": "01a103dc-12fc-7f60-8d4d-7626328e6db1",
            "snapshotId": "01a103dc-12fd-7527-b095-263d32d73a0b",
            "file": "source.go",
            "contentHash": "4df9211499a048cd6c55eeb1e764207d836654a4ea79cbd00d9be523e4080be7",
            "startLine": 3,
            "endLine": 3
          },
          "explanation": "The named function is declared at this exact source line"
        }
      }
    ],
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05",
    "importId": "01a103dc-12fd-7526-a328-292d22450f83"
  },
  "responseExcerpt": {
    "acceptedVersion": 2,
    "identities": [
      {
        "recordType": "node",
        "externalKey": "caller",
        "id": "01a103dc-12fe-7968-9802-84b30bfb9194"
      },
      {
        "recordType": "evidence",
        "externalKey": "proof-caller",
        "id": "01a103dc-12fe-79f7-9b2e-7e7fc906e134"
      }
    ]
  }
}
```

```json
{
  "tool": "get_backend_assertions",
  "request": {
    "importCandidate": {
      "importId": "01a103dc-12fd-7526-a328-292d22450f83",
      "importVersion": 3,
      "candidateHash": "376ee9912415e6d33dfb9e5da107a91b4b1a128cbc7ea0eea360d761405e4968"
    },
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05",
    "recordType": "edge",
    "id": "01a103dc-12fe-7ab8-9fde-f4f3c4355b02",
    "limit": 100
  },
  "responseExcerpt": {
    "documentVersion": "source-assertions-v1",
    "viewSchemaVersion": "import-candidate-v1",
    "target": {},
    "pins": {
      "viewSchemaVersion": "import-candidate-v1",
      "baseRevisionId": "01a103dc-12f9-7d47-9676-85df183d49bc",
      "baseSemanticHash": "bfd538cdbea65f0f04f828e7b8e7d845e148ebfec1e4c1568f035968a1e62726"
    },
    "basis": "candidate",
    "items": [
      {
        "assertion": {
          "recordType": "edge",
          "recordId": "01a103dc-12fe-7ab8-9fde-f4f3c4355b02",
          "owner": {
            "repositoryId": "01a103dc-12fc-7f60-8d4d-7626328e6db1"
          },
          "externalKey": "remote-call",
          "assertionHash": "ef89fce67ffdc5c76f39f58e6873e23b605866e2c10be11ca7e82b5b7f3fae17",
          "payload": {
            "recordType": "edge",
            "kind": "calls"
          },
          "evidenceIds": [
            "01a103dc-12fe-7b3f-89a2-d09ddaf609f6"
          ]
        },
        "currentness": {
          "recordType": "edge",
          "recordId": "01a103dc-12fe-7ab8-9fde-f4f3c4355b02",
          "repositoryId": "01a103dc-12fc-7f60-8d4d-7626328e6db1",
          "assertionHash": "ef89fce67ffdc5c76f39f58e6873e23b605866e2c10be11ca7e82b5b7f3fae17",
          "own": {
            "status": "current"
          },
          "dependency": {
            "status": "current"
          },
          "fields": [
            {
              "property": {
                "kind": "edge_endpoints"
              },
              "own": {
                "status": "current"
              },
              "dependency": {
                "status": "current"
              }
            }
          ]
        }
      }
    ],
    "nextCursor": ""
  }
}
```

```json
{
  "tool": "get_backend_assertions",
  "request": {
    "revisionId": "01a103dc-1338-70c8-9dbd-022f46d947c4",
    "projectId": "01a103dc-12bf-7e35-986b-2632d3940f05",
    "recordType": "edge",
    "id": "01a103dc-12fe-7ab8-9fde-f4f3c4355b02",
    "limit": 100
  },
  "responseExcerpt": {
    "documentVersion": "source-assertions-v1",
    "viewSchemaVersion": "6",
    "target": {},
    "pins": {
      "viewSchemaVersion": "6",
      "baseRevisionId": "01a103dc-1338-70c8-9dbd-022f46d947c4",
      "baseSemanticHash": "0957f9333af1872793d016a5d016ec33863f7fce205d242fb24a5249da5f79f0"
    },
    "basis": "source",
    "items": [
      {
        "assertion": {
          "recordType": "edge",
          "recordId": "01a103dc-12fe-7ab8-9fde-f4f3c4355b02",
          "owner": {
            "repositoryId": "01a103dc-12fc-7f60-8d4d-7626328e6db1"
          },
          "externalKey": "remote-call",
          "assertionHash": "ef89fce67ffdc5c76f39f58e6873e23b605866e2c10be11ca7e82b5b7f3fae17",
          "payload": {
            "recordType": "edge",
            "kind": "calls"
          },
          "evidenceIds": [
            "01a103dc-12fe-7b3f-89a2-d09ddaf609f6"
          ]
        },
        "currentness": {
          "recordType": "edge",
          "recordId": "01a103dc-12fe-7ab8-9fde-f4f3c4355b02",
          "repositoryId": "01a103dc-12fc-7f60-8d4d-7626328e6db1",
          "assertionHash": "ef89fce67ffdc5c76f39f58e6873e23b605866e2c10be11ca7e82b5b7f3fae17",
          "own": {
            "status": "current"
          },
          "dependency": {
            "status": "stale"
          },
          "fields": [
            {
              "property": {
                "kind": "edge_endpoints"
              },
              "own": {
                "status": "current"
              },
              "dependency": {
                "status": "stale"
              }
            }
          ]
        }
      }
    ],
    "nextCursor": ""
  }
}
```

### Full intent, permanent commands and SavedView-v2

Executable episode: `TestBackendB41SDKChangeLedgerAndSavedViewExamples`.

```json
{
  "tool": "preview_backend_change_proposal_commands",
  "request": {
    "expectedVersion": 1,
    "proposalRevisionId": "01a103dc-12d7-7c7a-a5fd-04694b143bb1",
    "commands": [
      {
        "target": {
          "kind": "source_identity",
          "source": {
            "recordType": "node",
            "id": "01a103dc-12c7-7506-a3b9-8dade44f4ddb",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "providerNamespace": "provider-a",
            "externalKey": "handler-a",
            "assertionHash": "416c47ab8b8396917e4482526a6d723af96664bcc90c89e0c47a6b623b8c4327"
          }
        },
        "expectedExternalKey": "handler-a",
        "newExternalKey": "desired-handler-a",
        "type": "map_identity",
        "commandId": "01a103dc-12dd-7e0e-b22e-a83c55780d6f",
        "reason": "Only provider-a gets this intended alias"
      },
      {
        "reason": "Desired structure only; no provider proof",
        "kind": "module",
        "name": "Planned module",
        "parentId": null,
        "commandId": "01a103dc-12dd-7e10-a033-85f250a34f26",
        "id": "01a103dc-12dd-7e0c-86ab-b9881ebcf6f8",
        "attributes": {},
        "type": "create_node"
      },
      {
        "type": "create_node",
        "id": "01a103dc-12dd-7e0d-a08e-a70574dc57a0",
        "attributes": {
          "analysisStatus": "complete",
          "gaps": [],
          "qualifiedName": "example.PlannedDTO"
        },
        "commandId": "01a103dc-12dd-7e18-8225-3fd5fb5de229",
        "reason": "Desired structure only; no provider proof",
        "kind": "dto",
        "name": "Planned DTO",
        "parentId": "01a103dc-12dd-7e0c-86ab-b9881ebcf6f8"
      },
      {
        "kind": "contains",
        "from": "01a103dc-12dd-7e0c-86ab-b9881ebcf6f8",
        "type": "upsert_edge",
        "commandId": "01a103dc-12dd-7e19-afe0-1d486574796f",
        "reason": "Exact desired containment",
        "to": "01a103dc-12dd-7e0d-a08e-a70574dc57a0",
        "attributes": {},
        "id": "01a103dc-12dd-7e1a-a890-d0f5c95c2472"
      },
      {
        "parentId": "01a103dc-12dd-7e0d-a08e-a70574dc57a0",
        "attributes": {
          "nullable": {
            "status": "known",
            "value": false
          },
          "cardinality": {
            "status": "known",
            "value": "one"
          },
          "analysisStatus": "complete",
          "gaps": [],
          "selector": [
            {
              "property": "value"
            }
          ],
          "nativeType": {
            "status": "known",
            "value": "string"
          }
        },
        "type": "create_node",
        "commandId": "01a103dc-12dd-7e31-b717-34c7f54c4e31",
        "reason": "Desired structure only; no provider proof",
        "id": "01a103dc-12dd-7e08-ae2e-65b5986a7753",
        "kind": "representation_field",
        "name": "value"
      },
      {
        "type": "upsert_edge",
        "reason": "Exact desired containment",
        "kind": "contains",
        "to": "01a103dc-12dd-7e08-ae2e-65b5986a7753",
        "attributes": {},
        "commandId": "01a103dc-12dd-7e32-ade2-a9aff3b32b25",
        "id": "01a103dc-12dd-7e35-9361-5c3c58b83074",
        "from": "01a103dc-12dd-7e0d-a08e-a70574dc57a0"
      },
      {
        "target": {
          "kind": "intent_identity",
          "recordType": "node",
          "id": "01a103dc-12dd-7e08-ae2e-65b5986a7753"
        },
        "expectedExternalKey": null,
        "newExternalKey": "planned.value",
        "type": "map_identity",
        "commandId": "01a103dc-12dd-7e36-99eb-8f109f9fc3ff",
        "reason": "First key for a created desired field"
      }
    ],
    "projectId": "01a103dc-12c4-7eb8-8609-ebd860078a2e",
    "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6"
  },
  "responseExcerpt": {
    "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
    "proposalRevisionId": "01a103dc-12d7-7c7a-a5fd-04694b143bb1",
    "baseRevisionId": "01a103dc-12d4-7502-a3a2-4967c977fcc9",
    "baseSemanticHash": "f796133e98ed87dafba7c58d6ed1daf61256faefc5a3c940b468fa0470374e9c",
    "documentVersion": "proposal-graph-v1",
    "semanticHash": "414b31062cb3095fbd0b634185945e91e163166bc72ad435b18b2529820e6ac9",
    "candidateHash": "98c0cb980d645c3a6ccb59d38b079e9ccddaa00beb826f43c3eb03a65dc3d757"
  }
}
```

```json
{
  "tool": "apply_backend_change_proposal_commands",
  "request": {
    "proposalRevisionId": "01a103dc-12d7-7c7a-a5fd-04694b143bb1",
    "commands": [
      {
        "newExternalKey": "desired-handler-a",
        "type": "map_identity",
        "commandId": "01a103dc-12dd-7e0e-b22e-a83c55780d6f",
        "reason": "Only provider-a gets this intended alias",
        "target": {
          "kind": "source_identity",
          "source": {
            "recordType": "node",
            "id": "01a103dc-12c7-7506-a3b9-8dade44f4ddb",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "providerNamespace": "provider-a",
            "externalKey": "handler-a",
            "assertionHash": "416c47ab8b8396917e4482526a6d723af96664bcc90c89e0c47a6b623b8c4327"
          }
        },
        "expectedExternalKey": "handler-a"
      },
      {
        "type": "create_node",
        "reason": "Desired structure only; no provider proof",
        "id": "01a103dc-12dd-7e0c-86ab-b9881ebcf6f8",
        "parentId": null,
        "commandId": "01a103dc-12dd-7e10-a033-85f250a34f26",
        "kind": "module",
        "name": "Planned module",
        "attributes": {}
      },
      {
        "type": "create_node",
        "commandId": "01a103dc-12dd-7e18-8225-3fd5fb5de229",
        "reason": "Desired structure only; no provider proof",
        "kind": "dto",
        "name": "Planned DTO",
        "parentId": "01a103dc-12dd-7e0c-86ab-b9881ebcf6f8",
        "attributes": {
          "qualifiedName": "example.PlannedDTO",
          "analysisStatus": "complete",
          "gaps": []
        },
        "id": "01a103dc-12dd-7e0d-a08e-a70574dc57a0"
      },
      {
        "type": "upsert_edge",
        "reason": "Exact desired containment",
        "id": "01a103dc-12dd-7e1a-a890-d0f5c95c2472",
        "kind": "contains",
        "from": "01a103dc-12dd-7e0c-86ab-b9881ebcf6f8",
        "commandId": "01a103dc-12dd-7e19-afe0-1d486574796f",
        "to": "01a103dc-12dd-7e0d-a08e-a70574dc57a0",
        "attributes": {}
      },
      {
        "reason": "Desired structure only; no provider proof",
        "id": "01a103dc-12dd-7e08-ae2e-65b5986a7753",
        "kind": "representation_field",
        "name": "value",
        "parentId": "01a103dc-12dd-7e0d-a08e-a70574dc57a0",
        "attributes": {
          "cardinality": {
            "status": "known",
            "value": "one"
          },
          "analysisStatus": "complete",
          "gaps": [],
          "selector": [
            {
              "property": "value"
            }
          ],
          "nativeType": {
            "status": "known",
            "value": "string"
          },
          "nullable": {
            "status": "known",
            "value": false
          }
        },
        "type": "create_node",
        "commandId": "01a103dc-12dd-7e31-b717-34c7f54c4e31"
      },
      {
        "type": "upsert_edge",
        "commandId": "01a103dc-12dd-7e32-ade2-a9aff3b32b25",
        "reason": "Exact desired containment",
        "kind": "contains",
        "from": "01a103dc-12dd-7e0d-a08e-a70574dc57a0",
        "to": "01a103dc-12dd-7e08-ae2e-65b5986a7753",
        "id": "01a103dc-12dd-7e35-9361-5c3c58b83074",
        "attributes": {}
      },
      {
        "commandId": "01a103dc-12dd-7e36-99eb-8f109f9fc3ff",
        "reason": "First key for a created desired field",
        "target": {
          "kind": "intent_identity",
          "recordType": "node",
          "id": "01a103dc-12dd-7e08-ae2e-65b5986a7753"
        },
        "expectedExternalKey": null,
        "newExternalKey": "planned.value",
        "type": "map_identity"
      }
    ],
    "candidateHash": "98c0cb980d645c3a6ccb59d38b079e9ccddaa00beb826f43c3eb03a65dc3d757",
    "idempotencyKey": "apply-8",
    "projectId": "01a103dc-12c4-7eb8-8609-ebd860078a2e",
    "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
    "expectedVersion": 1
  },
  "responseExcerpt": {
    "revision": {
      "id": "01a103dc-12e4-7a97-9653-21d62e2d3bfc",
      "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
      "documentVersion": "proposal-graph-v1",
      "baseRevisionId": "01a103dc-12d4-7502-a3a2-4967c977fcc9",
      "baseSemanticHash": "f796133e98ed87dafba7c58d6ed1daf61256faefc5a3c940b468fa0470374e9c",
      "sourceVector": {
        "documentVersion": "source-vector-v1",
        "partitions": [
          {
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "snapshotId": "01a103dc-12c5-7bd7-ba7f-906120874459",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "snapshotId": "01a103dc-12cf-71c2-82d5-b55c4fd2941b",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-b",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ],
        "snapshots": [
          {
            "id": "01a103dc-12c5-7bd7-ba7f-906120874459",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "id": "01a103dc-12cf-71c2-82d5-b55c4fd2941b",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-b",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ]
      },
      "semanticHash": "414b31062cb3095fbd0b634185945e91e163166bc72ad435b18b2529820e6ac9"
    },
    "semanticHash": "414b31062cb3095fbd0b634185945e91e163166bc72ad435b18b2529820e6ac9"
  }
}
```

```json
{
  "tool": "get_backend_evidence",
  "request": {
    "subjectId": "01a103dc-12dd-7e08-ae2e-65b5986a7753",
    "changeProposal": {
      "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
      "proposalRevisionId": "01a103dc-12e4-7a97-9653-21d62e2d3bfc"
    },
    "projectId": "01a103dc-12c4-7eb8-8609-ebd860078a2e"
  },
  "responseExcerpt": {
    "basis": "baseline",
    "target": {},
    "pins": {
      "viewSchemaVersion": "proposal-graph-v1",
      "baseRevisionId": "01a103dc-12d4-7502-a3a2-4967c977fcc9",
      "baseSemanticHash": "f796133e98ed87dafba7c58d6ed1daf61256faefc5a3c940b468fa0470374e9c"
    },
    "source": {
      "sourceVector": {
        "documentVersion": "source-vector-v1",
        "partitions": [
          {
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "snapshotId": "01a103dc-12c5-7bd7-ba7f-906120874459",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "snapshotId": "01a103dc-12cf-71c2-82d5-b55c4fd2941b",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-b",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ],
        "snapshots": [
          {
            "id": "01a103dc-12c5-7bd7-ba7f-906120874459",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "id": "01a103dc-12cf-71c2-82d5-b55c4fd2941b",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-b",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ]
      },
      "identities": [],
      "currentness": [],
      "legacyProofBases": []
    },
    "viewSchemaVersion": "proposal-graph-v1",
    "items": [],
    "nextCursor": ""
  }
}
```

```json
{
  "tool": "create_backend_saved_view",
  "request": {
    "documentVersion": "saved-view-v2",
    "name": "Exact desired presentation",
    "target": {
      "changeProposal": {
        "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
        "proposalRevisionId": "01a103dc-12e4-7a97-9653-21d62e2d3bfc"
      }
    },
    "state": {
      "kind": "flow",
      "scope": {},
      "filters": {
        "search": "",
        "accessKind": "",
        "reverseAccessKind": ""
      },
      "selection": null,
      "positions": [],
      "collapsedGroupIds": []
    },
    "idempotencyKey": "view-9",
    "projectId": "01a103dc-12c4-7eb8-8609-ebd860078a2e"
  },
  "responseExcerpt": {
    "id": "01a103dc-12fb-747e-b438-952f189c9c23",
    "version": 1,
    "documentVersion": "saved-view-v2",
    "name": "Exact desired presentation",
    "target": {},
    "pins": {
      "effective": {
        "viewSchemaVersion": "proposal-graph-v1",
        "baseRevisionId": "01a103dc-12d4-7502-a3a2-4967c977fcc9",
        "baseSemanticHash": "f796133e98ed87dafba7c58d6ed1daf61256faefc5a3c940b468fa0470374e9c"
      },
      "semanticHash": "414b31062cb3095fbd0b634185945e91e163166bc72ad435b18b2529820e6ac9"
    },
    "state": {
      "kind": "flow"
    }
  }
}
```

```json
{
  "tool": "restore_backend_change_proposal",
  "request": {
    "idempotencyKey": "restore-14",
    "projectId": "01a103dc-12c4-7eb8-8609-ebd860078a2e",
    "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
    "expectedVersion": 6,
    "proposalRevisionId": "01a103dc-130e-773b-98fe-3d3cc5b99cc2",
    "restoreRevisionId": "01a103dc-12d7-7c7a-a5fd-04694b143bb1"
  },
  "responseExcerpt": {
    "revision": {
      "id": "01a103dc-130f-7d91-b6da-ca8143a6b761",
      "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
      "documentVersion": "proposal-graph-v1",
      "baseRevisionId": "01a103dc-12d4-7502-a3a2-4967c977fcc9",
      "baseSemanticHash": "f796133e98ed87dafba7c58d6ed1daf61256faefc5a3c940b468fa0470374e9c",
      "sourceVector": {
        "documentVersion": "source-vector-v1",
        "partitions": [
          {
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "snapshotId": "01a103dc-12c5-7bd7-ba7f-906120874459",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "snapshotId": "01a103dc-12cf-71c2-82d5-b55c4fd2941b",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-b",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ],
        "snapshots": [
          {
            "id": "01a103dc-12c5-7bd7-ba7f-906120874459",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "id": "01a103dc-12cf-71c2-82d5-b55c4fd2941b",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-b",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ]
      },
      "semanticHash": "b9fda39a2bb99b64745bc732ff21e40bec8fad29a9ce306167ee1d7baaf80805"
    },
    "semanticHash": "b9fda39a2bb99b64745bc732ff21e40bec8fad29a9ce306167ee1d7baaf80805"
  }
}
```

```json
{
  "tool": "apply_backend_change_proposal_commands",
  "request": {
    "proposalRevisionId": "01a103dc-130f-7d91-b6da-ca8143a6b761",
    "commands": [
      {
        "recordType": "node",
        "name": "Handle",
        "type": "rename",
        "commandId": "01a103dc-12fb-7c76-9d81-3438c9cfccb4",
        "reason": "Reviewed desired name",
        "id": "01a103dc-12c7-7506-a3b9-8dade44f4ddb"
      }
    ],
    "candidateHash": "0777b098d5a1a8a90fb14bba5d4ea9740ed17d29cace305003a35559d45eb982",
    "idempotencyKey": "consumed-command-15",
    "projectId": "01a103dc-12c4-7eb8-8609-ebd860078a2e",
    "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
    "expectedVersion": 7
  },
  "error": "HTTP 409: {\"error\":{\"code\":\"backend_change_command_conflict\",\"message\":\"Command ID is permanently reserved by an accepted batch\",\"details\":{\"commandId\":\"01a103dc-12fb-7c76-9d81-3438c9cfccb4\"},\"retryable\":false}}"
}
```

```json
{
  "tool": "apply_backend_change_proposal_commands",
  "request": {
    "expectedVersion": 2,
    "proposalRevisionId": "01a103dc-12e4-7a97-9653-21d62e2d3bfc",
    "commands": [
      {
        "type": "rename",
        "commandId": "01a103dc-12fb-7c76-9d81-3438c9cfccb4",
        "reason": "Reviewed desired name",
        "id": "01a103dc-12c7-7506-a3b9-8dade44f4ddb",
        "recordType": "node",
        "name": "Handle"
      }
    ],
    "candidateHash": "0777b098d5a1a8a90fb14bba5d4ea9740ed17d29cace305003a35559d45eb982",
    "idempotencyKey": "apply-10",
    "projectId": "01a103dc-12c4-7eb8-8609-ebd860078a2e",
    "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6"
  },
  "responseExcerpt": {
    "revision": {
      "id": "01a103dc-12ff-7a72-b7c9-55b7cce0699e",
      "proposalId": "01a103dc-12d7-7c76-97e5-6c454e4dacd6",
      "documentVersion": "proposal-graph-v1",
      "baseRevisionId": "01a103dc-12d4-7502-a3a2-4967c977fcc9",
      "baseSemanticHash": "f796133e98ed87dafba7c58d6ed1daf61256faefc5a3c940b468fa0470374e9c",
      "sourceVector": {
        "documentVersion": "source-vector-v1",
        "partitions": [
          {
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "snapshotId": "01a103dc-12c5-7bd7-ba7f-906120874459",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "snapshotId": "01a103dc-12cf-71c2-82d5-b55c4fd2941b",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-b",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ],
        "snapshots": [
          {
            "id": "01a103dc-12c5-7bd7-ba7f-906120874459",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-a",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "id": "01a103dc-12cf-71c2-82d5-b55c4fd2941b",
            "repositoryId": "01a103dc-12c5-77ca-a58d-4d9bbb9d3780",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "provider-b",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ]
      },
      "semanticHash": "414b31062cb3095fbd0b634185945e91e163166bc72ad435b18b2529820e6ac9"
    },
    "semanticHash": "414b31062cb3095fbd0b634185945e91e163166bc72ad435b18b2529820e6ac9"
  }
}
```

```json
{
  "tool": "save_backend_saved_view",
  "request": {
    "documentVersion": "saved-view-v2",
    "name": "Same immutable desired target",
    "state": {
      "kind": "flow",
      "scope": {},
      "filters": {
        "search": "",
        "accessKind": "",
        "reverseAccessKind": ""
      },
      "selection": null,
      "positions": [],
      "collapsedGroupIds": []
    },
    "expectedVersion": 1,
    "idempotencyKey": "view-save-19",
    "projectId": "01a103dc-12c4-7eb8-8609-ebd860078a2e",
    "viewId": "01a103dc-12fb-747e-b438-952f189c9c23"
  },
  "responseExcerpt": {
    "id": "01a103dc-12fb-747e-b438-952f189c9c23",
    "version": 2,
    "documentVersion": "saved-view-v2",
    "name": "Same immutable desired target",
    "target": {},
    "pins": {
      "effective": {
        "viewSchemaVersion": "proposal-graph-v1",
        "baseRevisionId": "01a103dc-12d4-7502-a3a2-4967c977fcc9",
        "baseSemanticHash": "f796133e98ed87dafba7c58d6ed1daf61256faefc5a3c940b468fa0470374e9c"
      },
      "semanticHash": "414b31062cb3095fbd0b634185945e91e163166bc72ad435b18b2529820e6ac9"
    },
    "state": {
      "kind": "flow"
    }
  }
}
```

### Actual prior-binary metadata proof

Executable episode: `TestBackendB41SDKPriorBinaryMetadataExamples`.

The original DB hash is `3b8ff09b51a1deccf64fd0dd67c89f9736c0d715e864e0b400e5e5b258c0f3ee`, produced by prior binary `a5947af3e0104bd43339028022012896390b2a9e79c8c903c9e99efd1b59b558`. Original seed and actual later source5 head raw documents remain pinned. Five metadata pointers stay historical_metadata, the name proof stays legacy_semantic and broad proofs stay legacy_record. This service also has genuine broad support, so its record remains current; metadata-only witnesses are never promoted to typed semantic proof.

```json
{
  "tool": "begin_backend_import",
  "request": {
    "syncPolicy": "whole-source-v1",
    "profileExtension": {
      "fromProfile": "events-service-v1",
      "toProfile": "composed-source-v1"
    },
    "mode": "composed",
    "idempotencyKey": "begin-1",
    "inventory": [
      {
        "category": "files",
        "status": "complete",
        "knownCount": 1,
        "denominator": 1,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "endpoints",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "datastores",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "migrations",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "producers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "consumers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "jobs",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "contracts",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "tests",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      }
    ],
    "projectId": "01a1016f-14fa-704d-87ae-378bbd3e8b47",
    "sourceScope": {
      "kind": "add_repository"
    },
    "scopeStatus": {
      "status": "complete",
      "gaps": []
    },
    "profile": "composed-source-v1",
    "expectedVersion": 3,
    "baseRevisionId": "01a1016f-15c7-7ed0-97ce-1337fb84993b",
    "manifest": {
      "repositoryName": "sdk-oracle-addition",
      "provider": {
        "name": "sdk-example-collector",
        "version": "1",
        "namespace": "sdk-new-provider",
        "method": "ast",
        "profiles": [
          "foundation-graph-v1",
          "relational-graph-v1",
          "runtime-flow-v1",
          "field-lineage-v1",
          "events-service-v1",
          "composed-source-v1"
        ],
        "limitations": []
      },
      "snapshot": {
        "dirty": false,
        "consistency": "verified",
        "capturedAt": "2026-10-03T10:00:00Z",
        "files": [
          {
            "path": "source.go",
            "contentHash": "35f7ac3fe8a02ea5cdb114ea4517548ea8f71c9f7ee229b161e2ae91b2357794",
            "fileType": "go",
            "analysisStatus": "analyzed"
          }
        ]
      }
    }
  },
  "responseExcerpt": {
    "syncPolicy": "whole-source-v1",
    "id": "01a103dc-131b-716c-9c88-605f7c21a711",
    "baseRevisionId": "01a1016f-15c7-7ed0-97ce-1337fb84993b",
    "repositoryId": "01a103dc-131a-7cc4-8f8a-6632a773cf23",
    "snapshotId": "01a103dc-131b-716d-ab88-4b502eb33f94",
    "state": "collecting",
    "version": 1,
    "candidateHash": null
  }
}
```

```json
{
  "tool": "get_backend_evidence",
  "request": {
    "importCandidate": {
      "importId": "01a103dc-131b-716c-9c88-605f7c21a711",
      "importVersion": 3,
      "candidateHash": "593fbdcbe9a329fb3cc7f7eca64a9a67f67ed3c2f9a6a1a744711f15ecc2f609"
    },
    "projectId": "01a1016f-14fa-704d-87ae-378bbd3e8b47",
    "subjectId": "01a1016f-1523-7676-b7fa-da2865eba6eb"
  },
  "responseExcerpt": {
    "basis": "candidate",
    "target": {},
    "pins": {
      "viewSchemaVersion": "import-candidate-v1",
      "baseRevisionId": "01a1016f-15c7-7ed0-97ce-1337fb84993b",
      "baseSemanticHash": "2502e63a652004f30a118caf962f32d824fac9e465a09f7de49472b847aefcf0"
    },
    "source": {
      "sourceVector": {
        "documentVersion": "source-vector-v1",
        "partitions": [
          {
            "repositoryId": "01a1016f-14ff-7c2d-9a53-8f06a060e36b",
            "snapshotId": "01a1016f-14ff-7e72-a9ed-b08882bb6ba2",
            "provider": {
              "name": "orders-events-fixture",
              "version": "1",
              "namespace": "orders-events-fixture",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "repositoryId": "01a103dc-131a-7cc4-8f8a-6632a773cf23",
            "snapshotId": "01a103dc-131b-716d-ab88-4b502eb33f94",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "sdk-new-provider",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ],
        "snapshots": [
          {
            "id": "01a1016f-14ff-7e72-a9ed-b08882bb6ba2",
            "repositoryId": "01a1016f-14ff-7c2d-9a53-8f06a060e36b",
            "provider": {
              "name": "orders-events-fixture",
              "version": "1",
              "namespace": "orders-events-fixture",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          },
          {
            "id": "01a103dc-131b-716d-ab88-4b502eb33f94",
            "repositoryId": "01a103dc-131a-7cc4-8f8a-6632a773cf23",
            "provider": {
              "name": "sdk-example-collector",
              "version": "1",
              "namespace": "sdk-new-provider",
              "profiles": [
                "foundation-graph-v1",
                "relational-graph-v1"
              ]
            }
          }
        ]
      },
      "identities": [
        {
          "recordType": "node",
          "id": "01a1016f-1523-7676-b7fa-da2865eba6eb",
          "repositoryId": "01a1016f-14ff-7c2d-9a53-8f06a060e36b",
          "externalKey": "svc.orders",
          "assertionHash": "b4e268a5014bb4bb5f33419c410f2ef6053805ced0cb5991ef8cf8b7b839b236"
        }
      ],
      "currentness": [
        {
          "recordType": "node",
          "recordId": "01a1016f-1523-7676-b7fa-da2865eba6eb",
          "repositoryId": "01a1016f-14ff-7c2d-9a53-8f06a060e36b",
          "assertionHash": "b4e268a5014bb4bb5f33419c410f2ef6053805ced0cb5991ef8cf8b7b839b236",
          "own": {
            "status": "current"
          },
          "dependency": {
            "status": "current"
          },
          "fields": [
            {
              "property": {
                "kind": "name"
              },
              "own": {
                "status": "current"
              },
              "dependency": {
                "status": "current"
              }
            }
          ]
        }
      ],
      "legacyProofBases": [
        {
          "documentVersion": "legacy-proof-basis-v1",
          "sourceSchemaVersion": "5",
          "sourceRevisionId": "01a1016f-15c7-7ed0-97ce-1337fb84993b",
          "recordType": "node",
          "recordId": "01a1016f-1523-7676-b7fa-da2865eba6eb",
          "evidenceId": "01a1016f-1523-7466-92f0-ef47b0ec52ee",
          "revisionDocumentHash": "28dae74e8ec83ed90b08922d022ef193fee7c0805c653e03f15e5f9d084351f0",
          "sourceDocumentHash": "9fbe759e6d5ee99d3058910767b14f7c359cf24438b025be32b3262709e252b9",
          "subjectDocumentHash": "735e6d85ce6bd284f67bcbebaa4eb04eac27314698f27df97b614e36033c881e",
          "evidenceDocumentHash": "80a195e4780f3c1a54daddf90781aa1afba28f3796dc976b2b8a0bc3842b1893",
          "support": "legacy_record"
        },
        {
          "documentVersion": "legacy-proof-basis-v1",
          "sourceSchemaVersion": "5",
          "sourceRevisionId": "01a1016f-15c7-7ed0-97ce-1337fb84993b",
          "recordType": "node",
          "recordId": "01a1016f-1523-7676-b7fa-da2865eba6eb",
          "evidenceId": "01a1016f-1569-7bf7-88f5-484865c29362",
          "revisionDocumentHash": "28dae74e8ec83ed90b08922d022ef193fee7c0805c653e03f15e5f9d084351f0",
          "sourceDocumentHash": "9fbe759e6d5ee99d3058910767b14f7c359cf24438b025be32b3262709e252b9",
          "subjectDocumentHash": "735e6d85ce6bd284f67bcbebaa4eb04eac27314698f27df97b614e36033c881e",
          "evidenceDocumentHash": "98fe7d511689db082c4b6de2a6749e8a5ce609c780421490685227a4395703ec",
          "support": "historical_metadata"
        }
      ]
    },
    "viewSchemaVersion": "import-candidate-v1",
    "items": [
      {
        "id": "01a1016f-1523-7466-92f0-ef47b0ec52ee",
        "externalKey": "proof:node:svc.orders:0",
        "status": "explicit",
        "source": {
          "repositoryId": "01a1016f-14ff-7c2d-9a53-8f06a060e36b",
          "snapshotId": "01a1016f-14ff-7e72-a9ed-b08882bb6ba2"
        }
      },
      {
        "id": "01a1016f-1569-7bf7-88f5-484865c29362",
        "externalKey": "b41-legacy-proof-0",
        "status": "explicit",
        "source": {
          "repositoryId": "01a1016f-14ff-7c2d-9a53-8f06a060e36b",
          "snapshotId": "01a1016f-14ff-7e72-a9ed-b08882bb6ba2"
        }
      }
    ],
    "nextCursor": ""
  }
}
```

```json
{
  "tool": "preview_backend_import",
  "request": {
    "expectedImportVersion": 2,
    "baseRevisionId": "01a103dc-1905-7de3-bc18-41a99b418b74",
    "projectId": "01a1016f-14fa-704d-87ae-378bbd3e8b47",
    "importId": "01a103dc-1b71-7b99-9586-773415fc2daa"
  },
  "error": "HTTP 422: {\"error\":{\"code\":\"backend_import_invalid\",\"message\":\"Fresh proof must address an own semantic payload property\",\"details\":{\"path\":\"evidence.propertyPath\"},\"retryable\":false}}"
}
```

```json
{
  "tool": "get_backend_evidence",
  "request": {
    "revisionId": "01a103dc-1905-7de3-bc18-41a99b418b74",
    "projectId": "01a1016f-14fa-704d-87ae-378bbd3e8b47",
    "evidenceId": "01a1016f-1569-7bf7-88f5-484865c29362"
  },
  "error": "HTTP 422: {\"error\":{\"code\":\"backend_import_invalid\",\"message\":\"Original proof basis hashes or membership changed\",\"details\":{\"path\":\"legacyProofBasis\"},\"retryable\":false}}"
}
```

### Exact incremental scope and unchanged proof

Executable episode: `TestBackendB41SDKIncrementalExample`.

```json
{
  "tool": "begin_backend_import",
  "request": {
    "sourceScope": {
      "kind": "reconcile",
      "repositoryId": "01a103dc-12d1-7512-81a1-72a18a471e2b",
      "providerNamespace": "provider-a"
    },
    "scopeStatus": {
      "status": "complete",
      "gaps": []
    },
    "syncPolicy": "incremental-source-v1",
    "mode": "composed",
    "baseRevisionId": "01a103dc-12d8-7ab8-be96-44105a6adba6",
    "manifest": {
      "repositoryName": "incremental",
      "provider": {
        "name": "sdk-example-collector",
        "version": "1",
        "namespace": "provider-a",
        "method": "ast",
        "profiles": [
          "foundation-graph-v1",
          "relational-graph-v1",
          "runtime-flow-v1",
          "field-lineage-v1",
          "events-service-v1",
          "composed-source-v1"
        ],
        "limitations": []
      },
      "snapshot": {
        "dirty": false,
        "consistency": "verified",
        "capturedAt": "2026-10-03T10:00:00Z",
        "files": [
          {
            "path": "source.go",
            "contentHash": "c62143606d67249acf03c4bfece76be72d88f6d7135d51323fa1264ee7bef10f",
            "fileType": "go",
            "analysisStatus": "analyzed"
          },
          {
            "path": "spare.go",
            "contentHash": "378852932d230f7bcf1d374746d97157236009b113616557968ac4855a520141",
            "fileType": "go",
            "analysisStatus": "analyzed"
          }
        ]
      }
    },
    "inventory": [
      {
        "category": "files",
        "status": "complete",
        "knownCount": 2,
        "denominator": 2,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "endpoints",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "datastores",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "migrations",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "producers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "consumers",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "jobs",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "contracts",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      },
      {
        "category": "tests",
        "status": "complete",
        "knownCount": 0,
        "denominator": 0,
        "discoverySource": "inert Go declarations in SDK example",
        "gaps": [],
        "reason": ""
      }
    ],
    "changeManifest": {
      "scope": "affected-subgraph",
      "files": [
        {
          "kind": "modified",
          "path": "source.go",
          "beforeHash": "6b7cb66049f59257a7b9a8882fb8942ec67a9799f7157aaf108bec916030da6c",
          "afterHash": "c62143606d67249acf03c4bfece76be72d88f6d7135d51323fa1264ee7bef10f"
        }
      ],
      "affectedRoots": [
        {
          "recordType": "node",
          "id": "01a103dc-12d2-7dfb-a667-f1fab6220f96"
        }
      ]
    },
    "profile": "composed-source-v1",
    "expectedVersion": 3,
    "idempotencyKey": "begin-8",
    "projectId": "01a103dc-12c8-7245-ab00-e8dc0813e797"
  },
  "responseExcerpt": {
    "syncPolicy": "incremental-source-v1",
    "id": "01a103dc-12dc-7be3-a3f9-a5e955528134",
    "baseRevisionId": "01a103dc-12d8-7ab8-be96-44105a6adba6",
    "repositoryId": "01a103dc-12d1-7512-81a1-72a18a471e2b",
    "snapshotId": "01a103dc-12dc-7be7-a6ec-7a72f20c953b",
    "state": "collecting",
    "version": 1,
    "candidateHash": null
  }
}
```

```json
{
  "tool": "preview_backend_import",
  "request": {
    "expectedImportVersion": 2,
    "baseRevisionId": "01a103dc-12d8-7ab8-be96-44105a6adba6",
    "projectId": "01a103dc-12c8-7245-ab00-e8dc0813e797",
    "importId": "01a103dc-12dc-7be3-a3f9-a5e955528134"
  },
  "responseExcerpt": {
    "version": 3,
    "state": "ready",
    "candidateHash": "ceaf5eda07034104b1e93b51960fc64d22ebae6e113d1284e31814cb7bcfbb80"
  }
}
```

```json
{
  "tool": "get_backend_assertions",
  "request": {
    "id": "01a103dc-12d3-7097-a880-078ee2c06239",
    "limit": 100,
    "revisionId": "01a103dc-12e4-7841-a1b3-96d9064aac89",
    "projectId": "01a103dc-12c8-7245-ab00-e8dc0813e797",
    "recordType": "node"
  },
  "responseExcerpt": {
    "documentVersion": "source-assertions-v1",
    "viewSchemaVersion": "6",
    "target": {},
    "pins": {
      "viewSchemaVersion": "6",
      "baseRevisionId": "01a103dc-12e4-7841-a1b3-96d9064aac89",
      "baseSemanticHash": "34ca36f0f7232c9fa0cef39825522a9442f15d5a6f047ce3532beb6ca2b9f241"
    },
    "basis": "source",
    "items": [
      {
        "assertion": {
          "recordType": "node",
          "recordId": "01a103dc-12d3-7097-a880-078ee2c06239",
          "owner": {
            "repositoryId": "01a103dc-12d1-7512-81a1-72a18a471e2b"
          },
          "externalKey": "spare",
          "assertionHash": "17c254c6ee9c8624d531a90de0ee7b171ddfd3392b0f89f29a20784c7e7ffa5a",
          "payload": {
            "recordType": "node",
            "kind": "handler",
            "name": "Spare"
          },
          "evidenceIds": [
            "01a103dc-12d3-71eb-956d-7e7d4d9e3d38"
          ]
        },
        "currentness": {
          "recordType": "node",
          "recordId": "01a103dc-12d3-7097-a880-078ee2c06239",
          "repositoryId": "01a103dc-12d1-7512-81a1-72a18a471e2b",
          "assertionHash": "17c254c6ee9c8624d531a90de0ee7b171ddfd3392b0f89f29a20784c7e7ffa5a",
          "own": {
            "status": "current"
          },
          "dependency": {
            "status": "current"
          },
          "fields": [
            {
              "property": {
                "kind": "name"
              },
              "own": {
                "status": "current"
              },
              "dependency": {
                "status": "current"
              }
            }
          ]
        }
      }
    ],
    "nextCursor": ""
  }
}
```

## Shared fixture and helpers

The first snapshot has main.go and independent.go. The second renames ListOrders,
removes its call and removes independent.go. Hash the exact source bytes.

```javascript
const { createHash } = require('node:crypto');
const sha = text => createHash('sha256').update(text).digest('hex');
const sorted = v => Array.isArray(v) ? v.map(sorted) : v && typeof v === 'object'
  ? Object.fromEntries(Object.keys(v).sort().map(k => [k, sorted(v[k])])) : v;
const provider = { name:'local-agent', version:'1', namespace:'qa-foundation',
  method:'ast', profiles:['foundation-graph-v1'], limitations:['Fixture, not runtime proof'] };
const oldText = 'func ListOrders() { independentCall() }';
const newText = 'func ListOrdersV2() { return }';
const otherText = 'func independentCall() {}';
const oldHash = sha(oldText), newHash = sha(newText), otherHash = sha(otherText);
const file = (path, contentHash) => ({path, contentHash, fileType:'go', analysisStatus:'analyzed'});
function inventory(count, partial=false) {
  return ['files','endpoints','datastores','migrations','producers','consumers','jobs','contracts','tests']
    .map(category => ({category, status:['files','endpoints','datastores'].includes(category)
      ? (partial && category !== 'datastores' ? 'partial' : 'complete') : 'unsupported',
      knownCount:category === 'files' ? count : category === 'endpoints' && !partial ? 1 : 0,
      denominator:category === 'files' && !partial ? count : category === 'endpoints' && !partial
        ? 1 : category === 'datastores' ? 0 : null, discoverySource:'foundation-fixture',
      gaps:partial && ['files','endpoints'].includes(category) ? ['main.go unavailable'] : [],
      reason:['files','endpoints','datastores'].includes(category) ? '' : 'Not analyzed by fixture'}));
}
function beginInput(key, files, mode='initial', repositoryId, partial=false) {
  return {projectId:project.id, expectedVersion:project.version, baseRevisionId:project.currentRevisionId,
    idempotencyKey:key, mode, ...(mode === 'reconcile' ? {repositoryId,
      graphScope:{profile:'foundation-graph-v1',status:partial?'partial':'complete',gaps:partial?['main.go unavailable']:[]}} : {}),
    manifest:{repositoryName:'orders-demo',provider,snapshot:{dirty:false,consistency:'verified',
      capturedAt:new Date().toISOString(),files}}, inventory:inventory(files.length,partial)};
}
const n = (key, kind, name, ev) => ({op:'upsert_node',node:{externalKey:key,kind,name,
  attributes:kind === 'http_operation' ? {method:'GET',path:'/orders'} : {},evidenceKeys:[ev]}});
const e = (key, kind, fromKey, toKey, ev) => ({op:'upsert_edge',edge:{externalKey:key,kind,
  fromKey,toKey,attributes:{},evidenceKeys:[ev]}});
const ev = (s, key, type, subject, file, contentHash, explanation) => ({op:'upsert_evidence',
  evidence:{externalKey:key,subjectType:type,subjectKey:subject,method:'ast',status:'explicit',
    source:{repositoryId:s.repositoryId,snapshotId:s.snapshotId,file,contentHash,startLine:1,endLine:1},explanation}});
const batchInput = (s, batchId, version, commands) => ({projectId:project.id,importId:s.id,
  batchId,expectedImportVersion:version,payloadHash:sha(JSON.stringify(sorted(commands))),commands});
const commitInput = (s, p, key) => ({projectId:project.id,importId:s.id,expectedVersion:project.version,
  expectedImportVersion:p.version,candidateHash:p.candidateHash,idempotencyKey:key});
```

Complete zero datastores is justified only for this fully known tiny fixture.
Unknown migrations/producers/consumers/jobs/contracts/tests are unsupported with
null denominators, keeping overall inventory coverage partial.

## First import: empty revision to five nodes and two edges

Start with a project created through the pinned backend-overview workflow and its
empty revision. Save the returned project as `project`; do not create a duplicate
if it already exists. A fresh isolated fixture may use:

```javascript
let project = await tool('create_backend_project', {name:'Orders fixture',idempotencyKey:'fixture-project'});
const begin1 = beginInput('fixture-begin-1',[file('main.go',oldHash),file('independent.go',otherHash)]);
const s1 = await tool('begin_backend_import',begin1);
const subjects = [n('service:orders','service','Orders service','ev-service'),
  n('module:orders','module','Orders module','ev-module'),
  n('operation:orders','http_operation','GET /orders','ev-operation'),
  n('handler:orders','handler','ListOrders','ev-handler'),
  n('handler:independent','handler','IndependentHandler','ev-independent'),
  e('handles:orders','handles','operation:orders','handler:orders','ev-handles'),
  e('calls:independent','calls','handler:orders','handler:independent','ev-calls')];
const evidence1 = [['ev-service','node','service:orders'],['ev-module','node','module:orders'],
  ['ev-operation','node','operation:orders'],['ev-handler','node','handler:orders'],
  ['ev-handles','edge','handles:orders'],['ev-calls','edge','calls:independent']]
  .map(([key,type,subject]) => ev(s1,key,type,subject,'main.go',oldHash,'Original source assertion'));
evidence1.push(ev(s1,'ev-independent','node','handler:independent','independent.go',otherHash,'Independent declaration'));
const batch1 = batchInput(s1,'fixture-batch-1',s1.version,[...subjects,...evidence1]);
const receipt1 = await tool('put_backend_import_batch',batch1);
const p1 = await tool('preview_backend_import',{projectId:project.id,importId:s1.id,
  expectedImportVersion:receipt1.acceptedVersion,baseRevisionId:project.currentRevisionId});
// Require p1.state === 'ready' and nonnull candidateHash before commit.
const commit1 = commitInput(s1,p1,'fixture-commit-1');
const r1 = await tool('commit_backend_import',commit1); project = r1.project;
const g1 = await tool('query_backend_graph',{projectId:project.id,revisionId:r1.revision.id,recordType:'nodes'});
const edges1 = await tool('query_backend_graph',{projectId:project.id,revisionId:r1.revision.id,recordType:'edges'});
const ids = Object.fromEntries([...g1.nodes,...edges1.edges].map(x => [x.externalKey,x.id]));
```

Expected: five nodes/two edges/seven evidence records, fixed revision r1.revision.id,
no stale records, partial inventory for unsupported categories. Read coverage.
Failure: evidence with a wrong file hash is backend_import_invalid; no batch is
accepted. Correct source/hash and send a new batchId at the unchanged session
version. Unresolved references yield needs_resolution/null preview hash; add or
repair their bundles in a new batch and preview again, never commit that null hash.

## Lost response: replay the whole request

Starting from the accepted first batch/commit, suppose the response was lost.
These exact requests recover their original receipts even after version advancement:

```javascript
const recoveredBatch = await tool('put_backend_import_batch',batch1);
const recoveredCommit = await tool('commit_backend_import',commit1);
const status1 = await tool('get_backend_import',{projectId:project.id,importId:s1.id});
```

Expected: original identities/acceptedVersion and original r1 receipt;
status1.committedRevisionId identifies r1 even after a later head. Altering commands
under batch1.batchId returns backend_import_batch_conflict; altering versions/hash
under commit1.idempotencyKey returns backend_idempotency_conflict. Recover by
replaying the saved original input, then reread current state. Resume after restart
with list_backend_imports/get_backend_import and acceptedBatches pagination.

## Explicit rename and proven deletion

Start from r1 and the same provider/repository. Capture verified new main.go and
complete file inventory proving independent.go absent. Omitted initial mode on this
sourced base returns backend_reimport_unsupported; recovery is an explicit new
reconcile begin request/key, not replacing versions inside the old request.

```javascript
const begin2 = beginInput('fixture-begin-2',[file('main.go',newHash)],'reconcile',s1.repositoryId);
const s2 = await tool('begin_backend_import',begin2);
const map2 = batchInput(s2,'fixture-map-2',s2.version,[{op:'map_identity',identity:{recordType:'node',
  fromExternalKey:'handler:orders',toExternalKey:'handler:orders:v2',expectedId:ids['handler:orders'],
  reason:'Same handler renamed',evidenceKeys:['ev-handler-v2']}}]);
const mapped = await tool('put_backend_import_batch',map2);
const deletes = ['calls:independent','handler:independent'].map(externalKey => ({op:'delete_assertion',
  deletion:{recordType:externalKey.startsWith('calls:')?'edge':'node',externalKey,
    expectedId:ids[externalKey],reason:'Removed from verified complete fixture'}}));
const current = [n('service:orders','service','Orders service','ev-service'),
  n('module:orders','module','Orders module','ev-module'),
  n('operation:orders','http_operation','GET /orders','ev-operation'),
  n('handler:orders:v2','handler','ListOrdersV2','ev-handler-v2'),
  e('handles:orders','handles','operation:orders','handler:orders:v2','ev-handles')];
const evidence2 = [['ev-service','node','service:orders'],['ev-module','node','module:orders'],
  ['ev-operation','node','operation:orders'],['ev-handler-v2','node','handler:orders:v2'],
  ['ev-handles','edge','handles:orders']].map(([key,type,subject]) =>
    ev(s2,key,type,subject,'main.go',newHash,'Current source assertion'));
const batch2 = batchInput(s2,'fixture-batch-2',mapped.acceptedVersion,[...current,...deletes,...evidence2]);
const receipt2 = await tool('put_backend_import_batch',batch2);
const p2 = await tool('preview_backend_import',{projectId:project.id,importId:s2.id,
  expectedImportVersion:receipt2.acceptedVersion,baseRevisionId:r1.revision.id});
// Inspect ready diagnostics and saved source/identity/deletion pages at p2.version.
const commit2 = commitInput(s2,p2,'fixture-commit-2');
const r2 = await tool('commit_backend_import',commit2); project = r2.project;
const comparison = await tool('compare_backend_revisions',{projectId:project.id,
  fromRevisionId:r1.revision.id,toRevisionId:r2.revision.id,limit:1});
```

Expected: renamed handler keeps ids['handler:orders']; other retained UUIDs stay
stable. Four nodes/one edge remain; comparison nodes={added:0,removed:1,modified:1},
edges={added:0,removed:1,modified:0}, identityMappings=1 and sourceChanges=2.
Inventory still discloses unsupported categories. Open removed subjects/evidence
at r1; comparison pages retain both pins and comparisonHash.

## Partial source: retain stale assertions

Start from r2. main.go is unavailable; only a metadata.go service assertion is
reobserved. Use partial graphScope and partial files/endpoints inventory. Never
stage delete_assertion for that missing file. Partial deletion proof instead yields
needs_resolution/null hash; remove the unsafe staged decision and preview again.

```javascript
const metadataHash = sha('service metadata');
const begin3 = beginInput('fixture-begin-3',[file('metadata.go',metadataHash)],'reconcile',s1.repositoryId,true);
const s3 = await tool('begin_backend_import',begin3);
const batch3 = batchInput(s3,'fixture-batch-3',s3.version,
  [n('service:orders','service','Orders service','ev-service'),
   ev(s3,'ev-service','node','service:orders','metadata.go',metadataHash,'Only service reobserved')]);
const receipt3 = await tool('put_backend_import_batch',batch3);
const p3 = await tool('preview_backend_import',{projectId:project.id,importId:s3.id,
  expectedImportVersion:receipt3.acceptedVersion,baseRevisionId:r2.revision.id});
const commit3 = commitInput(s3,p3,'fixture-commit-3');
const r3 = await tool('commit_backend_import',commit3); project = r3.project;
const coverage3 = await tool('get_backend_coverage',{projectId:project.id,revisionId:r3.revision.id});
```

Expected: same four node/one edge UUIDs; three stale nodes, one stale edge and four
stale evidence records. coverage3.coverage.status is partial; snapshots include
primary and retained_provenance. Old handler evidence stays pinned to r2's source.
Missing main.go is unknown source absence, not deletion. Final answer names these
gaps and consistency instead of claiming executed behavior or full reconstruction.

## PostgreSQL and SQLite: captured source imports

The static fixture sources are under
`internal/backendmodel/testdata/relational/orders/{postgresql,sqlite}/{v1,v2}/`:
`schema.sql`, `models.go` and ordered `migrations/*.sql`. They are declarations,
including unsupported bodies and a create→drop source_only target, not SQL to run.
The complete authored command bundles are the adjacent `commands.json` inputs;
substitute the returned session/history tokens as the executable example does in
`internal/mcp/tools_backend_relational_example_test.go`. Do not use expected.json
as import data. Provider/repository/manifest hashes come from those exact inputs.
Use the ordered import protocol, not a partial request excerpt as a begin input.

The following are actual Task4 MCP SDK capture excerpts from isolated fixture
stores, after cold begin→batch→ready preview→commit. IDs/hashes identify those
fixture runs only; for another server use its returned IDs and source hashes.
No guideSetId is inferred from these data revision pins: select/verify the current
advertised guide set first and pin every related guide read to that same set.
Selected response fields are shown; omitted coverage/limitations are still part
of the full response and must accompany an answer.

### Postgresql fixture: exact read pins

Ready preview: schema 2, 48 nodes, 50 edges and 141 evidence. The commit and identical lost-response replay returned revision `01a0f4e1-7969-770e-bb3f-5e7f461bb988`. Four tables and three physical FK relationships were read at that revision; unsupported migration/body coverage remains disclosed.

`query_backend_database` input:

```json
{
  "projectId": "01a0f4e1-72d0-79fb-840a-8472b0ef5ef9",
  "revisionId": "01a0f4e1-7969-770e-bb3f-5e7f461bb988",
  "datastoreId": "01a0f4e1-786b-788b-98b8-4368f1508d17",
  "facetKey": "sql",
  "recordType": "tables"
}
```

Selected table-page fields and its first complete TableItem:

```json
{
  "projectId": "01a0f4e1-72d0-79fb-840a-8472b0ef5ef9",
  "revisionId": "01a0f4e1-7969-770e-bb3f-5e7f461bb988",
  "semanticHash": "95951ebad2e8557668e0e439ac02599642ff5257800ced4f1f161d283466594a",
  "datastoreId": "01a0f4e1-786b-788b-98b8-4368f1508d17",
  "facetKey": "sql",
  "recordType": "tables",
  "facetStatus": "current",
  "nextCursor": "",
  "tableItems": [
    {
      "tableId": "01a0f4e1-786b-7ab4-a240-d57949761c40",
      "schemaId": "01a0f4e1-786b-794b-bfb0-07625bf77f59",
      "qualifiedName": "public.users",
      "columnCount": 4,
      "facetKeys": [
        "orm",
        "sql"
      ],
      "driftStatus": "unknown"
    }
  ],
  "relationshipItems": []
}
```

This excerpt shows one item, not the complete table inventory. The actual page has four; tableItems is populated and relationshipItems empty. Keep this revision for node/graph/evidence reads, and fully page any larger result.

### Sqlite fixture: exact read pins

Ready preview: schema 2, 47 nodes, 49 edges and 139 evidence. The commit and identical lost-response replay returned revision `01a0f4e1-88ac-7c51-9228-98e8e929145d`. Four tables and three physical FK relationships were read at that revision; unsupported migration/body coverage remains disclosed.

`query_backend_database` input:

```json
{
  "facetKey": "sql",
  "recordType": "tables",
  "projectId": "01a0f4e1-82d2-768f-aebf-d90b8bda7df6",
  "revisionId": "01a0f4e1-88ac-7c51-9228-98e8e929145d",
  "datastoreId": "01a0f4e1-87ad-780c-9000-7cec0b796898"
}
```

Selected table-page fields and its first complete TableItem:

```json
{
  "projectId": "01a0f4e1-82d2-768f-aebf-d90b8bda7df6",
  "revisionId": "01a0f4e1-88ac-7c51-9228-98e8e929145d",
  "semanticHash": "92280b25f86b4e2851186715d5bd624da362f6ae49d609015529c77059db2592",
  "datastoreId": "01a0f4e1-87ad-780c-9000-7cec0b796898",
  "facetKey": "sql",
  "recordType": "tables",
  "facetStatus": "current",
  "nextCursor": "",
  "tableItems": [
    {
      "tableId": "01a0f4e1-87ad-7a4d-af57-37a7a778920e",
      "schemaId": "01a0f4e1-87ad-78d0-a596-13518f821961",
      "qualifiedName": "main.users",
      "columnCount": 4,
      "facetKeys": [
        "orm",
        "sql"
      ],
      "driftStatus": "unknown"
    }
  ],
  "relationshipItems": []
}
```

This excerpt shows one item, not the complete table inventory. The actual page has four; tableItems is populated and relationshipItems empty. Keep this revision for node/graph/evidence reads, and fully page any larger result.

### Ordered composite FK and participation

The PostgreSQL SQL orders→users relationship below is a complete captured RelationshipItem. Pair order is tenant_id→tenant_id, user_id→id. sourceCardinality means source rows per target; targetCardinality means target rows per source. Its explicit source min0 does not require every user to have an order.

```json
{
  "edgeId": "01a0f4e1-786f-752f-b96a-2fc746765406",
  "constraintId": "01a0f4e1-786e-76c8-9d50-0edebce9c147",
  "sourceTableId": "01a0f4e1-786c-725e-9695-9a88bd4cef9f",
  "targetTableId": "01a0f4e1-786b-7ab4-a240-d57949761c40",
  "columnPairs": [
    {
      "fromColumnId": "01a0f4e1-786c-73ef-9bde-d2bbd9ecd896",
      "toColumnId": "01a0f4e1-786b-7c5a-9d7b-75f947a6aaa8"
    },
    {
      "fromColumnId": "01a0f4e1-786c-7770-9ac5-0c4a1349d50b",
      "toColumnId": "01a0f4e1-786b-7ddb-a873-748b2d3ad824"
    }
  ],
  "evidenceIds": [
    "01a0f4e1-786a-7c35-bd52-ef0ff60c4725"
  ],
  "sourceCardinality": {
    "min": 0,
    "max": "many",
    "basis": [
      "Declared relationship 01a0f4e1-786f-752f-b96a-2fc746765406 does not require a source row for every target",
      "Current explicit nonmatching global key 01a0f4e1-786e-753f-9765-ed3ecb77e1ad with evidence 01a0f4e1-7869-796c-ab6d-073cb6adfc71",
      "Complete selected constraint inventory for 01a0f4e1-786c-725e-9695-9a88bd4cef9f has no matching global unique key"
    ]
  },
  "targetCardinality": {
    "min": 1,
    "max": "1",
    "basis": [
      "Current explicit complete key 01a0f4e1-786e-7208-89f4-3be9120109fa with evidence 01a0f4e1-7869-759d-8412-f35b225c4843",
      "Current explicit nonmatching global key 01a0f4e1-786e-737c-a5e6-b30f34fbbfe6 with evidence 01a0f4e1-7869-7799-9188-f76926d19a0d",
      "Selected source column 01a0f4e1-786c-73ef-9bde-d2bbd9ecd896 nullable known false with evidence 01a0f4e1-7866-7947-933f-59b3e829d82e",
      "Selected source column 01a0f4e1-786c-7770-9ac5-0c4a1349d50b nullable known false with evidence 01a0f4e1-7866-7d2f-b46d-898b7f02ac33",
      "Current explicit nondeferrable MATCH SIMPLE 01a0f4e1-786e-76c8-9d50-0edebce9c147 with selected source nullability"
    ]
  },
  "status": "explicit",
  "targetReason": null
}
```

A join table stays a physical table plus its FK edges; no synthetic N:M relationship is added. Matching complete current target uniqueness can prove max1 independently of the minimum. Conditional/expression indices do not establish a global column-only key.

### Unknown minimum with an independently proved maximum

These additional ORM examples were captured through real public MCP HTTP calls in the Task4 preliminary run, separately from the SDK captures above. They are source-only inspection examples, not final frozen-binary acceptance. SQL-only datastore/schema descriptors still have ORM facets on descendants; discover those facets through the whole pinned hierarchy. Each captured ORM page reports facetStatus unknown, but a complete current explicit target key proves max1 while MATCH/deferrability leaves min unknown.

postgresql — exact request and selected response fields:

```json
{
  "projectId": "01a0f4f3-d32a-7b43-bd5a-bb44100b3095",
  "revisionId": "01a0f4f3-d467-7dae-9184-56f26af8eafa",
  "datastoreId": "01a0f4f3-d41f-7a6e-81e7-562682ba1df2",
  "facetKey": "orm",
  "recordType": "relationships",
  "limit": 500
}
```

```json
{
  "revisionId": "01a0f4f3-d467-7dae-9184-56f26af8eafa",
  "semanticHash": "f8a4f96d5180f9c36eb2492dceabc54f49979e68fc24443884ddd2b0d07d3496",
  "facetKey": "orm",
  "facetStatus": "unknown",
  "relationshipItems": [
    {
      "edgeId": "01a0f4f3-d424-71df-8ef5-f2fcc49bbf05",
      "targetCardinality": {
        "min": null,
        "max": "1",
        "basis": [
          "Current explicit complete key 01a0f4f3-d422-7c04-bb16-c09f19a91427 with evidence 01a0f4f3-d41d-74c8-b304-ee0ee826f061",
          "Current explicit nonmatching global key 01a0f4f3-d422-7d95-8f19-4c7cbdb13926 with evidence 01a0f4f3-d41d-76c0-a8a1-c52e436b7d74",
          "Missing selected key proof 01a0f4f3-d424-7570-83c6-caeb646ec067",
          "Selected source column 01a0f4f3-d420-786e-8b54-f5be7a170285 nullable known false with evidence 01a0f4f3-d41a-7f3f-ae93-0c7f9f210885",
          "Selected source column 01a0f4f3-d420-7c62-acac-4b2ddfc67e05 nullable known true with evidence 01a0f4f3-d41b-72ed-9f8b-71f78cb1df4f",
          "Minimum unknown: selected nullability, nondeferrable MATCH SIMPLE enforcement or target key is not established for 01a0f4f3-d424-71df-8ef5-f2fcc49bbf05"
        ]
      }
    }
  ]
}
```

The full page also reports missing selected facet/key proofs and incomplete ORM analysis. Keep each basis proof/limitation; an unknown minimum does not erase the separately established maximum.

sqlite — exact request and selected response fields:

```json
{
  "projectId": "01a0f4f3-d629-7a56-9b13-941945aae81e",
  "revisionId": "01a0f4f3-d74b-78f9-8250-6da0d87e2e1e",
  "datastoreId": "01a0f4f3-d707-7e4d-9dde-97d7ba2d1b90",
  "facetKey": "orm",
  "recordType": "relationships",
  "limit": 500
}
```

```json
{
  "revisionId": "01a0f4f3-d74b-78f9-8250-6da0d87e2e1e",
  "semanticHash": "a3796ce3496a1c801ea90e63b3e7d74008660c82f8740b23ba1c9fb773a8625c",
  "facetKey": "orm",
  "facetStatus": "unknown",
  "relationshipItems": [
    {
      "edgeId": "01a0f4f3-d70c-7056-a2ad-571210113e3b",
      "targetCardinality": {
        "min": null,
        "max": "1",
        "basis": [
          "Current explicit complete key 01a0f4f3-d70a-7c87-bf23-9fa80456aa89 with evidence 01a0f4f3-d705-7b1a-a192-a5ce1cb0f920",
          "Current explicit nonmatching global key 01a0f4f3-d70a-7e14-aec2-089ba7bde04e with evidence 01a0f4f3-d705-7d02-94c2-81b0092b0edf",
          "Missing selected key proof 01a0f4f3-d70c-738d-84cf-cbf7f211a9c8",
          "Selected source column 01a0f4f3-d708-7bbe-a26b-f59a98c58fbc nullable known false with evidence 01a0f4f3-d703-753f-abb4-c1dc179d8ca4",
          "Selected source column 01a0f4f3-d708-7f12-ae40-b898983c31be nullable known true with evidence 01a0f4f3-d703-7bd2-a983-8f2fc3282701",
          "Minimum unknown: selected nullability, nondeferrable MATCH SIMPLE enforcement or target key is not established for 01a0f4f3-d70c-7056-a2ad-571210113e3b"
        ]
      }
    }
  ]
}
```

The full page also reports missing selected facet/key proofs and incomplete ORM analysis. Keep each basis proof/limitation; an unknown minimum does not erase the separately established maximum.

### SQL/ORM drift with pinned evidence

At PostgreSQL revision `01a0f4e1-7969-770e-bb3f-5e7f461bb988`, column `01a0f4e1-786c-7a9f-a061-dfdfa09fa46e` (`column:orders:total`) has SQL nativeType `numeric(18,2)` and ORM `numeric(12,2)`. SQLite retains its own `NUMERIC(18,2)` versus ORM `DECIMAL(12,2)` native strings. The captured PostgreSQL comparison and SQL evidence excerpt are:

```json
{
  "id": "01a0f4e1-786c-7a9f-a061-dfdfa09fa46e",
  "facetComparison": {
    "status": "different",
    "pairs": [
      {
        "leftFacetKey": "orm",
        "rightFacetKey": "sql",
        "status": "different",
        "changedPaths": [
          "/nativeType"
        ],
        "definitionDifferent": false
      }
    ]
  }
}
```

```json
{
  "id": "01a0f4e1-7867-72c4-96f2-f4211d90fad7",
  "subjectId": "01a0f4e1-786c-7a9f-a061-dfdfa09fa46e",
  "propertyPath": "/attributes/facets/sql",
  "status": "explicit",
  "source": {
    "repositoryId": "01a0f4e1-732e-7ff7-ad8a-6cc87bab3a8c",
    "snapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
    "file": "postgresql/v1/schema.sql",
    "contentHash": "902c33120041178dc4bcca6cbbd512d696e6ee3c8c346f12eb390c028e4adc88",
    "startLine": 20,
    "endLine": 20
  }
}
```

Pair fields are leftFacetKey/rightFacetKey; changedPaths sort as facet-relative JSON Pointers. A known contradiction remains different despite unrelated unknowns. definitionDifferent is separate from semantic property paths. Do not use source/completeness/freshness metadata as changed properties or interpret text inequality as SQL-equivalence analysis.

### Partial omission retains old facet snapshots

The SDK partial snapshot refreshed only table:orders SQL, then committed revision `01a0f4e1-7dca-7acc-83cf-67e3b534a3f0`. The omitted status column `01a0f4e1-786c-78e1-8704-d1553270b80c` retained both SQL and ORM claims, including different known DEFAULT values, at the original source snapshot:

```json
{
  "id": "01a0f4e1-786c-78e1-8704-d1553270b80c",
  "externalKey": "column:orders:status",
  "freshness": {
    "status": "stale",
    "confirmedSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
    "reasons": [
      "not_reobserved"
    ]
  },
  "attributes": {
    "facets": {
      "sql": {
        "sourceSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
        "freshness": {
          "status": "stale",
          "confirmedSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
          "reasons": [
            "not_reobserved"
          ]
        },
        "defaultExpression": {
          "value": "'draft'",
          "status": "known"
        }
      },
      "orm": {
        "sourceSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
        "freshness": {
          "status": "stale",
          "confirmedSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
          "reasons": [
            "not_reobserved"
          ]
        },
        "defaultExpression": {
          "status": "known",
          "value": "'pending'"
        }
      }
    }
  }
}
```

This is a selected-field excerpt of a retained node. Partial scope/stale evidence keeps coverage partial; omission never removes a facet or subject. A metadata-only datastore/routine upsert omitting its entire optional descriptor likewise retains committed facets and old proof stale alongside new metadata. Explicit null/malformed descriptors fail.

For a shared-proof collision, save the unresolved diagnostics and null candidateHash; do not commit. In a new addressed batch send `op:"remove"` with `remove:{recordType:"evidence",externalKey:oldProofKey}` to remove the conflicting staged evidence, then upsert the subject/facet using a distinct current proof key and upsert that current proof. The server retains old proof needed by omitted facets and restores the union. Repreview at the accepted version; commit only ready. Alternatively explicitly reassert every facet that needs the proof. This repair is a protocol recipe, not a fabricated captured response.

For endpoint conflict, an FK edge cannot retarget omitted facets. Reassert all edge facets with valid current pairs/proof, or use distinct stable edge keys for differing physical targets and explicitly retain/delete the old one under the deletion gates. No name-based retarget or inferred cascade.

### Explicit rename before target allocation

The captured v2 map batch changed `column:orders:status` to `column:orders:state`, preserving `01a0f4e1-786c-78e1-8704-d1553270b80c`. It ran before the target upsert. Complete actual mapping command and response:

```json
{
  "identity": {
    "recordType": "node",
    "fromExternalKey": "column:orders:status",
    "toExternalKey": "column:orders:state",
    "expectedId": "01a0f4e1-786c-78e1-8704-d1553270b80c",
    "reason": "Explicit source rename",
    "evidenceKeys": [
      "proof:v2:column:orders:state:sql"
    ]
  },
  "op": "map_identity"
}
```

```json
{
  "batchId": "mapping",
  "payloadHash": "0e64a3c320286ed0ad4634493a11103fac89c01668e74b4ed25bff761378ba45",
  "acceptedVersion": 2,
  "identities": [
    {
      "recordType": "node",
      "externalKey": "column:orders:state",
      "id": "01a0f4e1-786c-78e1-8704-d1553270b80c"
    }
  ]
}
```

V2 committed revision `01a0f4e1-80e0-7f78-8d1b-b96d88fca034`. Its later read at the original v1 pin still returned the original semanticHash and four-table page. The complete v2 bundle proves legacy_note/index deletion, repairs the view/index dependencies, and pins retained migration CREATE targets to their old immutable revision. source_only create→drop creates no active UUID. Never delete from a partial inventory or unavailable prior proof.

### Inspection-only design request

For typed schema designs use the separately negotiated database7 procedure.
Source import7 examples above keep their original protocol and CAS.

## Actual SDK proposal examples: PostgreSQL and SQLite

These are actual responses from the deterministic SDK fixture import, create,
preview, apply, pinned reads and restart receipt replay. The SQL/Go fixture files
were read as bytes and never executed. IDs below belong to those example runs;
use IDs returned by your own baseline reads. Both examples leave source records
and project/source pointers unchanged. Every data/writer/migration check remains
unverified. Root, either leaf alone, bundle and server-only load the same pinned
procedures with the actual topic owners.

### postgresql: nullable legacy_note to desired NOT NULL

`create_backend_proposal` input after choosing the exact source baseline:

```json
{
  "idempotencyKey": "proposal-create",
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "name": "Require users",
  "baseRevisionId": "01a0f7ed-a479-78a3-b4c2-e0318bea27bc",
  "repositoryId": "01a0f7ed-5c70-7f06-9514-f0bbeeaf8056",
  "datastoreId": "01a0f7ed-9725-73eb-8e9b-ad26a5547bf5",
  "facetKey": "sql"
}
```

`preview_backend_proposal_commands` input (no mutation yet):

```json
{
  "expectedVersion": 1,
  "draftRevisionId": "01a0f7ed-ad02-7343-9470-bc1b7c6fa149",
  "commands": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "reason": "Require legacy notes after backfill",
      "columnId": "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf",
      "nullable": false
    }
  ],
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da"
}
```

Actual preview response; its hash is bound to this exact draft and command:

```json
{
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
  "baseRevisionId": "01a0f7ed-a479-78a3-b4c2-e0318bea27bc",
  "baseSemanticHash": "7203cee5fbfd06cd79d572228fb40a4133be452fd8c323283ac781377fba7194",
  "draftRevisionId": "01a0f7ed-ad02-7343-9470-bc1b7c6fa149",
  "draftHash": "c9a24d4f029fb4673243b77c49681288ab0adc9771c1c6155b402c410b5327bd",
  "expectedVersion": 1,
  "candidateHash": "9c9f0ee16aea27c153e9a67388f31d5aa4729fced64baa5ab4386e455e5d8a59",
  "candidateGraphHash": "ef6fad3daace09ccc6c334a9ca817e52e0fa5f147165d3ef23a6bc6c0bb66233",
  "changes": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "subjectId": "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf",
      "before": {
        "nullable": {
          "status": "known",
          "value": true
        }
      },
      "after": {
        "nullable": {
          "status": "known",
          "value": false
        }
      },
      "generatedIds": {}
    }
  ],
  "criteria": [
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:existing_data",
      "kind": "existing_data",
      "targetIds": [
        "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
      ],
      "description": "Check existing nulls and backfill before enforcing NOT NULL",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    },
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:writers",
      "kind": "writers",
      "targetIds": [
        "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
      ],
      "description": "Identify all writers and verify omission/null handling; writer coverage is unknown",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    },
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:migration_plan",
      "kind": "migration_plan",
      "targetIds": [
        "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
      ],
      "description": "Review migration, rollout and rollback against the pinned baseline",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    }
  ],
  "diagnostics": [],
  "limitations": [
    "Existing data, writer inventory and migration feasibility are unverified; runtime enforcement and impact analysis are unavailable"
  ]
}
```

`apply_backend_proposal_commands` input using that preview and its own retained key:

```json
{
  "commands": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "reason": "Require legacy notes after backfill",
      "columnId": "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf",
      "nullable": false
    }
  ],
  "idempotencyKey": "save-require-user",
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
  "expectedVersion": 1,
  "draftRevisionId": "01a0f7ed-ad02-7343-9470-bc1b7c6fa149",
  "candidateHash": "9c9f0ee16aea27c153e9a67388f31d5aa4729fced64baa5ab4386e455e5d8a59"
}
```

Read the acknowledged revision with `get_backend_node`:

```json
{
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposal": {
    "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
    "proposalRevisionId": "01a0f7ed-bb74-7f6c-b27d-b39e67327548"
  },
  "nodeId": "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
}
```

Exact value at `proposalProjection.sourceRecord.attributes.facets.sql.nullable`:

```json
{
  "status": "known",
  "value": true
}
```

Exact value at `proposalProjection.effectiveFacet.values.nullable`:

```json
{
  "status": "known",
  "value": false
}
```

Exact value at `proposalProjection.effectiveFacet.propertyOrigins["/nullable"]`:

```json
{
  "kind": "intent",
  "commandId": "require-user",
  "reason": "Require legacy notes after backfill",
  "evidenceIds": []
}
```

### postgresql: complete ordered FK and rollout criterion

The next preview uses the acknowledged proposal version/draft. It creates a
separate desired membership FK with ordered tenant/user pairs:

```json
{
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
  "expectedVersion": 2,
  "draftRevisionId": "01a0f7ed-bb74-7f6c-b27d-b39e67327548",
  "commands": [
    {
      "type": "alter_constraint",
      "commandId": "membership",
      "reason": "Retain user membership",
      "action": "create",
      "name": "designed_membership_fk",
      "tableId": "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "targetTableId": "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1",
      "columnPairs": [
        {
          "fromColumnId": "01a0f7ed-973c-707e-b482-a7557003b1e2",
          "toColumnId": "01a0f7ed-972c-7533-800b-06b4ee402966"
        },
        {
          "fromColumnId": "01a0f7ed-9742-786a-970b-9ec021e04bb7",
          "toColumnId": "01a0f7ed-972f-7581-8d4e-41f29c0f2277"
        }
      ],
      "updateAction": "no_action",
      "deleteAction": "restrict",
      "matchType": "simple",
      "deferrable": false,
      "initiallyDeferred": false
    }
  ]
}
```

Actual preview change, including stable designed constraint/reference IDs:

```json
{
  "type": "alter_constraint",
  "commandId": "membership",
  "subjectId": "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
  "before": {},
  "after": {
    "columnPairs": [
      {
        "fromColumnId": "01a0f7ed-973c-707e-b482-a7557003b1e2",
        "toColumnId": "01a0f7ed-972c-7533-800b-06b4ee402966"
      },
      {
        "fromColumnId": "01a0f7ed-9742-786a-970b-9ec021e04bb7",
        "toColumnId": "01a0f7ed-972f-7581-8d4e-41f29c0f2277"
      }
    ],
    "deleteAction": {
      "status": "known",
      "value": "restrict"
    },
    "matchType": {
      "status": "known",
      "value": "simple"
    },
    "updateAction": {
      "status": "known",
      "value": "no_action"
    }
  },
  "generatedIds": {
    "constraintId": "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
    "edgeId": "66b018f0-ed90-5c84-8387-0178158ea413"
  }
}
```

The pinned ER read after apply returns status:proposed and
runtimeStatus:unverified. Bounds retain inherited uniqueness and any limitations.
Expression, conditional, stale or incomplete uniqueness cannot establish a global
key. Proposed FK intent does not prove orphan-free data or action safety.

The following authored migration-plan criterion supplements required checks:

```json
{
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
  "expectedVersion": 3,
  "draftRevisionId": "01a0f7ed-da03-7753-a3f8-ad7124c384e3",
  "commands": [
    {
      "commandId": "rollout",
      "reason": "Capture rollout planning",
      "criteria": [
        {
          "key": "rollback",
          "kind": "migration_plan",
          "targetIds": [
            "01a0f7ed-9738-7e5e-bced-821f9e32b74a"
          ],
          "description": "Review rollout and rollback before implementation"
        }
      ],
      "type": "set_criteria"
    }
  ]
}
```

Actual saved criteria are all unverified:

```json
[
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:existing_data",
    "kind": "existing_data",
    "targetIds": [
      "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
    ],
    "description": "Check existing nulls and backfill before enforcing NOT NULL",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:writers",
    "kind": "writers",
    "targetIds": [
      "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
    ],
    "description": "Identify all writers and verify omission/null handling; writer coverage is unknown",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:migration_plan",
    "kind": "migration_plan",
    "targetIds": [
      "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
    ],
    "description": "Review migration, rollout and rollback against the pinned baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:referential_integrity",
    "kind": "referential_integrity",
    "targetIds": [
      "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1"
    ],
    "description": "Check existing orphan rows and ordered pair values before enforcing the FK",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:target_uniqueness",
    "kind": "target_uniqueness",
    "targetIds": [
      "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1"
    ],
    "description": "Verify actual target uniqueness and FK enforcement against the selected baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:writers",
    "kind": "writers",
    "targetIds": [
      "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1"
    ],
    "description": "Identify every source/target writer and verify update/delete actions; writer coverage is unknown",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:migration_plan",
    "kind": "migration_plan",
    "targetIds": [
      "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1"
    ],
    "description": "Review FK migration, rollout and rollback against the pinned baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "rollback",
    "kind": "migration_plan",
    "targetIds": [
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a"
    ],
    "description": "Review rollout and rollback before implementation",
    "origin": "authored",
    "status": "unverified",
    "commandId": "rollout"
  }
]
```

After those later saves and a database restart, replaying the original
NOT NULL apply request/key returned byte-for-byte the original receipt.
Creation replay also returned its original acknowledgement. Reading the old
proposal revision still returned its immutable original draft; current CAS
version was 4. A later source head is reported as baseOutdated, without rebasing
the proposal. Legacy relational ready/rebase remain unsupported. Authorized static diff/impact now uses change2/backend-analysis-jobs with the exact `proposal:{proposalId,proposalRevisionId}` target; measured runtime checks remain unavailable. The captured JSON above records the historical response unchanged.

### sqlite: nullable legacy_note to desired NOT NULL

`create_backend_proposal` input after choosing the exact source baseline:

```json
{
  "idempotencyKey": "proposal-create",
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17",
  "name": "Require users",
  "baseRevisionId": "01a0f7ee-d8f9-743d-b97a-a73461e7c9b3",
  "repositoryId": "01a0f7ee-9003-7697-90f6-bfa0f849ca79",
  "datastoreId": "01a0f7ee-cb9f-7e9b-87bb-75c730f459d4",
  "facetKey": "sql"
}
```

`preview_backend_proposal_commands` input (no mutation yet):

```json
{
  "expectedVersion": 1,
  "draftRevisionId": "01a0f7ee-e173-71ba-a1f6-07ea0d5c353e",
  "commands": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "reason": "Require legacy notes after backfill",
      "columnId": "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b",
      "nullable": false
    }
  ],
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17",
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9"
}
```

Actual preview response; its hash is bound to this exact draft and command:

```json
{
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
  "baseRevisionId": "01a0f7ee-d8f9-743d-b97a-a73461e7c9b3",
  "baseSemanticHash": "3d08a0a78244e72d3c50cb0bb4c3644f3359e1b10a4cc6e403a56eadb7bdbfc3",
  "draftRevisionId": "01a0f7ee-e173-71ba-a1f6-07ea0d5c353e",
  "draftHash": "7bdaa877a41c6783236b8e015282d1a63566fdfb5ea69fd1741727f9f8f77405",
  "expectedVersion": 1,
  "candidateHash": "73e95b6fe3ba818d175bd330c03d89b6f737bcf74d9b6a43f79db0b754aa5cf8",
  "candidateGraphHash": "e728151dd3a79bff4b7e3ee41ca20426445b53c49f0149a3d049a2ad86d2eb32",
  "changes": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "subjectId": "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b",
      "before": {
        "nullable": {
          "status": "known",
          "value": true
        }
      },
      "after": {
        "nullable": {
          "status": "known",
          "value": false
        }
      },
      "generatedIds": {}
    }
  ],
  "criteria": [
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:existing_data",
      "kind": "existing_data",
      "targetIds": [
        "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
      ],
      "description": "Check existing nulls and backfill before enforcing NOT NULL",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    },
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:writers",
      "kind": "writers",
      "targetIds": [
        "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
      ],
      "description": "Identify all writers and verify omission/null handling; writer coverage is unknown",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    },
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:migration_plan",
      "kind": "migration_plan",
      "targetIds": [
        "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
      ],
      "description": "Review migration, rollout and rollback against the pinned baseline",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    }
  ],
  "diagnostics": [],
  "limitations": [
    "Existing data, writer inventory and migration feasibility are unverified; runtime enforcement and impact analysis are unavailable"
  ]
}
```

`apply_backend_proposal_commands` input using that preview and its own retained key:

```json
{
  "draftRevisionId": "01a0f7ee-e173-71ba-a1f6-07ea0d5c353e",
  "candidateHash": "73e95b6fe3ba818d175bd330c03d89b6f737bcf74d9b6a43f79db0b754aa5cf8",
  "commands": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "reason": "Require legacy notes after backfill",
      "columnId": "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b",
      "nullable": false
    }
  ],
  "idempotencyKey": "save-require-user",
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17",
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
  "expectedVersion": 1
}
```

Read the acknowledged revision with `get_backend_node`:

```json
{
  "proposal": {
    "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
    "proposalRevisionId": "01a0f7ee-f020-7a8f-b8af-d9ed84d86a62"
  },
  "nodeId": "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b",
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17"
}
```

Exact value at `proposalProjection.sourceRecord.attributes.facets.sql.nullable`:

```json
{
  "status": "known",
  "value": true
}
```

Exact value at `proposalProjection.effectiveFacet.values.nullable`:

```json
{
  "status": "known",
  "value": false
}
```

Exact value at `proposalProjection.effectiveFacet.propertyOrigins["/nullable"]`:

```json
{
  "kind": "intent",
  "commandId": "require-user",
  "reason": "Require legacy notes after backfill",
  "evidenceIds": []
}
```

### sqlite: complete ordered FK and rollout criterion

The next preview uses the acknowledged proposal version/draft. It creates a
separate desired membership FK with ordered tenant/user pairs:

```json
{
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
  "expectedVersion": 2,
  "draftRevisionId": "01a0f7ee-f020-7a8f-b8af-d9ed84d86a62",
  "commands": [
    {
      "type": "alter_constraint",
      "commandId": "membership",
      "reason": "Retain user membership",
      "action": "create",
      "name": "designed_membership_fk",
      "tableId": "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "targetTableId": "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4",
      "columnPairs": [
        {
          "fromColumnId": "01a0f7ee-cbb6-703d-8ba4-51dda0eb6cf4",
          "toColumnId": "01a0f7ee-cba6-7cc8-a8eb-87ff6155732e"
        },
        {
          "fromColumnId": "01a0f7ee-cbbc-7191-9aa5-ab8bb009a720",
          "toColumnId": "01a0f7ee-cba9-7d91-8502-ba686f6043e7"
        }
      ],
      "updateAction": "no_action",
      "deleteAction": "restrict",
      "matchType": "simple",
      "deferrable": false,
      "initiallyDeferred": false
    }
  ],
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17"
}
```

Actual preview change, including stable designed constraint/reference IDs:

```json
{
  "type": "alter_constraint",
  "commandId": "membership",
  "subjectId": "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
  "before": {},
  "after": {
    "columnPairs": [
      {
        "fromColumnId": "01a0f7ee-cbb6-703d-8ba4-51dda0eb6cf4",
        "toColumnId": "01a0f7ee-cba6-7cc8-a8eb-87ff6155732e"
      },
      {
        "fromColumnId": "01a0f7ee-cbbc-7191-9aa5-ab8bb009a720",
        "toColumnId": "01a0f7ee-cba9-7d91-8502-ba686f6043e7"
      }
    ],
    "deleteAction": {
      "status": "known",
      "value": "restrict"
    },
    "matchType": {
      "status": "known",
      "value": "simple"
    },
    "updateAction": {
      "status": "known",
      "value": "no_action"
    }
  },
  "generatedIds": {
    "constraintId": "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
    "edgeId": "c9fb4dab-8b05-5fb4-b1de-b6bc228e0687"
  }
}
```

The pinned ER read after apply returns status:proposed and
runtimeStatus:unverified. Bounds retain inherited uniqueness and any limitations.
Expression, conditional, stale or incomplete uniqueness cannot establish a global
key. Proposed FK intent does not prove orphan-free data or action safety.

The following authored migration-plan criterion supplements required checks:

```json
{
  "commands": [
    {
      "type": "set_criteria",
      "commandId": "rollout",
      "reason": "Capture rollout planning",
      "criteria": [
        {
          "key": "rollback",
          "kind": "migration_plan",
          "targetIds": [
            "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d"
          ],
          "description": "Review rollout and rollback before implementation"
        }
      ]
    }
  ],
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17",
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
  "expectedVersion": 3,
  "draftRevisionId": "01a0f7ef-0f67-78d9-881b-ec2e008aceec"
}
```

Actual saved criteria are all unverified:

```json
[
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:existing_data",
    "kind": "existing_data",
    "targetIds": [
      "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
    ],
    "description": "Check existing nulls and backfill before enforcing NOT NULL",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:writers",
    "kind": "writers",
    "targetIds": [
      "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
    ],
    "description": "Identify all writers and verify omission/null handling; writer coverage is unknown",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:migration_plan",
    "kind": "migration_plan",
    "targetIds": [
      "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
    ],
    "description": "Review migration, rollout and rollback against the pinned baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:referential_integrity",
    "kind": "referential_integrity",
    "targetIds": [
      "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4"
    ],
    "description": "Check existing orphan rows and ordered pair values before enforcing the FK",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:target_uniqueness",
    "kind": "target_uniqueness",
    "targetIds": [
      "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4"
    ],
    "description": "Verify actual target uniqueness and FK enforcement against the selected baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:writers",
    "kind": "writers",
    "targetIds": [
      "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4"
    ],
    "description": "Identify every source/target writer and verify update/delete actions; writer coverage is unknown",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:migration_plan",
    "kind": "migration_plan",
    "targetIds": [
      "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4"
    ],
    "description": "Review FK migration, rollout and rollback against the pinned baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "rollback",
    "kind": "migration_plan",
    "targetIds": [
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d"
    ],
    "description": "Review rollout and rollback before implementation",
    "origin": "authored",
    "status": "unverified",
    "commandId": "rollout"
  }
]
```

After those later saves and a database restart, replaying the original
NOT NULL apply request/key returned byte-for-byte the original receipt.
Creation replay also returned its original acknowledgement. Reading the old
proposal revision still returned its immutable original draft; current CAS
version was 4. A later source head is reported as baseOutdated, without rebasing
the proposal. Legacy relational ready/rebase remain unsupported. Authorized static diff/impact now uses change2/backend-analysis-jobs with the exact `proposal:{proposalId,proposalRevisionId}` target; measured runtime checks remain unavailable. The captured JSON above records the historical response unchanged.

## Public MCP source4 import → lineage query fixture

Select import7 and inspect7 with complete requirements in one verified guide
set; load backend-model, backend-import-protocol, backend-recovery and the
inspect7 flow/analysis references. This example is exercised by
`internal/mcp/tools_backend_lineage_example_test.go`,
`TestBackendLineageRealSDKGuideFixtureExample` through actual SDK tool calls.
The independent bundle is `internal/backendmodel/testdata/lineage/orders/`:
source.go.txt, commands.json and expected.json. Read source bytes as inert data;
never run source, SQL or migrations. expected.json is an oracle, not import data.
Use an isolated fixture project and retain every full write input/key/receipt.

With the earlier sha/sorted helpers and configured tool connection, Node.js
can load this fixture using the following ordered procedure. Begin input is
complete; actual IDs/hashes/versions always come from responses.

```javascript
const fs = require('node:fs');
const root = 'internal/backendmodel/testdata/lineage/orders/';
let lineageProject = await tool('create_backend_project',
  {name:'Lineage fixture',idempotencyKey:'lineage-example-create'});
const sourceHash = sha(fs.readFileSync(root+'source.go.txt'));
const lineageInventory = ['files','endpoints','datastores','migrations',
  'producers','consumers','jobs','contracts','tests'].map(category => {
    const captured = ['files','endpoints','datastores'].includes(category);
    return {category,status:captured?'complete':'unsupported',knownCount:captured?1:0,
      denominator:captured?1:null,discoverySource:'source fixture',gaps:[],
      reason:captured?'':'Outside captured fixture'};
  });
const lineageBegin = {projectId:lineageProject.id,expectedVersion:lineageProject.version,
  baseRevisionId:lineageProject.currentRevisionId,idempotencyKey:'lineage-example-begin',
  mode:'initial',profile:'field-lineage-v1',inventory:lineageInventory,
  manifest:{repositoryName:'orders',provider:{name:'orders-fixture',version:'1',
    namespace:'lineage-fixture',method:'agent',profiles:['foundation-graph-v1',
      'relational-graph-v1','runtime-flow-v1','field-lineage-v1'],
    limitations:['Static bounded fixture']},snapshot:{dirty:false,consistency:'verified',
      capturedAt:'2026-10-01T12:00:00Z',files:[{path:'source.go.txt',contentHash:sourceHash,
        fileType:'go',analysisStatus:'analyzed'}]}}};
const lineageSession = await tool('begin_backend_import',lineageBegin);
const lineageCommands = JSON.parse(fs.readFileSync(root+'commands.json','utf8')
  .replaceAll('@repositoryId@',lineageSession.repositoryId)
  .replaceAll('@snapshotId@',lineageSession.snapshotId));
const lineageBatch = {projectId:lineageProject.id,importId:lineageSession.id,
  batchId:'lineage-example',expectedImportVersion:lineageSession.version,
  payloadHash:sha(JSON.stringify(sorted(lineageCommands))),commands:lineageCommands};
const lineageReceipt = await tool('put_backend_import_batch',lineageBatch);
const lineageIds = Object.fromEntries(lineageReceipt.identities
  .filter(x=>x.recordType==='node').map(x=>[x.externalKey,x.id]));
const lineagePreview = await tool('preview_backend_import',{projectId:lineageProject.id,
  importId:lineageSession.id,expectedImportVersion:lineageReceipt.acceptedVersion,
  baseRevisionId:lineageProject.currentRevisionId});
if (lineagePreview.state !== 'ready' || !lineagePreview.candidateHash)
  throw Error('Inspect and repair preview; no commit');
// Independently audit every source assertion/proof at this ready tuple first.
const lineageCommit = {projectId:lineageProject.id,importId:lineageSession.id,
  expectedVersion:lineageProject.version,expectedImportVersion:lineagePreview.version,
  candidateHash:lineagePreview.candidateHash,idempotencyKey:'lineage-example-commit'};
const lineageResult = await tool('commit_backend_import',lineageCommit);
const lineagePin = lineageResult.revision.id;
const requestSeed = {kind:'api_field',nodeId:lineageIds.request};
const amountSeed = {kind:'column',nodeId:lineageIds['column:orders:amount'],facetKey:'sql'};
const responseSeed = {kind:'api_field',nodeId:lineageIds.response};
async function lineagePages(seed,direction) {
  let cursor='', items=[];
  do {
    const input = {projectId:lineageProject.id,revisionId:lineagePin,seed,direction,
      maxDepth:8,limit:1,...(cursor?{cursor}:{})};
    const page = await tool('query_backend_lineage',input);
    // Verify project/revision/semanticHash/complete seed/direction before display.
    // Retain page.coverage, truncated, truncationReasons and limitations.
    items.push(...page.items); cursor=page.nextCursor;
  } while(cursor);
  return items;
}
const requestMappings = await lineagePages(requestSeed,'forward');
const amountMappings = await lineagePages(amountSeed,'forward');
const responseOrigins = await lineagePages(responseSeed,'reverse');
const unknownBoundary = await lineagePages(
  {kind:'api_field',nodeId:lineageIds.token},'reverse');
```

Expected request→column witness: m01-request,m02-parameter,m03-write;
amount column→response: m04-amount,m06-total; reverse tax witness:
m06-total,m05-tax (use receipt UUIDs). m06-total retains both ordered query
results amount then tax; forward arrival through amount does not reach tax as a
co-input. Reverse expands both. m10-unknown remains a boundary with its complete
destination/inputs visible. The constant is m08-constant; unknown with zero
inputs is still unknown, never relabeled constant. Static fixture includes cycle
and alternative mappings, not runtime execution or every possible path.

On uncertain commit replay lineageCommit unchanged and require original receipt;
never replace CAS/key with today's head. Evidence/owner/value inspector reads use
lineagePin and complete facet/collection/opaque portKey. Redacted shape/explanation
must omit sensitive constants and samples; source-local API field identity does
not resolve external API pins; their separate inspect7 contract is in backend-flow-reference. Old foundation/relational examples above keep
their source1/2 formats and ordinary workflow; source3 flow examples stay source3.
