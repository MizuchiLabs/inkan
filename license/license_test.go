package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// testKeypair returns the public key the way an app carries it, as hex.
func testKeypair(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(pub), priv
}

func TestIssueVerifyRoundtrip(t *testing.T) {
	pub, priv := testKeypair(t)

	key, err := Issue("myapp", Claims{Plan: "pro", Email: "a@b.c"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "MYAPP-") {
		t.Fatalf("prefix missing: %s", key)
	}

	c, err := Verify(key, "myapp", pub)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.App != "myapp" || c.Plan != "pro" || c.Email != "a@b.c" || c.Version != wireVersion {
		t.Fatalf("unexpected claims: %+v", c)
	}
}

func TestIssueIDAndMeta(t *testing.T) {
	pub, priv := testKeypair(t)

	claims := Claims{Plan: "pro", ID: "sub_123", Meta: map[string]string{"seats": "25", "org": "Acme"}}
	key, err := Issue("myapp", claims, priv)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(key, "myapp", pub)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.ID != "sub_123" || c.Meta["seats"] != "25" || c.Meta["org"] != "Acme" {
		t.Fatalf("optional claims lost: %+v", c)
	}

	// Map order must not leak into the key, re-issuing has to be byte-identical.
	again, err := Issue("myapp", claims, priv)
	if err != nil {
		t.Fatal(err)
	}
	if again != key {
		t.Fatal("same claims produced a different key")
	}
}

// Keys are deterministic, so a key without id or meta must keep the payload
// it had before those claims existed.
func TestClaimsWithoutOptionalsKeepLegacyBytes(t *testing.T) {
	payload, err := json.Marshal(Claims{Version: 1, App: "myapp", Plan: "pro", Email: "a@b.c", ExpiresAt: 42})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"app":"myapp","plan":"pro","email":"a@b.c","exp":42}`
	if string(payload) != want {
		t.Fatalf("payload changed:\n got %s\nwant %s", payload, want)
	}
}

func TestVerifyKeyList(t *testing.T) {
	oldPub, oldPriv := testKeypair(t)
	newPub, newPriv := testKeypair(t)
	_, otherPriv := testKeypair(t)
	list := newPub + ", " + oldPub

	for name, priv := range map[string]ed25519.PrivateKey{"old": oldPriv, "new": newPriv} {
		key, err := Issue("myapp", Claims{Plan: "pro"}, priv)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(key, "myapp", list); err != nil {
			t.Fatalf("%s key must verify against the list: %v", name, err)
		}
	}

	key, err := Issue("myapp", Claims{Plan: "pro"}, otherPriv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(key, "myapp", list); !errors.Is(err, ErrInvalidSig) {
		t.Fatalf("key outside the list: got %v", err)
	}
}

// A build without a public key, or with a broken one, takes no key at all.
func TestVerifyWithoutAPublicKey(t *testing.T) {
	pub, priv := testKeypair(t)
	key, err := Issue("myapp", Claims{Plan: "pro"}, priv)
	if err != nil {
		t.Fatal(err)
	}

	for _, none := range []string{"", "  "} {
		if _, err := Verify(key, "myapp", none); !errors.Is(err, ErrNoPublicKey) {
			t.Fatalf("public key %q: got %v, want ErrNoPublicKey", none, err)
		}
	}
	for name, broken := range map[string]string{
		"not hex":            "zzzz",
		"too short":          "ab12",
		"one bad in a list":  pub + ",zzzz",
		"an empty list part": pub + ",",
	} {
		if _, err := Verify(key, "myapp", broken); err == nil || errors.Is(err, ErrNoPublicKey) {
			t.Fatalf("%s: got %v, want an error about the public key", name, err)
		}
	}
}

func TestVerifyWith(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	key, err := Issue("myapp", Claims{Plan: "pro"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWith(key, "myapp", pub); err != nil {
		t.Fatalf("given key must verify: %v", err)
	}
	if _, err := VerifyWith(key, "myapp", otherPub); !errors.Is(err, ErrInvalidSig) {
		t.Fatalf("wrong key: got %v", err)
	}
	if _, err := VerifyWith(key, "myapp"); !errors.Is(err, ErrInvalidSig) {
		t.Fatalf("no keys: got %v", err)
	}
}

func TestIssueStampsAppAndVersion(t *testing.T) {
	pub, priv := testKeypair(t)

	key, err := Issue("myapp", Claims{App: "otherapp", Plan: "pro"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	// Issue must overwrite caller-supplied App so the key verifies.
	if _, err := Verify(key, "myapp", pub); err != nil {
		t.Fatalf("stamped App ignored: %v", err)
	}
}

func TestIssueEmptyApp(t *testing.T) {
	_, priv := testKeypair(t)
	if _, err := Issue("", Claims{Plan: "pro"}, priv); err == nil {
		t.Fatal("empty app should error")
	}
}

func TestVerifyAppBinding(t *testing.T) {
	pub, priv := testKeypair(t)

	key, err := Issue("myapp", Claims{Plan: "pro"}, priv)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Verify(key, "otherapp", pub); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("different app prefix: got %v", err)
	}
	if _, err := Verify(key, "MYAPP", pub); !errors.Is(err, ErrAppMismatch) {
		t.Fatalf("casing must matter: got %v", err)
	}
}

func TestVerifyExpiry(t *testing.T) {
	pub, priv := testKeypair(t)

	past := time.Now().UTC().Add(-time.Hour).Unix()
	key, err := Issue("myapp", Claims{Plan: "pro", ExpiresAt: past}, priv)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(key, "myapp", pub)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expired key: got %v", err)
	}
	if c == nil || c.Plan != "pro" {
		t.Fatalf("expired key must return claims: %+v", c)
	}

	future := time.Now().UTC().Add(time.Hour).Unix()
	key, err = Issue("myapp", Claims{Plan: "pro", ExpiresAt: future}, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(key, "myapp", pub); err != nil {
		t.Fatalf("unexpired key: %v", err)
	}
}

// Keys travel by mail and through env files, and come out wrapped or quoted.
func TestVerifyMangledKey(t *testing.T) {
	pub, priv := testKeypair(t)
	key, err := Issue("myapp", Claims{Plan: "pro", Email: "ops@acme.io", ID: "sub_123"}, priv)
	if err != nil {
		t.Fatal(err)
	}

	for name, mangled := range map[string]string{
		"wrapped by a mail client": key[:76] + "\r\n" + key[76:152] + "\n  " + key[152:],
		"quoted in an env file":    `"` + key + `"`,
		"single quoted":            " '" + key + "'\n",
		"tabs and no-break spaces": "\t" + key[:20] + "\u00a0" + key[20:],
	} {
		if _, err := Verify(mangled, "myapp", pub); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if got := Clean(mangled); got != key {
			t.Errorf("%s: cleaned to %q, want the key as issued", name, got)
		}
	}

	if _, err := Verify(key[:40]+"x"+key[40:], "myapp", pub); err == nil {
		t.Fatal("an extra character inside the key must not verify")
	}
}

func TestParsePrivateKey(t *testing.T) {
	privB64, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParsePrivateKey(privB64)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pub := hex.EncodeToString([]byte(priv.Public().(ed25519.PublicKey)))

	key, err := Issue("myapp", Claims{Plan: "pro"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(key, "myapp", pub); err != nil {
		t.Fatalf("parsed key must sign valid keys: %v", err)
	}

	if _, err := ParsePrivateKey("not-base64!!"); err == nil {
		t.Fatal("garbage should error")
	}
	if _, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("wrong length should error")
	}
}

func TestVerifyTampered(t *testing.T) {
	pub, priv := testKeypair(t)

	key, err := Issue("myapp", Claims{Plan: "pro"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := strings.CutPrefix(key, "MYAPP-")
	clean := strings.ReplaceAll(enc, "-", "")
	raw, err := base64.RawStdEncoding.DecodeString(clean)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0x01

	tampered := "MYAPP-" + chunkKey(base64.RawStdEncoding.EncodeToString(raw))
	if _, err := Verify(tampered, "myapp", pub); !errors.Is(err, ErrInvalidSig) {
		t.Fatalf("tampered key: got %v", err)
	}
}

func TestVerifyWrongWireVersion(t *testing.T) {
	pub, priv := testKeypair(t)

	key, err := Issue("myapp", Claims{Plan: "pro"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := strings.CutPrefix(key, "MYAPP-")
	clean := strings.ReplaceAll(enc, "-", "")
	raw, err := base64.RawStdEncoding.DecodeString(clean)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = wireVersion + 1

	bumped := "MYAPP-" + chunkKey(base64.RawStdEncoding.EncodeToString(raw))
	if _, err := Verify(bumped, "myapp", pub); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("wrong wire version: got %v", err)
	}
}

func TestGenerateKey(t *testing.T) {
	privB64, pubHex, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		t.Fatalf("private key: len=%d err=%v", len(priv), err)
	}
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("public key: len=%d err=%v", len(pub), err)
	}
	if !ed25519.PublicKey(pub).Equal(ed25519.PrivateKey(priv).Public()) {
		t.Fatal("keypair halves do not match")
	}
}

// A fixed key and fixed claims must give this exact license key, forever.
// It is the test vector for signers in other languages, the README shows it.
func TestKeyBytesArePinned(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	key, err := Issue("MYAPP", Claims{Plan: "pro", Email: "ops@acme.io", ExpiresAt: 1800000000, ID: "sub_123"}, priv)
	if err != nil {
		t.Fatal(err)
	}
	const want = "MYAPP-AXsid-iI6MS-wiYXB-wIjoi-TVlBU-FAiLC-JwbGF-uIjoi-cHJvI-iwiZW-1haWw-iOiJv-cHNAY-WNtZS-5pbyI-" +
		"sImV4-cCI6M-TgwMD-AwMDA-wMCwi-aWQiO-iJzdW-JfMTI-zIn3Q-DEmCB-YC3y4-iSznb-oo7x7-yqfYu-BHBfj-RU/BW-" +
		"xFysA-6WlSu-09lvw-15++b-xP4DY-wLH1H-fl2wS-OhmTO-e0RyC-JdsM"
	if key != want {
		t.Fatalf("the key format changed:\n got %s\nwant %s", key, want)
	}
}
