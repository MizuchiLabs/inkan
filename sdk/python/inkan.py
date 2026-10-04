"""Verifies inkan license keys.

The format is in FORMAT.md, the Go package
github.com/mizuchilabs/inkan/license is the reference.
"""

import base64
import binascii
import json
import re
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from http.client import HTTPResponse
from typing import TypeVar, cast

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

__all__ = ["Claims", "LicenseError", "clean", "refresh", "verify"]

_WIRE_VERSION = 1
_SIGNATURE_SIZE = 64
_T = TypeVar("_T")
_MESSAGES = {
    "format": "license format is invalid",
    "signature": "signature verification failed",
    "app": "license issued for a different application",
    "version": "invalid license version",
    "expired": "license has expired",
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

    An expired key carries its claims, to show who it was for.
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
    if claims.exp != 0 and (time.time() if now is None else now) > claims.exp:
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
