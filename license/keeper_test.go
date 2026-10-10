package license

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func issueFor(t *testing.T, priv ed25519.PrivateKey, plan string, valid time.Duration) string {
	t.Helper()
	key, err := Issue("myapp", Claims{Plan: plan, ExpiresAt: time.Now().Add(valid).Unix(), ID: "sub_1"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestKeeperSet(t *testing.T) {
	pub, priv := testKeypair(t)
	pro := issueFor(t, priv, "pro", time.Hour)
	team := issueFor(t, priv, "team", time.Hour)
	expired := issueFor(t, priv, "pro", -time.Hour)

	k := NewKeeper("myapp", pub, "")
	if c, err := k.Claims(); c != nil || !errors.Is(err, ErrNoKey) || k.Key() != "" || k.Valid() {
		t.Fatalf("an empty keeper must say so: %+v, %v, %q", c, err, k.Key())
	}

	if c, err := k.Set(" " + pro + "\n"); err != nil || c.Plan != "pro" || k.Key() != pro || !k.Valid() {
		t.Fatalf("a valid key must be held: %+v, %v, %q", c, err, k.Key())
	}
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Set(issueFor(t, stranger, "team", time.Hour)); !errors.Is(err, ErrInvalidSig) {
		t.Fatalf("the key of a stranger: got %v", err)
	}
	if c, err := k.Claims(); err != nil || c.Plan != "pro" || k.Key() != pro {
		t.Fatalf("a rejected key must leave the held one alone: %+v, %v", c, err)
	}

	if c, err := k.Set(team); err != nil || c.Plan != "team" || k.Key() != team {
		t.Fatalf("a new key must replace the held one: %+v, %v", c, err)
	}

	// Renew needs it as its ticket.
	if c, err := k.Set(expired); !errors.Is(err, ErrExpired) || c == nil || k.Key() != expired || k.Valid() {
		t.Fatalf("an expired key must be held with its claims: %+v, %v", c, err)
	}
}

func TestKeeperWithoutAPublicKey(t *testing.T) {
	_, priv := testKeypair(t)
	k := NewKeeper("myapp", "", "")
	if _, err := k.Set(issueFor(t, priv, "pro", time.Hour)); !errors.Is(err, ErrNoPublicKey) {
		t.Fatalf("got %v, want ErrNoPublicKey", err)
	}
	if k.Valid() || k.Key() != "" {
		t.Fatal("a keeper that cannot verify must hold nothing")
	}
}

func TestKeeperClaimsAreACopy(t *testing.T) {
	pub, priv := testKeypair(t)
	key, err := Issue("myapp", Claims{Plan: "pro", Meta: map[string]string{"seats": "5"}}, priv)
	if err != nil {
		t.Fatal(err)
	}
	k := NewKeeper("myapp", pub, "")
	c, err := k.Set(key)
	if err != nil {
		t.Fatal(err)
	}
	c.Meta["seats"] = "500"

	if c, _ := k.Claims(); c.Meta["seats"] != "5" {
		t.Fatalf("a caller changed the held claims: %+v", c.Meta)
	}
}

func TestKeeperNoticesExpiryWhileRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pub, priv := testKeypair(t)
		k := NewKeeper("myapp", pub, "")
		if _, err := k.Set(issueFor(t, priv, "pro", time.Hour)); err != nil {
			t.Fatal(err)
		}

		if !k.Valid() {
			t.Fatal("the key has an hour left")
		}

		time.Sleep(2 * time.Hour)
		if k.Valid() {
			t.Fatal("the key ran out an hour ago")
		}
		if c, err := k.Claims(); !errors.Is(err, ErrExpired) || c == nil || c.Plan != "pro" {
			t.Fatalf("got %+v, %v, want the claims and ErrExpired", c, err)
		}
	})
}

func TestKeeperRenew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pub, priv := testKeypair(t)
		old := issueFor(t, priv, "pro", -time.Hour)
		renewed := issueFor(t, priv, "pro", 30*24*time.Hour)

		// The worker is unreachable on the first try and has a new key after that.
		start := time.Now()
		var asked []time.Duration
		var sent []string
		refreshClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			asked = append(asked, time.Since(start))
			sent = append(sent, string(body))
			if len(asked) == 1 {
				return nil, errors.New("no route to host")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(renewed))}, nil
		})
		t.Cleanup(func() { refreshClient.Transport = nil })

		k := NewKeeper("myapp", pub, "")
		if _, err := k.Set(old); !errors.Is(err, ErrExpired) {
			t.Fatalf("the old key: got %v", err)
		}

		var saved, failed []string
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			k.Renew(ctx, "https://keys.example.com/refresh",
				func(key string) { saved = append(saved, key) },
				func(err error) { failed = append(failed, err.Error()) },
			)
		}()
		time.Sleep(25 * time.Hour)
		cancel()
		<-done

		if want := []time.Duration{0, time.Minute, time.Minute + 24*time.Hour}; !slices.Equal(asked, want) {
			t.Fatalf("asked at %v, want %v", asked, want)
		}
		if want := []string{old, old, renewed}; !slices.Equal(sent, want) {
			t.Fatalf("the worker must get the held key every time, got %q", sent)
		}
		if !slices.Equal(saved, []string{renewed}) {
			t.Fatalf("saved %q, want the new key once", saved)
		}
		if len(failed) != 1 || !strings.Contains(failed[0], "no route to host") {
			t.Fatalf("failed got %q, want the one try that did not get through", failed)
		}
		if c, err := k.Claims(); err != nil || k.Key() != renewed {
			t.Fatalf("the new key must be held: %+v, %v", c, err)
		}
	})
}

