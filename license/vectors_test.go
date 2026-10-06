package license

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/vectors.json")

// vectors is testdata/vectors.json, the test cases for verifiers and signers
// in other languages. FORMAT.md describes it.
type vectors struct {
	Seed      string         `json:"seed"`
	PublicKey string         `json:"public_key"`
	Now       int64          `json:"now"`
	Verify    []verifyVector `json:"verify"`
	Sign      []signVector   `json:"sign"`
	Renew     []renewVector  `json:"renew"`
}

type verifyVector struct {
	Name   string  `json:"name"`
	Key    string  `json:"key"`
	App    string  `json:"app"`
	Result string  `json:"result"`
	Claims *Claims `json:"claims"`
}

type signVector struct {
	Name    string `json:"name"`
	App     string `json:"app"`
	Claims  Claims `json:"claims"`
	Payload string `json:"payload"`
	Key     string `json:"key"`
}

// renewVector is one case of when a keeper refreshes next, in seconds.
type renewVector struct {
	Name     string `json:"name"`
	Exp      int64  `json:"exp"`
	Failures int    `json:"failures"`
	Wait     int64  `json:"wait"`
}

const (
	vectorsNow = 1_790_000_000
	future     = 4_102_444_800
	past       = 1_000_000_000
)

func resultOf(err error) string {
	switch {
	case err == nil:
		return "valid"
	case errors.Is(err, ErrExpired):
		return "expired"
	case errors.Is(err, ErrInvalidSig):
		return "signature"
	case errors.Is(err, ErrAppMismatch):
		return "app"
	case errors.Is(err, ErrInvalidVersion):
		return "version"
	default:
		return "format"
	}
}

// raw builds a key around any payload, for the cases Issue never produces.
func raw(wire byte, payload string, priv ed25519.PrivateKey) string {
	data := append([]byte{wire}, payload...)
	data = append(data, ed25519.Sign(priv, []byte(payload))...)
	return "MYAPP-" + chunkKey(base64.RawStdEncoding.EncodeToString(data))
}

