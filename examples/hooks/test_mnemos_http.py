import json
import tempfile
import unittest
from pathlib import Path

from mnemos_http import load_server, request_for


class HTTPHookTests(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.config = Path(self.tempdir.name) / "selected config.toml"

    def test_auth_and_json_quote_newline_round_trip(self):
        self.config.write_text(
            '[server]\nhttp_addr = "127.0.0.1:8080"\napi_key = "secret-token"\n',
            encoding="utf-8",
        )
        payload = {"Content": 'quote " safely\nsecond line', "Project": "space & slash/雪"}
        request = request_for(str(self.config), "/v1/observations", payload)

        self.assertEqual(request.full_url, "http://127.0.0.1:8080/v1/observations")
        self.assertEqual(request.get_header("Authorization"), "Bearer secret-token")
        self.assertEqual(request.get_header("Content-type"), "application/json")
        self.assertEqual(json.loads(request.data.decode("utf-8")), payload)

    def test_colon_address_and_empty_key(self):
        self.config.write_text('[server]\nhttp_addr = ":9090"\n', encoding="utf-8")
        self.assertEqual(load_server(str(self.config)), ("http://localhost:9090", ""))
        request = request_for(str(self.config), "/v1/touch", {"path": 'a"b\nc'})
        self.assertIsNone(request.get_header("Authorization"))
        self.assertEqual(json.loads(request.data.decode("utf-8")), {"path": 'a"b\nc'})


if __name__ == "__main__":
    unittest.main()
