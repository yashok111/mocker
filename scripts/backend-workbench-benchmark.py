#!/usr/bin/env python3
"""Explicit opt-in benchmark runner. Never starts/stops an application or Docker."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import resource
import subprocess
import time
import urllib.parse
import urllib.request

CASES = {
    "observation_import_500": ("./internal/backendobservations", "BenchmarkObservationImport500"),
    "observation_query_500": ("./internal/backendobservations", "BenchmarkObservationQuery500"),
    "measurements_1000_executions": ("./internal/backendanalysis", "BenchmarkScenarioMeasurement1000"),
    "source_diff_10000_30000": ("./internal/backendanalysis", "BenchmarkSourceDiff10000Nodes30000Edges"),
    "percentile_100000": ("./internal/backendanalysis", "BenchmarkMeasurementNearestRank100000"),
}


def nearest_rank(samples, percentile=95):
    if not samples:
        return None
    return sorted(samples)[math.ceil(len(samples) * percentile / 100) - 1]


def environment():
    return {"os": platform.platform(), "machine": platform.machine(),
            "cpu_count": os.cpu_count(), "python": platform.python_version(),
            "target_profile": "NOT VERIFIED: 4 CPU / 8 GiB / local SSD",
            "memory_storage_browser": "Record RAM, storage medium and browser version in workload metadata"}


def run_go(name, package, benchmark, out, repetitions):
    cmd = ["go", "test", package, "-run=^$", f"-bench=^{benchmark}$",
           "-benchtime=1x", f"-count={repetitions + 3}", "-benchmem"]
    started = time.monotonic_ns()
    result = subprocess.run(cmd, capture_output=True, text=True, check=False, timeout=1800)
    (out / f"{name}.log").write_text(result.stdout + result.stderr)
    rows = re.findall(r"^" + re.escape(benchmark) + r"(?:-\d+)?\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", result.stdout, re.M)
    raw = [{"duration_ns": float(ns), "bytes_per_op": int(size), "allocs_per_op": int(allocs)} for ns, size, allocs in rows]
    if result.returncode or len(raw) != repetitions + 3:
        raise RuntimeError(f"{name}: incomplete/failed benchmark; retain log")
    return {"command": cmd, "status": "MEASURED LOCAL", "cache": "warm process; NOT cold-start evidence",
            "wall_ns": time.monotonic_ns() - started, "child_maxrss_platform_units": resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss, "warmups": raw[:3], "samples": raw[3:],
            "p95_ns": nearest_rank([x["duration_ns"] for x in raw[3:]])}


def run_http(workload, base, out, repetitions, admin_host):
    if not re.fullmatch(r"[A-Za-z0-9.-]+(?::[0-9]{1,5})?", admin_host):
        raise ValueError("Explicit valid admin Host is required")
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "::1") or not parsed.port or parsed.username or parsed.password or parsed.path not in ("", "/") or parsed.query or parsed.fragment:
        raise ValueError("Use an explicitly isolated loopback HTTP host:port")
    results = {}
    token = os.environ.get("MOCKER_BENCHMARK_MCP_KEY", "")
    # Tool requests run through the public authenticated MCP boundary. No redirects
    # or URL inputs are accepted; only supplied exact workloads are replayed.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    for case in workload["cases"]:
        name = case["name"]
        if not re.fullmatch(r"[a-z0-9_]+", name):
            raise ValueError("invalid case name")
        requests = case["requests"]
        if len(requests) != repetitions + 3:
            raise ValueError("Supply warmup + sample requests with exact pins and distinct correlation keys/CAS versions")
        samples = []
        for index, request in enumerate(requests):
            allowed = {"query_backend_graph", "query_backend_diagram", "get_backend_observation_records", "correlate_backend_observations", "start_backend_analysis", "get_backend_analysis", "get_backend_analysis_results"}
            if request["name"] not in allowed:
                raise ValueError("unsupported benchmark operation")
            body = json.dumps({"jsonrpc": "2.0", "id": index + 1, "method": "tools/call", "params": request}).encode()
            req = urllib.request.Request(base.rstrip("/") + "/mcp", data=body, headers={"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "Authorization": "Bearer " + token, "Host": admin_host})
            start = time.monotonic_ns()
            with opener.open(req, timeout=65) as response:
                raw = response.read(34 << 20)
            duration = time.monotonic_ns() - start
            (out / f"{name}-{index:02d}.response").write_bytes(raw)
            envelope = json.loads(raw)
            if "error" in envelope or envelope.get("result", {}).get("isError"):
                raise RuntimeError(f"failed MCP result {name}/{index}")
            samples.append({"duration_ns": duration, "response_bytes": len(raw), "request_sha256": hashlib.sha256(body).hexdigest()})
        results[name] = {"status": "MEASURED LOCAL", "metadata": case.get("metadata", {}), "warmups": samples[:3], "samples": samples[3:], "p95_ns": nearest_rank([x["duration_ns"] for x in samples[3:]]), "limits": "start_backend_analysis measures admission only; measure terminal execution separately"}
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", action="store_true", help="Explicitly execute long benchmarks")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--repetitions", type=int, default=30)
    parser.add_argument("--isolated-base-url")
    parser.add_argument("--admin-host", default="")
    parser.add_argument("--workload", type=Path)
    parser.add_argument("--source-manifest", type=Path)
    args = parser.parse_args()
    if args.repetitions < 30:
        parser.error("At least 30 measured repetitions required")
    if args.run and not args.source_manifest:
        parser.error("--run requires --source-manifest with exact source/build identity")
    args.output.mkdir(parents=True, exist_ok=False)
    report = {"environment": environment(), "warmups": 3, "repetitions": args.repetitions,
              "cases": {name: {"status": "NOT RUN"} for name in CASES},
              "browser": {"status": "NOT RUN", "profiles": ["first canvas", "200 nodes / 600 edges", "10000 nodes / 30000 edges"]},
              "acceptance": "NOT ACCEPTED: local samples do not establish target hardware/product acceptance"}
    try:
        if args.source_manifest:
            manifest = args.source_manifest.read_bytes()
            report["source_manifest_sha256"] = hashlib.sha256(manifest).hexdigest()
            (args.output / "source-manifest.json").write_bytes(manifest)
        if args.workload:
            raw = args.workload.read_bytes()
            report["workload_sha256"] = hashlib.sha256(raw).hexdigest()
            (args.output / "workload.json").write_bytes(raw)
        if args.run:
            for name, (package, benchmark) in CASES.items():
                try:
                    report["cases"][name] = run_go(name, package, benchmark, args.output, args.repetitions)
                except Exception as error:
                    report["cases"][name] = {"status": "FAIL", "error": str(error)}
                    raise
            if args.workload and args.isolated_base_url:
                report["public_cases"] = run_http(json.loads(raw), args.isolated_base_url, args.output, args.repetitions, args.admin_host)
    finally:
        (args.output / "report.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
