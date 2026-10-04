package license

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"sync/atomic"
	"time"
)

const refreshEvery = 24 * time.Hour

// Keeper holds the license key of a running app, so the key can change
// without a restart. Either someone hands the app a new key, or the app
// fetches it from a worker:
//
//	lic := license.NewKeeper("MYAPP")
//	_, err := lic.Set(os.Getenv("MYAPP_LICENSE_KEY"))
//	go lic.Renew(ctx, "https://keys.example.com/refresh", save)
//
// Ask [Keeper.Valid] wherever the app gates a feature, or [Keeper.Claims] if
// it depends on the plan or the meta. Both are cheap, the signature is only
// checked in [Keeper.Set]. All methods are safe for concurrent use.
type Keeper struct {
	app  string
	held atomic.Pointer[held]
}

type held struct {
	key    string
	claims Claims
}

// NewKeeper returns a Keeper without a key. app is the one [Verify] takes.
func NewKeeper(app string) *Keeper {
	return &Keeper{app: app}
}

// Set swaps in rawKey and returns its claims like [Verify] does. An expired
// key is held too, [Keeper.Renew] can trade it for the current one. On any
// other error the Keeper keeps the key it has.
func (k *Keeper) Set(rawKey string) (*Claims, error) {
	claims, err := Verify(rawKey, k.app)
	if err != nil && !errors.Is(err, ErrExpired) {
		return nil, err
	}
	k.held.Store(&held{key: Clean(rawKey), claims: *claims})
	return k.Claims()
}

// Valid reports whether a key is held that has not run out. It is all an app
// needs that only asks if there is a license.
func (k *Keeper) Valid() bool {
	h := k.held.Load()
	return h != nil && !h.claims.IsExpired()
}

// Claims returns the claims of the held key. The expiry is checked on every
// call, so a key that runs out while the app runs gives [ErrExpired] next to
// its claims. Without a key the error is [ErrNoKey].
func (k *Keeper) Claims() (*Claims, error) {
	h := k.held.Load()
	if h == nil {
		return nil, ErrNoKey
	}
	claims := h.claims
	// A copy, the caller must not be able to change what is held.
	claims.Meta = maps.Clone(claims.Meta)
	if claims.IsExpired() {
		return &claims, ErrExpired
	}
	return &claims, nil
}

// Key returns the held key, for the app to store. It is empty without one.
func (k *Keeper) Key() string {
	if h := k.held.Load(); h != nil {
		return h.key
	}
	return ""
}

// Renew keeps the held key current until ctx is done. It asks the worker at
// endpoint with [Refresh], right away and then once a day. A failed try is
// repeated after a minute, then after two, and so on up to a day.
//
// save gets every new key, so the app can store it and start with it next
// time. It may be nil. The app then starts with its first key every time and
// needs the worker to answer once that key has run out.
func (k *Keeper) Renew(ctx context.Context, endpoint string, save func(key string)) {
	wait, retry := time.Duration(0), time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = refreshEvery

		old := k.Key()
		if old == "" {
			// A key pasted later must not wait a day for its first refresh.
			wait = time.Minute
			continue
		}
		key, err := Refresh(ctx, endpoint, old, k.app)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			slog.Warn("license refresh failed", "error", err)
			wait, retry = retry, min(2*retry, refreshEvery)
		case key != old:
			// Refresh has verified the key, Set cannot turn it down.
			_, _ = k.Set(key)
			if save != nil {
				save(key)
			}
		}
	}
}
