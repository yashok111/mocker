#!/usr/bin/env python3
"""Language-neutral, file-backed Mocker import transfer (Python standard library).

This client transfers inspected NDJSON commands. It does not analyze source or
certify source fidelity. Stage and Commit are separate explicit operations.
"""

import argparse
import hashlib
import http.client
import json
import math
import os
import ssl
import sys
import tempfile
from pathlib import Path
from urllib.parse import urlsplit

MAX_RESPONSE = 64 << 20
MAX_COMMAND = 1 << 20
MAX_CAPTURE = 2 << 30


class TransferError(Exception):
    pass


class UncertainOutcome(TransferError):
    pass


class NumberToken(str):
    """Retain fractional/exponent tokens as received, without binary rounding."""


def decode(raw):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise TransferError("duplicate JSON object key")
            result[key] = value
        return result

    def invalid_number(_):
        raise TransferError("nonfinite JSON number")

    return json.loads(
        raw,
        parse_float=NumberToken,
        parse_constant=invalid_number,
        object_pairs_hook=unique,
    )


def canonical(value):
    def encode(item, depth=0):
        if depth > 128:
            raise TransferError("JSON nesting exceeds 128 levels")
        if isinstance(item, NumberToken):
            return str(item)
        if item is None:
            return "null"
        if isinstance(item, bool):
            return "true" if item else "false"
        if isinstance(item, int):
            return str(item)
        if isinstance(item, float):
            if not math.isfinite(item):
                raise TransferError("nonfinite number")
            return json.dumps(item, allow_nan=False)
        if isinstance(item, str):
            return json.dumps(item, ensure_ascii=False)
        if isinstance(item, list):
            return "[" + ",".join(encode(v, depth + 1) for v in item) + "]"
        if isinstance(item, dict) and all(isinstance(k, str) for k in item):
            return (
                "{"
                + ",".join(
                    encode(k) + ":" + encode(item[k], depth + 1) for k in sorted(item)
                )
                + "}"
            )
        raise TransferError("unsupported JSON value")

    return encode(value).encode("utf-8")


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def file_digest(path):
    h = hashlib.sha256()
    total = 0
    with path.open("rb") as stream:
        while chunk := stream.read(1 << 20):
            total += len(chunk)
            if total > MAX_CAPTURE:
                raise TransferError("command artifact exceeds 2 GiB")
            h.update(chunk)
    return h.hexdigest()


