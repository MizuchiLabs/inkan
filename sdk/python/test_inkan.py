import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import inkan

VECTORS = json.loads((Path(__file__).parents[2] / "license/testdata/vectors.json").read_text())
PUBLIC_KEY = VECTORS["public_key"]


class VerifyTest(unittest.TestCase):
    def test_vectors(self):
        for case in VECTORS["verify"]:
            with self.subTest(case["name"]):
                result, claims = "valid", None
                try:
                    claims = inkan.verify(case["key"], case["app"], PUBLIC_KEY, VECTORS["now"])
                except inkan.LicenseError as err:
                    result, claims = err.code, err.claims
                self.assertEqual(result, case["result"])
                want = case["claims"]
                if want is None:
                    self.assertIsNone(claims)
                    continue
                self.assertEqual(
                    claims,
                    inkan.Claims(
                        v=want["v"],
                        app=want["app"],
                        plan=want["plan"],
                        email=want["email"],
                        exp=want["exp"],
                        id=want.get("id", ""),
                        meta=want.get("meta", {}),
                    ),
                )

    def test_several_public_keys(self):
        good = VECTORS["verify"][0]
        other = "00" * 32
        inkan.verify(good["key"], good["app"], f"{other}, {PUBLIC_KEY}", VECTORS["now"])
        with self.assertRaises(inkan.LicenseError) as raised:
            inkan.verify(good["key"], good["app"], other, VECTORS["now"])
        self.assertEqual(raised.exception.code, "signature")
        with self.assertRaises(ValueError):
            inkan.verify(good["key"], good["app"], "nope", VECTORS["now"])


class RefreshTest(unittest.TestCase):
    def test_refresh_only_returns_a_key_of_this_app(self):
        good = VECTORS["verify"][0]["key"]
        expired = next(c["key"] for c in VECTORS["verify"] if c["result"] == "expired")
        answers = {
            "/renewed": (200, good + "\n"),
            "/expired": (200, expired),
            "/hostile": (200, "MYAPP-AAAAA"),
            "/gone": (404, "no license behind this key"),
        }

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                status, body = answers[self.path]
                self.send_response(status)
                self.end_headers()
                self.wfile.write(body.encode())

            def log_message(self, *args):
                pass

        server = HTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        url = f"http://127.0.0.1:{server.server_port}"

        self.assertEqual(inkan.refresh(url + "/renewed", expired, "MYAPP", PUBLIC_KEY), good)
        self.assertEqual(inkan.refresh(url + "/expired", expired, "MYAPP", PUBLIC_KEY), expired)
        with self.assertRaises(inkan.LicenseError):
            inkan.refresh(url + "/hostile", expired, "MYAPP", PUBLIC_KEY)
        with self.assertRaisesRegex(RuntimeError, "no license behind"):
            inkan.refresh(url + "/gone", expired, "MYAPP", PUBLIC_KEY)


if __name__ == "__main__":
    unittest.main()
