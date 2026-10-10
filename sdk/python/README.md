# inkan for Python

Verifies [inkan](https://github.com/MizuchiLabs/inkan) license keys offline,
and holds and renews the key of a running app. One file, its only dependency
is `cryptography`.

It is not on PyPI yet, and `inkan` there is another project. Copy
[`inkan.py`](inkan.py) into your app and install its one dependency.

```sh
pip install cryptography
```

```python
import os
import inkan

# The hex key from the Products page of inkan.
PUBLIC_KEY = "03a107bf…"

try:
    claims = inkan.verify(os.environ.get("MYAPP_LICENSE_KEY", ""), "MYAPP", PUBLIC_KEY)
    print(claims.plan, claims.meta.get("seats"))
except inkan.LicenseError as err:
    if err.code == "expired":
        print(f"the license of {err.claims.email} ran out")
```

## In an app that stays up

A `Keeper` holds the key, so it can change while the app runs.

```python
lic = inkan.Keeper("MYAPP", PUBLIC_KEY)
try:
    lic.set(stored_key)
except inkan.LicenseError:
    pass  # Not a key of this app. A key that ran out is held all the same.
lic.renew("https://<worker>/refresh", save=store)

A third argument binds the keeper to this machine. It is whatever the app
calls the machine, like an install id it keeps in its data directory. A key
issued with that fingerprint works nowhere else, a key without one works
everywhere. `verify` takes it as `fingerprint=`.

lic.valid()  # is there a license right now
lic.claims()  # plan, email and meta, raises a LicenseError without one
lic.set(pasted)  # a new key came by mail
```

- **Live.** `valid` and `claims` check the expiry on every call, so a running
  app notices when its key runs out.
- **Swap.** `set` replaces the key while the app runs. A key it turns down
  changes nothing.
- **Renew.** `renew` asks the worker from a thread of its own, right away and
  then once a day, sooner for a key that runs out before that. Every new key
  goes to `save`, so the app can store it for its next start. `failed` gets
  the error of a try that did not work. The thread is a daemon and ends with
  the app, or when the `threading.Event` passed as `stop` is set. This needs
  the worker of the product.

`refresh` is the one step behind `renew`, for an app with a loop of its own.
It trades a key for the current one. `renew_wait` says how long to wait until
the next one.

```python
key = inkan.refresh("https://<worker>/refresh", old_key, "MYAPP", PUBLIC_KEY)
```

Run the tests with `python -m unittest`. They read the vectors of the Go
package.