def save_once(path, raw):
    """Fsync before atomic publication; never replace an acknowledged artifact."""
    if path.is_symlink():
        raise TransferError("journal symlinks are not allowed")
    if path.exists():
        if path.read_bytes() != raw:
            raise TransferError("saved journal artifact differs; refusing overwrite")
        return
    descriptor, temporary = tempfile.mkstemp(prefix=".pending-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        try:
            os.link(temporary, path)
        except FileExistsError:
            if path.is_symlink() or path.read_bytes() != raw:
                raise TransferError("concurrent journal artifact differs") from None
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        os.unlink(temporary)


def capture_file(source, target, expected_hash):
    if target.is_symlink():
        raise TransferError("journal symlinks are not allowed")
    if target.exists():
        if file_digest(target) != expected_hash:
            raise TransferError("captured command bytes changed")
        return
    descriptor, temporary = tempfile.mkstemp(prefix=".capture-", dir=target.parent)
    try:
        with os.fdopen(descriptor, "wb") as output, source.open("rb") as stream:
            h, total = hashlib.sha256(), 0
            while chunk := stream.read(1 << 20):
                total += len(chunk)
                if total > MAX_CAPTURE:
                    raise TransferError("command artifact exceeds 2 GiB")
                output.write(chunk)
                h.update(chunk)
            if h.hexdigest() != expected_hash:
                raise TransferError("command artifact changed during capture")
            output.flush()
            os.fsync(output.fileno())
        try:
            os.link(temporary, target)
        except FileExistsError:
            if target.is_symlink() or file_digest(target) != expected_hash:
                raise TransferError("concurrent command capture differs") from None
        descriptor = os.open(target.parent, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    finally:
        os.unlink(temporary)


def bind(value, session):
    if isinstance(value, dict):
        if set(value) == {"$importBinding"}:
            key = value["$importBinding"]
            if key not in ("repositoryId", "snapshotId"):
                raise TransferError("unknown import binding")
            return session[key]
        return {key: bind(item, session) for key, item in value.items()}
    if isinstance(value, list):
        return [bind(item, session) for item in value]
    return value


class Transfer:
    def __init__(self, plan_path, journal, endpoint, caller, progress=None):
        self.plan_path = Path(plan_path)
        self.plan = decode(self.plan_path.read_bytes())
        if self.plan.get("format") != "mocker-import-transfer-v1":
            raise TransferError("unsupported transfer plan")
        for key in (
            "projectId",
            "installationId",
            "guideSetId",
            "begin",
            "commandsFile",
        ):
            if not self.plan.get(key):
                raise TransferError("incomplete transfer plan")
        self.commands = (self.plan_path.parent / self.plan["commandsFile"]).resolve()
        self.journal = Path(journal)
        if self.journal.is_symlink():
            raise TransferError("journal must not be a symlink")
        self.journal.mkdir(mode=0o700, parents=True, exist_ok=True)
        if self.journal.stat().st_mode & 0o077:
            raise TransferError("journal must be private (permissions 0700)")
        self.endpoint, self.caller = endpoint, caller
        self.progress = progress or (lambda _: None)
        self.sequence = 0

    def negotiate(self):
        capabilities = self.caller.call("get_backend_capabilities", {})
        for key in ("installationId", "guideSetId"):
            if capabilities.get(key) != self.plan[key]:
                raise TransferError(
                    "installation or guide set changed; review compatibility before resuming"
                )
        binding = {
            "endpoint": self.endpoint,
            "planHash": digest(canonical(self.plan)),
            "commandsHash": file_digest(self.commands),
        }
        save_once(self.journal / "input.json", canonical(binding))
        self.capture = self.journal / "commands.ndjson"
        capture_file(self.commands, self.capture, binding["commandsHash"])
        self.input_binding = binding
        limits = capabilities["limits"]
        self.max_commands = min(500, int(limits["maxImportBatchCommands"]))
        self.max_bytes = min(MAX_COMMAND, int(limits["maxImportBatchBytes"]))
        self.max_body = int(limits.get("maxBodyBytes", self.max_bytes + 1024))
        self.max_bytes = min(self.max_bytes, self.max_body - 1024)
        if self.max_commands < 1 or self.max_bytes < 1024:
            raise TransferError("advertised batch budget is unusable")
        return capabilities

    def request(self, name, arguments):
        self.sequence += 1
        stem = f"{self.sequence:06d}"
        raw = canonical({"name": name, "arguments": arguments})
        request_path = self.journal / (stem + ".request.json")
        pending = request_path.exists()
        save_once(request_path, raw)
        response_path = self.journal / (stem + ".receipt.json")
        if response_path.is_symlink():
            raise TransferError("journal symlinks are not allowed")
        if response_path.exists():
            saved = decode(response_path.read_bytes())
            if saved["requestHash"] != digest(raw) or saved["resultHash"] != digest(
                canonical(saved["result"])
            ):
                raise TransferError("saved receipt binding is invalid")
            return saved["result"]
        envelope = {
            "jsonrpc": "2.0",
            "id": 9223372036854775807,
            "method": "tools/call",
            "params": {"name": name, "arguments": arguments},
        }
        if len(canonical(envelope)) > self.max_body:
            raise TransferError("saved request exceeds the server HTTP body limit")
        result = (
            self.recover_preview(arguments)
            if pending and name == "preview_backend_import"
            else None
        )
        try:
            if result is None:
                result = self.caller.call(name, arguments)
        except UncertainOutcome:
            if name != "preview_backend_import":
                raise
            result = self.recover_preview(arguments)
            if result is None:
                raise UncertainOutcome(
                    "preview outcome unresolved; preserve the journal"
                ) from None
        receipt = {
            "requestHash": digest(raw),
            "resultHash": digest(canonical(result)),
            "result": result,
        }
        save_once(response_path, canonical(receipt))
        return result

    def recover_preview(self, arguments):
        status = self.caller.call(
            "get_backend_import",
            {"projectId": arguments["projectId"], "importId": arguments["importId"]},
        )
        result, session = status.get("preview"), status.get("session", {})
        if (
            result
            and result.get("sessionId") == arguments["importId"]
            and result.get("version") == arguments["expectedImportVersion"] + 1
            and session.get("version") == result["version"]
            and session.get("baseRevisionId") == arguments["baseRevisionId"]
        ):
            return result
        return None

    def batch_arguments(self, session, version, index, commands):
        return {
            "projectId": self.plan["projectId"],
            "importId": session["id"],
            "batchId": f"file-{index:06d}",
            "expectedImportVersion": version,
            "payloadHash": digest(canonical(commands)),
            "commands": commands,
        }

    def stage(self):
        self.sequence = 0
        self.negotiate()
        begin = dict(self.plan["begin"], projectId=self.plan["projectId"])
        session = self.request("begin_backend_import", begin)
        version, index, batch = session["version"], 1, []

        def send(commands):
            nonlocal version, index
            arguments = self.batch_arguments(session, version, index, commands)
            result = self.request("put_backend_import_batch", arguments)
            if (
                result.get("batchId") != arguments["batchId"]
                or result.get("payloadHash") != arguments["payloadHash"]
                or result.get("acceptedVersion") != version + 1
            ):
                raise TransferError(
                    "batch receipt differs from the exact submitted batch"
                )
            version = result["acceptedVersion"]
            self.progress(
                {
                    "stage": "batch",
                    "index": index,
                    "commands": len(commands),
                    "acceptedVersion": version,
                }
            )
            index += 1

        with self.capture.open("rb") as stream:
            iterator = self.read_commands(stream, session)
            # Saved boundaries are part of replay identity. Reproduce the journal
            # roster first; changed advertised limits apply only to the new tail.
            while True:
                saved_path = self.journal / f"{self.sequence + 1:06d}.request.json"
                if not saved_path.exists():
                    break
                if saved_path.is_symlink():
                    raise TransferError("journal symlinks are not allowed")
                saved = decode(saved_path.read_bytes())
                if saved["name"] != "put_backend_import_batch":
                    break
                expected = saved["arguments"]["commands"]
                consumed = []
                for _ in expected:
                    try:
                        consumed.append(next(iterator))
                    except StopIteration:
                        raise TransferError(
                            "saved batch exceeds captured input"
                        ) from None
                arguments = self.batch_arguments(session, version, index, consumed)
                if canonical(arguments) != canonical(saved["arguments"]):
                    raise TransferError(
                        "saved batch differs from captured commands or original CAS"
                    )
                send(consumed)
            for command in iterator:
                # Identity decisions must precede allocating upserts even when
                # both fit the same transport batch.
                identity = command.get("op") == "map_identity"
                if batch and (identity or batch[0].get("op") == "map_identity"):
                    send(batch)
                    batch = []
                candidate = batch + [command]
                size = len(
                    canonical(self.batch_arguments(session, version, index, candidate))
                )
                if batch and (
                    len(candidate) > self.max_commands or size > self.max_bytes
                ):
                    send(batch)
                    batch = []
                if (
                    len(
                        canonical(
                            self.batch_arguments(session, version, index, [command])
                        )
                    )
                    > self.max_bytes
                ):
                    raise TransferError(
                        "one command exceeds the advertised batch byte limit"
                    )
                batch.append(command)
        if batch:
            send(batch)
        if file_digest(self.capture) != self.input_binding["commandsHash"]:
            raise TransferError("command file changed during transfer; do not commit")
        ready = self.request(
            "preview_backend_import",
            {
                "projectId": self.plan["projectId"],
                "importId": session["id"],
                "expectedImportVersion": version,
                "baseRevisionId": begin["baseRevisionId"],
            },
        )
        if (
            ready.get("sessionId") != session["id"]
            or ready.get("version") != version + 1
        ):
            raise TransferError("preview does not bind the submitted session/version")
        save_once(self.journal / "ready.json", canonical(ready))
        self.progress(
            {"stage": "preview", "state": ready["state"], "version": ready["version"]}
        )
        return ready

    @staticmethod
    def read_commands(stream, session):
        while line := stream.readline(MAX_COMMAND + 1):
            if len(line) > MAX_COMMAND:
                raise TransferError("NDJSON command exceeds the batch budget")
            if not line.strip():
                continue
            command = decode(line)
            if not isinstance(command, dict):
                raise TransferError("each NDJSON command must be an object")
            yield bind(command, session)

    def audit_binding(self):
        files = sorted(self.journal.glob("*.request.json")) + sorted(
            self.journal.glob("*.receipt.json")
        )
        # Commit is outside the pre-commit audit tuple, including after a lost
        # Commit response. Otherwise its own pending file would invalidate replay.
        roster = []
        for path in sorted(files):
            request = self.journal / path.name.replace(".receipt.json", ".request.json")
            if decode(request.read_bytes())["name"] == "commit_backend_import":
                continue
            roster.append([path.name, digest(path.read_bytes())])
        return digest(
            canonical(
                {
                    "input": decode((self.journal / "input.json").read_bytes()),
                    "roster": roster,
                }
            )
        )

    def commit(self, audit_path):
        ready = self.stage()
        if ready.get("state") != "ready" or not ready.get("candidateHash"):
            raise TransferError(
                "candidate is not ready; repair requires a new inspected plan"
            )
        audit = decode(Path(audit_path).read_bytes())
        begin = self.plan["begin"]
        expected = {
            "result": "pass",
            "projectId": self.plan["projectId"],
            "importId": ready["sessionId"],
            "candidateHash": ready["candidateHash"],
            "previewVersion": ready["version"],
            "baseRevisionId": begin["baseRevisionId"],
            "expectedVersion": begin["expectedVersion"],
            "journalHash": self.audit_binding(),
        }
        if any(
            audit.get(key) != value for key, value in expected.items()
        ) or not audit.get("findings"):
            raise TransferError(
                "an independent audit with exact candidate/journal bindings and findings is required"
            )
        save_once(self.journal / "audit.json", canonical(audit))
        return self.request(
            "commit_backend_import",
            {
                "projectId": self.plan["projectId"],
                "importId": ready["sessionId"],
                "expectedVersion": begin["expectedVersion"],
                "expectedImportVersion": ready["version"],
                "candidateHash": ready["candidateHash"],
                "idempotencyKey": "file-commit-" + expected["journalHash"],
            },
        )


class HTTPMCP:
    def __init__(self, endpoint, token, ca_file=None, timeout=180):
        self.url = urlsplit(endpoint)
        if (
            self.url.username
            or self.url.password
            or self.url.fragment
            or self.url.scheme not in ("http", "https")
        ):
            raise TransferError("use an HTTP(S) MCP endpoint without URL credentials")
        if self.url.scheme == "http" and self.url.hostname not in (
            "localhost",
            "127.0.0.1",
            "::1",
        ):
            raise TransferError("plain HTTP is restricted to localhost")
        if not token:
            raise TransferError("MCP token environment variable is empty")
        self.token, self.sequence, self.protocol, self.session = (
            token,
            0,
            "2025-03-26",
            None,
        )
        if self.url.scheme == "https":
            self.connection = http.client.HTTPSConnection(
                self.url.hostname,
                self.url.port,
                timeout=timeout,
                context=ssl.create_default_context(cafile=ca_file),
            )
        else:
            self.connection = http.client.HTTPConnection(
                self.url.hostname, self.url.port, timeout=timeout
            )
        result = self.rpc(
            "initialize",
            {
                "protocolVersion": self.protocol,
                "capabilities": {},
                "clientInfo": {"name": "mocker-file-import", "version": "1"},
            },
        )
        self.protocol = result["protocolVersion"]
        self.rpc("notifications/initialized", {}, notification=True)

    def rpc(self, method, params, notification=False):
        self.sequence += 1
        request = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notification:
            request["id"] = self.sequence
        headers = {
            "Authorization": "Bearer " + self.token,
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": self.protocol,
            "Mcp-Method": method,
        }
        if method == "tools/call":
            headers["Mcp-Name"] = params["name"]
        if self.session:
            headers["Mcp-Session-Id"] = self.session
        path = self.url.path or "/mcp"
        if self.url.query:
            path += "?" + self.url.query
        try:
            self.connection.request("POST", path, canonical(request), headers)
            response = self.connection.getresponse()
            if response.getheader("Mcp-Session-Id"):
                self.session = response.getheader("Mcp-Session-Id")
            if (
                200 <= response.status < 300
                and not notification
                and "text/event-stream" in response.getheader("Content-Type", "")
            ):
                message = self.sse_result(response)
                self.connection.close()
                return self.result(message)
            raw = response.read(MAX_RESPONSE + 1)
        except (OSError, http.client.HTTPException) as error:
            self.connection.close()
            raise UncertainOutcome(
                "transport outcome unknown; rerun with the same journal"
            ) from error
        if response.status in (408, 502, 503, 504):
            raise UncertainOutcome(
                "HTTP transport outcome unknown; preserve original requests"
            )
        if not 200 <= response.status < 300:
            raise TransferError(f"MCP HTTP request refused (status {response.status})")
        if len(raw) > MAX_RESPONSE:
            raise UncertainOutcome(
                "response exceeded client bound; preserve original requests"
            )
        if notification:
            return None
        return self.result(decode(raw))

    def result(self, message):
        if message.get("id") != self.sequence or "error" in message:
            raise TransferError("MCP protocol request refused or response ID differs")
        return message["result"]

    def sse_result(self, response):
        data, total = [], 0
        while line := response.readline(MAX_RESPONSE + 1):
            total += len(line)
            if total > MAX_RESPONSE:
                raise UncertainOutcome("SSE response exceeded client bound")
            if not line.strip():
                if data:
                    message = decode(b"\n".join(data))
                    data = []
                    if message.get("id") == self.sequence:
                        return message
            elif line.startswith(b"data:"):
                data.append(line[5:].lstrip(b" ").rstrip(b"\r\n"))
        raise UncertainOutcome("SSE ended before the matching response")

    def call(self, name, arguments):
        result = self.rpc("tools/call", {"name": name, "arguments": arguments})
        if result.get("isError"):
            # Do not echo a server validation tree: it can contain source values.
            raise TransferError(
                "MCP tool refused the saved request; inspect it locally"
            )
        if "structuredContent" in result:
            return result["structuredContent"]
        text = next(
            (
                item["text"]
                for item in result.get("content", [])
                if item.get("type") == "text"
            ),
            None,
        )
        if text is None:
            raise UncertainOutcome("tool returned no inspectable result")
        return decode(text)

    def close(self):
        self.connection.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("stage", "commit", "audit-binding"))
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--journal", type=Path, required=True)
    parser.add_argument("--url", required=True)
    parser.add_argument("--token-env", default="MOCKER_MCP_KEY")
    parser.add_argument("--ca-file")
    parser.add_argument("--audit", type=Path)
    args = parser.parse_args()
    caller = None
    try:
        if args.operation != "audit-binding":
            caller = HTTPMCP(args.url, os.environ.get(args.token_env, ""), args.ca_file)
        transfer = Transfer(
            args.plan,
            args.journal,
            args.url,
            caller,
            lambda value: print(json.dumps(value), flush=True),
        )
        if args.operation == "stage":
            transfer.stage()
        elif args.operation == "audit-binding":
            print(json.dumps({"journalHash": transfer.audit_binding()}))
        else:
            if args.audit is None:
                raise TransferError("commit requires --audit PATH")
            result = transfer.commit(args.audit)
            print(
                json.dumps(
                    {"stage": "committed", "revisionId": result["revision"]["id"]}
                )
            )
    except (TransferError, OSError, ValueError, KeyError) as error:
        # Error class is public; inspected source, tokens and raw responses stay
        # in the user's private artifacts rather than terminal/model-visible logs.
        print(
            f"Import transfer stopped ({type(error).__name__}). Preserve the journal and inspect the pending request.",
            file=sys.stderr,
        )
        return 1
    finally:
        if caller is not None:
            caller.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
