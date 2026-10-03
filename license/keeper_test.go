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
	_, priv := testKeypair(t)
	pro := issueFor(t, priv, "pro", time.Hour)
	team := issueFor(t, priv, "team", time.Hour)
	expired := issueFor(t, priv, "pro", -time.Hour)

	k := NewKeeper("myapp")
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

func TestKeeperClaimsAreACopy(t *testing.T) {
	_, priv := testKeypair(t)
	key, err := Issue("myapp", Claims{Plan: "pro", Meta: map[string]string{"seats": "5"}}, priv)
	if err != nil {
		t.Fatal(err)
	}
	k := NewKeeper("myapp")
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
		_, priv := testKeypair(t)
		k := NewKeeper("myapp")
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
		_, priv := testKeypair(t)
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

		k := NewKeeper("myapp")
		if _, err := k.Set(old); !errors.Is(err, ErrExpired) {
			t.Fatalf("the old key: got %v", err)
		}

		var saved []string
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			k.Renew(ctx, "https://keys.example.com/refresh", func(key string) { saved = append(saved, key) })
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
		if c, err := k.Claims(); err != nil || k.Key() != renewed {
			t.Fatalf("the new key must be held: %+v, %v", c, err)
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
			NewKeeper("myapp").Renew(ctx, "https://keys.example.com/refresh", nil)
		}()
		time.Sleep(49 * time.Hour)
		cancel()
		<-done
	})
}
