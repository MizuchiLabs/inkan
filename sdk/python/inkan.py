"""Verifies inkan license keys.

The format is in FORMAT.md, the Go package
github.com/mizuchilabs/inkan/license is the reference.
"""

import base64
import binascii
import json
import re
import threading
import time
import urllib.error
import urllib.request
from collections.abc import Callable
from dataclasses import dataclass, field, replace
from http.client import HTTPResponse
from typing import TypeVar, cast

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

__all__ = [
    "Claims",
    "Keeper",
    "LicenseError",
    "clean",
    "refresh",
    "renew_wait",
    "verify",
]

_WIRE_VERSION = 1
_SIGNATURE_SIZE = 64
_MINUTE = 60
_DAY = 86_400
_T = TypeVar("_T")
_MESSAGES = {
    "format": "license format is invalid",
    "signature": "signature verification failed",
    "app": "license issued for a different application",
    "version": "invalid license version",
    "expired": "license has expired",
    "nokey": "no license key set",
}


@dataclass
class Claims:
    v: int
    app: str
    plan: str
    email: str
    exp: int  # unix seconds, 0 never expires
    id: str = ""  # stable id of the license, empty in old keys
    meta: dict[str, str] = field(
        default_factory=dict
    )  # extras the app defines, like seats


class LicenseError(Exception):
    """code is one of format, signature, app, version, expired.

    An expired key carries its claims, to show who it was for. A Keeper that
    holds no key raises with the code nokey.
    """

    code: str
    claims: Claims | None

    def __init__(self, code: str, claims: Claims | None = None):
        super().__init__(_MESSAGES[code])
        self.code = code
        self.claims = claims


def clean(key: str) -> str:
    """Strips what a key picks up on its way: the line breaks of a mail
    client, stray spaces, the quotes of an env file. verify does it by itself.
    """
    return "".join(key.split()).strip("\"'")


def verify(key: str, app: str, public_keys: str, now: float | None = None) -> Claims:
    """Checks a key and returns its claims.

    app is compared exactly, the prefix of the key is its uppercase form.
    public_keys is the hex key from the Products page, several can be given
    separated by commas while a signing key rotates.

    Raises LicenseError. An expired key raises with code "expired" and
    carries its claims. now is in unix seconds.
    """
    keys = [_public_key(part.strip()) for part in public_keys.split(",")]

    prefix = app.upper() + "-"
    cleaned = clean(key)
    if not cleaned.startswith(prefix):
        raise LicenseError("format")
    raw = _decode(cleaned[len(prefix) :].replace("-", ""))
    if raw is None or len(raw) < 1 + _SIGNATURE_SIZE + 1 or raw[0] != _WIRE_VERSION:
        raise LicenseError("format")
    payload, signature = raw[1:-_SIGNATURE_SIZE], raw[-_SIGNATURE_SIZE:]

    if not any(_signed(public_key, signature, payload) for public_key in keys):
        raise LicenseError("signature")

    claims = _parse(payload)
    if claims.app != app:
        raise LicenseError("app")
    if claims.v != _WIRE_VERSION:
        raise LicenseError("version")
    if _ran_out(claims, time.time() if now is None else now):
        raise LicenseError("expired", claims)
    return claims


def refresh(endpoint: str, key: str, app: str, public_keys: str) -> str:
    """Asks a worker for the key a license is owed now.

    The answer is verified before it is returned, so a broken server cannot
    hand the app anything but a real key of this app. It can still be expired,
    verify tells. On an error keep the key you have.
    """
    request = urllib.request.Request(
        endpoint,
        data=clean(key).encode(),
        headers={"Content-Type": "text/plain; charset=utf-8"},
        method="POST",
    )
    try:
        with cast(
            HTTPResponse, urllib.request.urlopen(request, timeout=15)
        ) as response:
            answer = response.read(16 << 10).decode().strip()
    except urllib.error.HTTPError as err:
        body = err.read(16 << 10).decode(errors="replace").strip()
        raise RuntimeError(f"refreshing license: {err.code}: {body[:200]}") from err
    try:
        _ = verify(answer, app, public_keys)
    except LicenseError as err:
        if err.code != "expired":
            raise
    return clean(answer)


def renew_wait(exp: int, now: float, failures: int) -> float:
    """How many seconds a keeper waits before it asks the worker again.

    exp is the expiry of the held key and now the time, both in unix seconds.
    failures counts the tries in a row that did not work.

    Once a day, at half of the time a key has left when that comes sooner, and
    a minute after a failed try, twice as long after every further one.
    FORMAT.md has the rule. Keeper.renew follows it, an app with a loop of its
    own can ask here.
    """
    if failures > 0:
        return min(_MINUTE << (failures - 1), _DAY)
    left = exp - now
    if exp == 0 or left <= 0:
        return _DAY
    # Never under a minute, or the end of a key that nobody renews is asked
    # about in a tight loop.
    return min(_DAY, max(left / 2, _MINUTE))