func buildVectors(t *testing.T) vectors {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub, _ := priv.Public().(ed25519.PublicKey)
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))

	issue := func(app string, c Claims, with ed25519.PrivateKey) string {
		key, err := Issue(app, c, with)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	plain := Claims{Plan: "pro", Email: "ops@acme.io", ExpiresAt: future, ID: "sub_123"}
	good := issue("MYAPP", plain, priv)
	tampered := strings.Replace(good, "wiYXB", "wiYXC", 1)

	v := vectors{
		Seed:      hex.EncodeToString(seed),
		PublicKey: hex.EncodeToString(pub),
		Now:       vectorsNow,
	}
	for _, c := range []struct{ name, key, app string }{
		{"a key with an expiry", good, "MYAPP"},
		{"a key that never expires", issue("MYAPP", Claims{Plan: "pro", Email: "ops@acme.io"}, priv), "MYAPP"},
		{
			"a key with meta",
			issue("MYAPP", Claims{Plan: "team", ID: "lic_abc", Meta: map[string]string{"seats": "5", "tier": "gold"}}, priv),
			"MYAPP",
		},
		{"a key that ran out", issue("MYAPP", Claims{Plan: "pro", ExpiresAt: past, ID: "sub_123"}, priv), "MYAPP"},
		{"wrapped by a mail client and quoted", "\"" + good[:40] + "\r\n " + good[40:90] + "\n" + good[90:] + "\" ", "MYAPP"},
		{"a lowercase app has an uppercase prefix", issue("myapp", plain, priv), "myapp"},
		{"the app is compared exactly", issue("myapp", plain, priv), "MYAPP"},
		{"a claim this version does not know", raw(1, `{"v":1,"app":"MYAPP","plan":"pro","email":"","exp":0,"new":true}`, priv), "MYAPP"},
		{"one changed character", tampered, "MYAPP"},
		{"signed with another key", issue("MYAPP", plain, other), "MYAPP"},
		{"the key of another product", issue("OTHER", plain, priv), "MYAPP"},
		{"another app behind the right prefix", raw(1, `{"v":1,"app":"OTHER","plan":"pro","email":"","exp":0}`, priv), "MYAPP"},
		{"a claims version from the future", raw(1, `{"v":2,"app":"MYAPP","plan":"pro","email":"","exp":0}`, priv), "MYAPP"},
		{"a wire version from the future", raw(2, `{"v":1,"app":"MYAPP","plan":"pro","email":"","exp":0}`, priv), "MYAPP"},
		{"cut off", good[:len(good)-30], "MYAPP"},
		{"too short to hold a signature", "MYAPP-AXsid-iI6MS", "MYAPP"},
		{"not base64", "MYAPP-!!!!!-?????", "MYAPP"},
		{"empty", "", "MYAPP"},
	} {
		claims, err := VerifyWith(c.key, c.app, pub)
		v.Verify = append(v.Verify, verifyVector{c.name, c.key, c.app, resultOf(err), claims})
	}

	for _, c := range []struct {
		name   string
		app    string
		claims Claims
	}{
		{"the plain case", "MYAPP", plain},
		{"no id and no expiry", "MYAPP", Claims{Plan: "pro", Email: "ops@acme.io"}},
		{"meta keys are sorted", "MYAPP", Claims{Plan: "team", ID: "cs_1", Meta: map[string]string{"tier": "gold", "seats": "5", "Zone": "eu"}}},
		{"html characters are escaped", "MYAPP", Claims{Plan: "a<b>&c", Email: "Ops <ops@acme.io>"}},
		{"quotes, backslashes and control characters", "MYAPP", Claims{Plan: "a\"b\\c", Email: "\x00\x01\b\f\n\r\t\x1f\x7f"}},
		{"text outside ascii stays as it is", "MYAPP", Claims{Plan: "プロ", Email: "jürgen@müller.de", Meta: map[string]string{"emoji": "🔑"}}},
		{"line separators are escaped", "MYAPP", Claims{Plan: "a b c"}},
		{"a lowercase app", "myapp", plain},
	} {
		key := issue(c.app, c.claims, priv)
		enc := strings.ReplaceAll(strings.TrimPrefix(key, strings.ToUpper(c.app)+"-"), "-", "")
		data, err := base64.RawStdEncoding.DecodeString(enc)
		if err != nil {
			t.Fatal(err)
		}
		claims := c.claims
		claims.App, claims.Version = c.app, wireVersion
		payload := string(data[1 : len(data)-ed25519.SignatureSize])
		v.Sign = append(v.Sign, signVector{c.name, c.app, claims, payload, key})
	}

	const hour, day = 3600, 86400
	for _, c := range []struct {
		name     string
		exp      int64
		failures int
	}{
		{"a long key is asked about once a day", vectorsNow + 30*day, 0},
		{"a key that never expires too", 0, 0},
		{"two days left are still a day", vectorsNow + 2*day, 0},
		{"a short key at half of what it has left", vectorsNow + 16*hour, 0},
		{"two minutes left are one", vectorsNow + 120, 0},
		{"never under a minute", vectorsNow + 30, 0},
		{"an expired key once a day again", vectorsNow - hour, 0},
		{"a minute after the first failed try", vectorsNow + 30*day, 1},
		{"twice as long after every further one", vectorsNow + 30*day, 4},
		{"a failed try does not look at the key", vectorsNow + 30, 2},
		{"up to a day", vectorsNow + 30*day, 12},
		{"and no longer", vectorsNow + 30*day, 500},
	} {
		wait := renewWait(c.exp, time.Unix(vectorsNow, 0), c.failures)
		v.Renew = append(v.Renew, renewVector{c.name, c.exp, c.failures, int64(wait / time.Second)})
	}
	return v
}

// The file must never change by accident, other languages test against it.
// After a deliberate change run it once with -update.
func TestVectors(t *testing.T) {
	built := buildVectors(t)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(built); err != nil {
		t.Fatal(err)
	}
	const path = "testdata/vectors.json"
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, buf.Bytes()) {
		t.Fatal("testdata/vectors.json no longer matches what this package signs and verifies")
	}

	var v vectors
	if err := json.Unmarshal(stored, &v); err != nil {
		t.Fatal(err)
	}
	pub, err := hex.DecodeString(v.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]bool{}
	for _, c := range v.Verify {
		claims, err := VerifyWith(c.Key, c.App, pub)
		if got := resultOf(err); got != c.Result {
			t.Errorf("%s: got %s, want %s", c.Name, got, c.Result)
		}
		if !reflect.DeepEqual(claims, c.Claims) {
			t.Errorf("%s: got claims %+v, want %+v", c.Name, claims, c.Claims)
		}
		results[c.Result] = true
	}
	for _, want := range []string{"valid", "expired", "signature", "app", "version", "format"} {
		if !results[want] {
			t.Errorf("no vector ends as %s", want)
		}
	}
	for _, c := range v.Renew {
		got := renewWait(c.Exp, time.Unix(v.Now, 0), c.Failures)
		if got != time.Duration(c.Wait)*time.Second {
			t.Errorf("renew, %s: got %s, want %d seconds", c.Name, got, c.Wait)
		}
	}
}
