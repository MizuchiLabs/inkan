import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from typing import cast

import inkan

Obj = dict[str, object]
VECTORS = cast(
    Obj,
    json.loads(
        (Path(__file__).parents[2] / "license/testdata/vectors.json").read_text()
    ),
)
PUBLIC_KEY = cast(str, VECTORS["public_key"])
NOW = cast(float, VECTORS["now"])
CASES = cast(list[Obj], VECTORS["verify"])


class VerifyTest(unittest.TestCase):
    def test_vectors(self):
        for case in CASES:
            with self.subTest(case["name"]):
                result, claims = "valid", None
                try:
                    claims = inkan.verify(
                        cast(str, case["key"]), cast(str, case["app"]), PUBLIC_KEY, NOW
                    )
                except inkan.LicenseError as err:
                    result, claims = err.code, err.claims
                self.assertEqual(result, case["result"])
                want = cast(Obj | None, case["claims"])
                if want is None:
                    self.assertIsNone(claims)
                    continue
                self.assertEqual(
                    claims,
                    inkan.Claims(
                        v=cast(int, want["v"]),
                        app=cast(str, want["app"]),
                        plan=cast(str, want["plan"]),
                        email=cast(str, want["email"]),
                        exp=cast(int, want["exp"]),
                        id=cast(str, want.get("id", "")),
                        meta=cast(dict[str, str], want.get("meta", {})),
                    ),
                )

    def test_several_public_keys(self):
        good = CASES[0]
        key, app = cast(str, good["key"]), cast(str, good["app"])
        other = "00" * 32
        _ = inkan.verify(key, app, f"{other}, {PUBLIC_KEY}", NOW)
        with self.assertRaises(inkan.LicenseError) as raised:
            _ = inkan.verify(key, app, other, NOW)
        self.assertEqual(raised.exception.code, "signature")
        with self.assertRaises(ValueError):
            _ = inkan.verify(key, app, "nope", NOW)


class RefreshTest(unittest.TestCase):
    def test_refresh_only_returns_a_key_of_this_app(self):
        good = cast(str, CASES[0]["key"])
        expired = next(cast(str, c["key"]) for c in CASES if c["result"] == "expired")
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
                _ = self.wfile.write(body.encode())

            def log_message(self, format: str, *args: object):  # pyright: ignore[reportImplicitOverride]
                pass

        server = HTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        url = f"http://127.0.0.1:{server.server_port}"

        self.assertEqual(
            inkan.refresh(url + "/renewed", expired, "MYAPP", PUBLIC_KEY), good
        )
        self.assertEqual(
            inkan.refresh(url + "/expired", expired, "MYAPP", PUBLIC_KEY), expired
        )
        with self.assertRaises(inkan.LicenseError):
            _ = inkan.refresh(url + "/hostile", expired, "MYAPP", PUBLIC_KEY)
        with self.assertRaisesRegex(RuntimeError, "no license behind"):
            _ = inkan.refresh(url + "/gone", expired, "MYAPP", PUBLIC_KEY)


if __name__ == "__main__":
    _ = unittest.main()
