import importlib.util
import json
import tempfile
import unittest
from io import BytesIO
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "backend_import_client", Path(__file__).with_name("backend_import_client.py")
)
client = importlib.util.module_from_spec(spec)
spec.loader.exec_module(client)


class FakeServer:
    def __init__(self):
        self.requests = []
        self.receipts = {}
        self.lost = None
        self.publications = 0
        self.preview = None
        self.installation = "installation"
        self.batch_commands = 2

    def call(self, name, arguments):
        raw = client.canonical(arguments)
        self.requests.append((name, raw))
        if name == "get_backend_capabilities":
            return {
                "installationId": self.installation,
                "guideSetId": "guide",
                "limits": {
                    "maxImportBatchCommands": self.batch_commands,
                    "maxImportBatchBytes": 4096,
                },
            }
        if name == "begin_backend_import":
            result = {
                "id": "import",
                "repositoryId": "repo",
                "snapshotId": "snapshot",
                "version": 1,
            }
        elif name == "put_backend_import_batch":
            result = {
                "batchId": arguments["batchId"],
                "payloadHash": arguments["payloadHash"],
                "acceptedVersion": arguments["expectedImportVersion"] + 1,
                "identities": [],
            }
        elif name == "preview_backend_import":
            result = {
                "sessionId": "import",
                "version": arguments["expectedImportVersion"] + 1,
                "state": "ready",
                "candidateHash": "c" * 64,
            }
            self.preview = result
        elif name == "get_backend_import":
            return {
                "preview": self.preview,
                "session": {
                    "version": self.preview["version"],
                    "baseRevisionId": "base",
                },
            }
        elif name == "commit_backend_import":
            if (name, raw) not in self.receipts:
                self.publications += 1
            result = {"sessionId": "import", "revision": {"id": "published"}}
        else:
            raise AssertionError(name)
        self.receipts[(name, raw)] = result
        if self.lost == name:
            self.lost = None
            raise client.UncertainOutcome("lost reply")
        return result