// The waits after a failed try grow. A try that works has to bring them back
// down, or one bad night makes every later hiccup cost hours.
func TestKeeperRenewStartsOverAfterASuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pub, priv := testKeypair(t)
		held := issueFor(t, priv, "pro", 30*24*time.Hour)

		// Down for two tries, back for one, down for one more.
		down := map[int]bool{1: true, 2: true, 4: true}
		start := time.Now()
		var asked []time.Duration
		refreshClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			asked = append(asked, time.Since(start))
			if down[len(asked)] {
				return nil, errors.New("no route to host")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(held))}, nil
		})
		t.Cleanup(func() { refreshClient.Transport = nil })

		k := NewKeeper("myapp", pub, "")
		if _, err := k.Set(held); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			k.Renew(ctx, "https://keys.example.com/refresh", nil, nil)
		}()
		time.Sleep(25 * time.Hour)
		cancel()
		<-done

		day := 24 * time.Hour
		want := []time.Duration{0, time.Minute, 3 * time.Minute, day + 3*time.Minute, day + 4*time.Minute}
		if !slices.Equal(asked, want) {
			t.Fatalf("asked at %v, want %v", asked, want)
		}
	})
}

// A worker with max key days hands out keys that end before the next daily
// try. The next key is there for the second half of their life.
func TestKeeperRenewAsksSoonerForAShortKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pub, priv := testKeypair(t)
		short := issueFor(t, priv, "pro", 16*time.Hour)
		next := issueFor(t, priv, "pro", 40*time.Hour)

		start := time.Now()
		var asked []time.Duration
		refreshClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			asked = append(asked, time.Since(start))
			key := next
			if len(asked) == 1 {
				key = short
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(key))}, nil
		})
		t.Cleanup(func() { refreshClient.Transport = nil })

		k := NewKeeper("myapp", pub, "")
		if _, err := k.Set(short); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			k.Renew(ctx, "https://keys.example.com/refresh", nil, nil)
		}()
		time.Sleep(25 * time.Hour)
		cancel()
		<-done

		if want := []time.Duration{0, 8 * time.Hour, 24 * time.Hour}; !slices.Equal(asked, want) {
			t.Fatalf("asked at %v, want %v", asked, want)
		}
		if !k.Valid() || k.Key() != next {
			t.Fatal("the short key ran out at 16 hours, the next one must be held by now")
		}
	})
}

// Half of what is left gets small at the end of a key that nobody renews.
func TestKeeperRenewDoesNotHammerAtTheEndOfAKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pub, priv := testKeypair(t)
		ending := issueFor(t, priv, "pro", 3*time.Minute)

		start := time.Now()
		var asked []time.Duration
		refreshClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			asked = append(asked, time.Since(start))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(ending))}, nil
		})
		t.Cleanup(func() { refreshClient.Transport = nil })

		k := NewKeeper("myapp", pub, "")
		if _, err := k.Set(ending); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			k.Renew(ctx, "https://keys.example.com/refresh", nil, nil)
		}()
		time.Sleep(time.Hour)
		cancel()
		<-done

		// Never closer than a minute, and back to once a day when the key is over.
		want := []time.Duration{0, 90 * time.Second, 150 * time.Second, 210 * time.Second}
		if !slices.Equal(asked, want) {
			t.Fatalf("asked at %v, want %v", asked, want)
		}
	})
}

func TestKeeperRenewWithoutAKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		refreshClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			t.Error("a keeper without a key has nothing to ask for")
			return nil, errors.New("unexpected request")
		})
		t.Cleanup(func() { refreshClient.Transport = nil })

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			NewKeeper("myapp", "", "").Renew(ctx, "https://keys.example.com/refresh", nil, nil)
		}()
		time.Sleep(49 * time.Hour)
		cancel()
		<-done
	})
}
