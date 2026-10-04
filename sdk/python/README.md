# inkan for Python

Verifies [inkan](https://github.com/MizuchiLabs/inkan) license keys offline.
One file, its only dependency is `cryptography`.

```sh
pip install inkan
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

An app that is online can trade its key for the current one. Store what comes
back and start with it next time.

```python
key = inkan.refresh("https://<worker>/refresh", old_key, "MYAPP", PUBLIC_KEY)
```

Run the tests with `python -m unittest`. They read the vectors of the Go
package.
