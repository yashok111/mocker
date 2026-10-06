import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("b62_benchmark", Path(__file__).parents[1] / "backend-workbench-benchmark.py")
benchmark = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmark)

class BenchmarkContractTests(unittest.TestCase):
    def test_raw_nearest_rank_and_missing(self):
        self.assertEqual(benchmark.nearest_rank(list(range(1, 31))), 29)
        self.assertIsNone(benchmark.nearest_rank([]))

    def test_host_routing_preserves_loopback_connection(self):
        class Response:
            def __enter__(self): return self
            def __exit__(self, *args): return False
            def read(self, limit): return b'{"result":{"content":[]}}'
        class Opener:
            def open(self, request, timeout):
                self.request = request
                return Response()
        opener = Opener()
        workload = {"cases":[{"name":"exact_read", "requests":[{"name":"get_backend_analysis","arguments":{"analysisId":"exact"}}] * 33}]}
        with tempfile.TemporaryDirectory() as out, patch.object(benchmark.urllib.request, "build_opener", return_value=opener):
            result = benchmark.run_http(workload,"http://127.0.0.1:18099",Path(out),30,"admin.isolated.test")
        self.assertEqual(opener.request.full_url,"http://127.0.0.1:18099/mcp")
        self.assertEqual(opener.request.get_header("Host"),"admin.isolated.test")
        self.assertEqual(len(result["exact_read"]["samples"]),30)

    def test_reject_non_loopback_and_header_injection(self):
        for base, host in [("http://example.com:8080","admin.test"),("http://127.0.0.1:18099","admin\r\nInjected: value")]:
            with self.assertRaises(ValueError): benchmark.run_http({"cases":[]},base,Path("/tmp"),30,host)

if __name__ == "__main__": unittest.main()