class TransferTests(unittest.TestCase):
    def test_uncertain_tail_cannot_be_corrected_and_original_can_resume(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            plan = self.fixture(root)
            server = FakeServer()
            server.lost = "put_backend_import_batch"
            original = client.Transfer(
                plan, root / "original", "https://mocker.test/mcp", server
            )
            with self.assertRaises(client.UncertainOutcome):
                original.stage()
            continuation = client.Transfer(
                plan, root / "continued", "https://mocker.test/mcp", server
            )
            with self.assertRaises(client.TransferError):
                continuation.continue_from(original.journal)
            self.assertEqual(original.stage()["state"], "ready")

    def test_transient_tool_error_keeps_original_request_retryable(self):
        class TransientServer(FakeServer):
            failed = False

            def call(self, name, arguments):
                if name == "put_backend_import_batch" and not self.failed:
                    self.failed = True
                    raise client.RejectedRequest(
                        {
                            "isError": True,
                            "content": [
                                {"type": "text", "text": "HTTP 503: unavailable"}
                            ],
                        }
                    )
                return super().call(name, arguments)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            transfer = client.Transfer(
                self.fixture(root),
                root / "journal",
                "https://mocker.test/mcp",
                TransientServer(),
            )
            with self.assertRaises(client.TransferError):
                transfer.stage()
            request = (transfer.journal / "000002.request.json").read_bytes()
            self.assertEqual(transfer.stage()["state"], "ready")
            self.assertEqual(
                request, (transfer.journal / "000002.request.json").read_bytes()
            )

    def test_duplicate_evidence_keys_stop_before_begin(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            plan = self.fixture(root)
            with (root / "commands.ndjson").open("ab") as stream:
                stream.write(
                    client.canonical(
                        {
                            "op": "upsert_edge",
                            "edge": {"evidenceKeys": ["proof", "proof"]},
                        }
                    )
                    + b"\n"
                )
            server = FakeServer()
            with self.assertRaises(client.TransferError):
                client.Transfer(
                    plan, root / "journal", "https://mocker.test/mcp", server
                ).stage()
            self.assertFalse(
                any(name == "begin_backend_import" for name, _ in server.requests)
            )

    def test_continuation_preserves_accepted_prefix_and_rejected_attempt(self):
        class RejectingServer(FakeServer):
            advanced = False

            def call(self, name, arguments):
                if name == "put_backend_import_batch" and any(
                    c.get("evidence", {}).get("externalKey") == "2"
                    for c in arguments["commands"]
                ):
                    raise client.RejectedRequest(
                        {
                            "isError": True,
                            "structuredContent": {
                                "status": 422,
                                "code": "validation_error",
                            },
                        }
                    )
                if name == "get_backend_import":
                    version = max(
                        (v.get("acceptedVersion", 1) for v in self.receipts.values()),
                        default=1,
                    )
                    return {
                        "session": {
                            "id": "import",
                            "projectId": "project",
                            "baseRevisionId": "base",
                            "version": version + int(self.advanced),
                            "state": "collecting",
                        }
                    }
                return super().call(name, arguments)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            plan = self.fixture(root)
            server = RejectingServer()
            parent = root / "original"
            with self.assertRaises(client.TransferError):
                client.Transfer(plan, parent, "https://mocker.test/mcp", server).stage()
            before = {p.name: p.read_bytes() for p in parent.iterdir() if p.is_file()}
            commands = [
                client.decode(line)
                for line in (root / "commands.ndjson").read_bytes().splitlines()
            ]
            commands[2]["evidence"]["externalKey"] = "corrected"
            (root / "commands.ndjson").write_bytes(
                b"\n".join(client.canonical(c) for c in commands) + b"\n"
            )
            corrected = (root / "commands.ndjson").read_bytes()
            commands[0]["evidence"]["snippet"] = "changed accepted command"
            (root / "commands.ndjson").write_bytes(
                b"\n".join(client.canonical(c) for c in commands) + b"\n"
            )
            with self.assertRaisesRegex(
                client.TransferError, "changed accepted commands"
            ):
                client.Transfer(
                    plan, root / "refused-prefix", "https://mocker.test/mcp", server
                ).continue_from(parent)
            (root / "commands.ndjson").write_bytes(corrected)
            server.advanced = True
            with self.assertRaisesRegex(client.TransferError, "server import advanced"):
                client.Transfer(
                    plan, root / "refused-cas", "https://mocker.test/mcp", server
                ).continue_from(parent)
            server.advanced = False
            transfer = client.Transfer(
                plan, root / "continued", "https://mocker.test/mcp", server
            )
            transfer.continue_from(parent)
            ready = transfer.stage()
            self.assertEqual(ready["state"], "ready")
            self.assertEqual(
                before,
                {p.name: p.read_bytes() for p in parent.iterdir() if p.is_file()},
            )
            for name in (
                "000001.request.json",
                "000001.receipt.json",
                "000002.request.json",
                "000002.receipt.json",
            ):
                self.assertEqual(
                    (parent / name).read_bytes(), (transfer.journal / name).read_bytes()
                )
            new_request = client.decode(
                (transfer.journal / "000003.request.json").read_bytes()
            )
            self.assertNotEqual(new_request["arguments"]["batchId"], "file-000002")
            self.assertEqual(new_request["arguments"]["expectedImportVersion"], 2)
            self.assertEqual(
                client.decode((transfer.journal / "verification.json").read_bytes())[
                    "acceptedCommands"
                ],
                5,
            )

    def fixture(self, root):
        plan = {
            "format": "mocker-import-transfer-v1",
            "projectId": "project",
            "installationId": "installation",
            "guideSetId": "guide",
            "begin": {
                "expectedVersion": 7,
                "baseRevisionId": "base",
                "idempotencyKey": "begin",
                "manifest": {"provider": {"name": "any-language"}},
            },
            "commandsFile": "commands.ndjson",
        }
        commands = [
            {
                "op": "upsert_evidence",
                "evidence": {
                    "externalKey": str(i),
                    "source": {
                        "repositoryId": {"$importBinding": "repositoryId"},
                        "snapshotId": {"$importBinding": "snapshotId"},
                    },
                    "snippet": "keep @snapshotId@ and ${source} verbatim",
                },
            }
            for i in range(5)
        ]
        (root / "plan.json").write_bytes(client.canonical(plan))
        (root / "commands.ndjson").write_bytes(
            b"\n".join(client.canonical(c) for c in commands) + b"\n"
        )
        return root / "plan.json"

    def test_hash_vectors_and_large_integer_precision(self):
        vectors = json.loads(
            (
                Path(__file__).parents[1]
                / "internal/backendmodel/testdata/import_batch_hash_vectors.json"
            ).read_text()
        )
        for vector in vectors:
            self.assertEqual(
                client.canonical(vector["commands"]).decode(), vector["canonical"]
            )
            self.assertEqual(
                client.digest(client.canonical(vector["commands"])), vector["sha256"]
            )
        raw = b'{"version":9007199254740993,"value":1.2300e+4}'
        self.assertEqual(
            client.canonical(client.decode(raw)),
            b'{"value":1.2300e+4,"version":9007199254740993}',
        )

    def test_resume_replays_exact_pending_batch_and_commit_once(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            plan = self.fixture(root)
            server = FakeServer()
            transfer = client.Transfer(
                plan, root / "journal", "https://mocker.test/mcp", server
            )
            server.lost = "put_backend_import_batch"
            with self.assertRaises(client.UncertainOutcome):
                transfer.stage()
            first = next(
                raw
                for name, raw in server.requests
                if name == "put_backend_import_batch"
            )
            transfer = client.Transfer(
                plan, root / "journal", "https://mocker.test/mcp", server
            )
            ready = transfer.stage()
            batches = [
                raw
                for name, raw in server.requests
                if name == "put_backend_import_batch"
            ]
            self.assertEqual(batches[0], batches[1])
            self.assertEqual(first, batches[1])
            self.assertEqual(len(batches), 4)
            bound = client.decode(batches[1])["commands"][0]["evidence"]
            self.assertEqual(
                bound["source"], {"repositoryId": "repo", "snapshotId": "snapshot"}
            )
            self.assertEqual(
                bound["snippet"], "keep @snapshotId@ and ${source} verbatim"
            )
            self.assertEqual(server.publications, 0)
            audit = {
                "result": "pass",
                "projectId": "project",
                "importId": "import",
                "candidateHash": ready["candidateHash"],
                "previewVersion": ready["version"],
                "baseRevisionId": "base",
                "expectedVersion": 7,
                "journalHash": transfer.audit_binding(),
                "findings": [
                    {
                        "subjectKey": "scope",
                        "finding": "Independent source checks recorded separately",
                    }
                ],
            }
            (root / "audit.json").write_bytes(client.canonical(audit))
            server.lost = "commit_backend_import"
            with self.assertRaises(client.UncertainOutcome):
                transfer.commit(root / "audit.json")
            result = client.Transfer(
                plan, root / "journal", "https://mocker.test/mcp", server
            ).commit(root / "audit.json")
            self.assertEqual(result["revision"]["id"], "published")
            self.assertEqual(server.publications, 1)
            commits = [
                raw for name, raw in server.requests if name == "commit_backend_import"
            ]
            self.assertEqual(commits[0], commits[1])

    def test_changed_inputs_and_changed_installation_are_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            plan = self.fixture(root)
            server = FakeServer()
            client.Transfer(
                plan, root / "journal", "https://mocker.test/mcp", server
            ).stage()
            (root / "commands.ndjson").write_text("{}\n")
            with self.assertRaises(client.TransferError):
                client.Transfer(
                    plan, root / "journal", "https://mocker.test/mcp", server
                ).stage()

    def test_streamable_http_reads_matching_sse_result(self):
        transport = object.__new__(client.HTTPMCP)
        transport.sequence = 7
        raw = b': keepalive\n\ndata: {"jsonrpc":"2.0","method":"notifications/message"}\n\ndata: {"jsonrpc":"2.0","id":7,"result":{"ok":true}}\n\n'
        self.assertEqual(
            transport.result(transport.sse_result(BytesIO(raw))), {"ok": True}
        )

    def test_lost_preview_is_recovered_without_advancing_cas(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            server = FakeServer()
            server.lost = "preview_backend_import"
            transfer = client.Transfer(
                self.fixture(root), root / "journal", "https://mocker.test/mcp", server
            )
            ready = transfer.stage()
            self.assertEqual(ready["state"], "ready")
            self.assertEqual(
                sum(name == "preview_backend_import" for name, _ in server.requests), 1
            )

    def test_different_installation_is_refused_before_writes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            server = FakeServer()
            server.installation = "different"
            transfer = client.Transfer(
                self.fixture(root), root / "journal", "https://mocker.test/mcp", server
            )
            with self.assertRaises(client.TransferError):
                transfer.stage()
            self.assertEqual(
                [name for name, _ in server.requests], ["get_backend_capabilities"]
            )

    def test_resume_keeps_saved_roster_when_server_limits_change(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            plan = self.fixture(root)
            server = FakeServer()
            transfer = client.Transfer(
                plan, root / "journal", "https://mocker.test/mcp", server
            )
            first = transfer.stage()
            before = [
                (p.name, p.read_bytes())
                for p in sorted((root / "journal").glob("*.request.json"))
            ]
            server.batch_commands = 1
            resumed = client.Transfer(
                plan, root / "journal", "https://mocker.test/mcp", server
            ).stage()
            self.assertEqual(first, resumed)
            self.assertEqual(
                before,
                [
                    (p.name, p.read_bytes())
                    for p in sorted((root / "journal").glob("*.request.json"))
                ],
            )


if __name__ == "__main__":
    unittest.main()
