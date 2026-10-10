import json
import queue
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from typing import cast
from unittest import mock

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
RENEW = cast(list[Obj], VECTORS["renew"])


def case(result: str, name: str = "") -> tuple[str, int]:
    """The key and the expiry of the first verify case that fits."""
    found = next(
        c for c in CASES if c["result"] == result and name in cast(str, c["name"])
    )
    return cast(str, found["key"]), cast(int, cast(Obj, found["claims"])["exp"])


GOOD, GOOD_EXP = case("valid")
EXPIRED, EXPIRED_EXP = case("expired")
WITH_META, _ = case("valid", "a key with meta")
ENDPOINT = "https://keys.example.com/refresh"


class VerifyTest(unittest.TestCase):
    def test_vectors(self):
        for case in CASES:
            with self.subTest(case["name"]):
                result, claims = "valid", None
                try:
                    claims = inkan.verify(
                        cast(str, case["key"]),
                        cast(str, case["app"]),
                        PUBLIC_KEY,
                        cast(str, case.get("fingerprint", "")),
                        NOW,
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
                        app=cast(str, want["app"]),
                        plan=cast(str, want["plan"]),
                        email=cast(str, want["email"]),
                        exp=cast(int, want["exp"]),
                        id=cast(str, want.get("id", "")),
                        fp=cast(str, want.get("fp", "")),
                        meta=cast(dict[str, str], want.get("meta", {})),
                    ),
                )

    def test_several_public_keys(self):
        good = CASES[0]
        key, app = cast(str, good["key"]), cast(str, good["app"])
        other = "00" * 32
        _ = inkan.verify(key, app, f"{other}, {PUBLIC_KEY}", now=NOW)
        with self.assertRaises(inkan.LicenseError) as raised:
            _ = inkan.verify(key, app, other, now=NOW)
        self.assertEqual(raised.exception.code, "signature")
        with self.assertRaises(ValueError):
            _ = inkan.verify(key, app, "nope", now=NOW)


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


class RenewWaitTest(unittest.TestCase):
    def test_vectors(self):
        for c in RENEW:
            with self.subTest(c["name"]):
                wait = inkan.renew_wait(
                    cast(int, c["exp"]), NOW, cast(int, c["failures"])
                )
                self.assertEqual(wait, c["wait"])


class KeeperTest(unittest.TestCase):
    def expired(self, lic: inkan.Keeper) -> None:
        """Hands lic the key that ran out, which it holds all the same."""
        with self.assertRaises(inkan.LicenseError) as raised:
            _ = lic.set(EXPIRED)
        self.assertEqual(raised.exception.code, "expired")

    def test_holds_a_key_and_checks_its_expiry_on_every_call(self):
        lic = inkan.Keeper("MYAPP", PUBLIC_KEY)
        self.assertFalse(lic.valid())
        self.assertEqual(lic.key(), "")
        with self.assertRaises(inkan.LicenseError) as raised:
            _ = lic.claims()
        self.assertEqual(raised.exception.code, "nokey")

        claims = lic.set(f' "{GOOD}"\n')
        self.assertEqual(claims.id, "sub_123")
        self.assertEqual(lic.key(), GOOD, "held clean, the way an app stores it")
        self.assertTrue(lic.valid())

        with self.assertRaises(inkan.LicenseError):
            _ = lic.set("MYAPP-nope")
        self.assertEqual(lic.key(), GOOD, "a key that is turned down changes nothing")

        # The key runs out while the app runs.
        with mock.patch("time.time", return_value=GOOD_EXP + 1.0):
            self.assertFalse(lic.valid())
            with self.assertRaises(inkan.LicenseError) as raised:
                _ = lic.claims()
        self.assertEqual(raised.exception.code, "expired")
        self.assertEqual(cast(inkan.Claims, raised.exception.claims).id, "sub_123")

    def test_an_expired_key_is_held(self):
        lic = inkan.Keeper("MYAPP", PUBLIC_KEY)
        self.expired(lic)
        self.assertEqual(lic.key(), EXPIRED, "renew can trade it for the current one")
        self.assertFalse(lic.valid())

    def test_claims_are_a_copy(self):
        lic = inkan.Keeper("MYAPP", PUBLIC_KEY)
        claims = lic.set(WITH_META)
        claims.plan = "enterprise"
        claims.meta["seats"] = "9999"
        self.assertEqual(lic.claims().plan, "team")
        self.assertEqual(lic.claims().meta["seats"], "5")

    def test_renew_trades_the_held_key_for_the_current_one(self):
        asked: queue.Queue[str] = queue.Queue()
        waits: list[tuple[int, int]] = []
        saved: list[str] = []
        failed: list[str] = []

        # The worker is unreachable on the first try and has a new key after that.
        def refresh(_endpoint: str, key: str, _app: str, _public_keys: str) -> str:
            asked.put(key)
            if not waits:
                raise OSError("no route to host")
            return GOOD

        # The real waits are pinned by the vectors, here they only have to be short.
        def wait(exp: int, _now: float, failures: int) -> float:
            waits.append((exp, failures))
            return 0.01

        lic = inkan.Keeper("MYAPP", PUBLIC_KEY)
        self.expired(lic)
        stop = threading.Event()
        with (
            mock.patch.object(inkan, "refresh", refresh),
            mock.patch.object(inkan, "renew_wait", wait),
        ):
            thread = lic.renew(
                ENDPOINT,
                save=saved.append,
                failed=lambda err: failed.append(str(err)),
                stop=stop,
            )
            # The third try comes after the first two are over.
            sent = [asked.get(timeout=5) for _ in range(3)]
            stop.set()
            thread.join(timeout=5)

        self.assertFalse(thread.is_alive(), "a set stop ends the thread")
        self.assertEqual(sent, [EXPIRED, EXPIRED, GOOD], "the worker gets the held key")
        self.assertEqual(waits[:2], [(EXPIRED_EXP, 1), (GOOD_EXP, 0)])
        self.assertEqual(saved, [GOOD], "the new key is saved once")
        self.assertEqual(len(failed), 1)
        self.assertIn("no route to host", failed[0])
        self.assertEqual(lic.key(), GOOD)
        self.assertTrue(lic.valid())

    def test_a_stopped_keeper_drops_the_answer_it_waited_for(self):
        stop = threading.Event()
        saved: list[str] = []

        def refresh(_endpoint: str, _key: str, _app: str, _public_keys: str) -> str:
            stop.set()
            return GOOD

        lic = inkan.Keeper("MYAPP", PUBLIC_KEY)
        self.expired(lic)
        with mock.patch.object(inkan, "refresh", refresh):
            thread = lic.renew(ENDPOINT, save=saved.append, stop=stop)
            thread.join(timeout=5)

        self.assertFalse(thread.is_alive())
        self.assertEqual(lic.key(), EXPIRED)
        self.assertEqual(saved, [])


if __name__ == "__main__":
    _ = unittest.main()