class Keeper:
    """Holds the license key of a running app, so the key can change without a
    restart. Either someone hands the app a new key, or the app fetches it from
    a worker:

        lic = inkan.Keeper("MYAPP", PUBLIC_KEY)
        lic.set(stored_key)
        lic.renew("https://keys.example.com/refresh", save=store)

    Ask valid() wherever the app gates a feature, or claims() if it depends on
    the plan or the meta. Both are cheap, the signature is only checked in set.
    All methods are safe to call from several threads.

    app and public_keys are the ones verify takes.
    """

    _app: str
    _public_keys: str
    _held: tuple[str, Claims] | None

    def __init__(self, app: str, public_keys: str):
        self._app = app
        self._public_keys = public_keys
        self._held = None

    def set(self, key: str) -> Claims:
        """Swaps in a key and returns its claims like verify does.

        An expired key is held too, renew can trade it for the current one,
        and set raises "expired" with its claims. On any other error the
        keeper keeps the key it has.
        """
        self._hold(key)
        return self.claims()

    def valid(self) -> bool:
        """Whether a key is held that has not run out. It is all an app needs
        that only asks if there is a license.
        """
        held = self._held
        return held is not None and not _ran_out(held[1], time.time())

    def claims(self) -> Claims:
        """The claims of the held key.

        The expiry is checked on every call, so a key that runs out while the
        app runs raises "expired" with its claims. Without a key it raises
        "nokey".
        """
        held = self._held
        if held is None:
            raise LicenseError("nokey")
        # A copy, the caller must not be able to change what is held.
        claims = replace(held[1], meta=dict(held[1].meta))
        if _ran_out(claims, time.time()):
            raise LicenseError("expired", claims)
        return claims

    def key(self) -> str:
        """The held key, for the app to store. It is empty without one."""
        held = self._held
        return held[0] if held else ""

    def renew(
        self,
        endpoint: str,
        save: Callable[[str], object] | None = None,
        failed: Callable[[Exception], object] | None = None,
        stop: threading.Event | None = None,
    ) -> threading.Thread:
        """Keeps the held key current in a thread of its own, until stop is set.

        It asks the worker at endpoint with refresh, right away and then as
        renew_wait says: once a day, sooner for a key that runs out before
        that, and with growing waits after a failed try. The held key stays
        as it is when a try fails.

        save gets every new key, so the app can store it and start with it
        next time. failed gets the error of every try that did not work, for
        the app to log. The thread is a daemon, it does not keep an app alive
        that is otherwise done.
        """
        stopped = stop or threading.Event()

        def loop() -> None:
            wait: float = 0
            failures = 0
            while not stopped.wait(wait):
                old = self.key()
                if not old:
                    # A key pasted later must not wait a day for its first refresh.
                    wait = _MINUTE
                    continue
                try:
                    key = refresh(endpoint, old, self._app, self._public_keys)
                    if stopped.is_set():
                        return
                    # The next outage starts with short waits again.
                    failures = 0
                    if key != old:
                        # refresh has verified the key, it cannot be turned down.
                        self._hold(key)
                        if save:
                            _ = save(key)
                # The network, the answer or save, nothing may end the thread.
                except Exception as err:  # noqa: BLE001
                    if stopped.is_set():
                        return
                    failures += 1
                    if failed:
                        _ = failed(err)
                held = self._held
                wait = renew_wait(held[1].exp if held else 0, time.time(), failures)

        thread = threading.Thread(target=loop, name="inkan-renew", daemon=True)
        thread.start()
        return thread

    def _hold(self, key: str) -> None:
        try:
            claims = verify(key, self._app, self._public_keys)
        except LicenseError as err:
            if err.code != "expired" or err.claims is None:
                raise
            claims = err.claims
        self._held = (clean(key), claims)


def _ran_out(claims: Claims, now: float) -> bool:
    return claims.exp != 0 and now > claims.exp


def _public_key(hex_key: str) -> Ed25519PublicKey:
    try:
        raw = bytes.fromhex(hex_key)
    except ValueError:
        raw = b""
    if len(raw) != 32:
        raise ValueError("invalid ed25519 public key hex")
    return Ed25519PublicKey.from_public_bytes(raw)


def _signed(public_key: Ed25519PublicKey, signature: bytes, payload: bytes) -> bool:
    try:
        public_key.verify(signature, payload)
    except InvalidSignature:
        return False
    return True


def _decode(text: str) -> bytes | None:
    # Standard alphabet, no padding.
    if not re.fullmatch(r"[A-Za-z0-9+/]*", text) or len(text) % 4 == 1:
        return None
    try:
        return base64.b64decode(text + "=" * (-len(text) % 4))
    except binascii.Error:
        return None


def _parse(payload: bytes) -> Claims:
    try:
        loaded = cast(object, json.loads(payload.decode(errors="replace")))
    except ValueError:
        raise LicenseError("format") from None
    if not isinstance(loaded, dict):
        raise LicenseError("format")
    fields = cast(dict[str, object], loaded)

    def get(name: str, kind: type[_T], zero: _T) -> _T:
        value = fields.get(name)
        if value is None:
            return zero
        # bool is an int in Python, a key must not pass with "exp": true.
        if not isinstance(value, kind) or isinstance(value, bool):
            raise LicenseError("format")
        return value

    meta: dict[str, str] = {}
    raw_meta = fields.get("meta")
    if raw_meta is not None:
        if not isinstance(raw_meta, dict):
            raise LicenseError("format")
        for key, value in cast(dict[object, object], raw_meta).items():
            if not isinstance(key, str) or not isinstance(value, str):
                raise LicenseError("format")
            meta[key] = value
    return Claims(
        v=get("v", int, 0),
        app=get("app", str, ""),
        plan=get("plan", str, ""),
        email=get("email", str, ""),
        exp=get("exp", int, 0),
        id=get("id", str, ""),
        meta=meta,
    )
