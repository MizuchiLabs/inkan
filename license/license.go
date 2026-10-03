// Package license issues and verifies Ed25519-signed license keys.
//
// A key looks like MYAPP-eyJ2I-joxLC-... and is built in three steps:
//
//  1. The claims are encoded as JSON. That is the payload.
//  2. The payload is signed with the Ed25519 private key.
//  3. One version byte, the payload and the 64-byte signature are joined,
//     base64 encoded without padding, cut into chunks of five and prefixed
//     with the uppercase app name.
//
// Verify undoes the three steps and checks the signature over the payload
// bytes as they arrived. A signer in any language therefore verifies here,
// whatever its JSON looks like. Only this package promises that the same
// claims always give the same key.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

const (
	wireVersion = 1
	chunkSize   = 5
)

var (
	ErrInvalidFormat  = errors.New("license format is invalid")
	ErrInvalidVersion = errors.New("invalid license version")
	ErrInvalidSig     = errors.New("signature verification failed")
	ErrExpired        = errors.New("license has expired")
	ErrAppMismatch    = errors.New("license issued for a different application")
)

var pubkey string

type Claims struct {
	Version   int    `json:"v"`
	App       string `json:"app"` // Must equal the app passed to Issue/Verify (case-sensitive)
	Plan      string `json:"plan"`
	Email     string `json:"email"`
	ExpiresAt int64  `json:"exp"` // Unix timestamp, 0 = perpetual
	// Stable id of the license, e.g. the billing subscription id. Omitted
	// when empty, so keys issued without it keep their exact bytes.
	ID string `json:"id,omitempty"`
	// App-defined extras such as seats or features. They are signed and
	// never read here. Every byte makes the key longer.
	Meta map[string]string `json:"meta,omitempty"`
}

func (c *Claims) IsExpired() bool {
	if c.ExpiresAt == 0 {
		return false
	}
	return time.Now().Unix() > c.ExpiresAt
}

// ParsePrivateKey decodes the base64 64-byte Ed25519 private key
// (seed + public key) produced by GenerateKey, the format the issuing
// side stores.
func ParsePrivateKey(privateB64 string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(privateB64)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("private key must be the base64 64-byte Ed25519 key from GenerateKey")
	}
	return ed25519.PrivateKey(raw), nil
}

// Issue generates a formatted key using the worker's private key.
//
// app names the license's owner app. With a shared keypair across apps,
// binding is enforced by the signed Claims.App value, which must equal
// app (case-sensitive). The uppercase app name also becomes the readable
// key prefix, e.g. Issue("myapp", ...) emits "MYAPP-XXXXX-...".
func Issue(app string, claims Claims, priv ed25519.PrivateKey) (string, error) {
	if app == "" {
		return "", errors.New("app name is required")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("invalid ed25519 private key")
	}
	claims.App = app
	claims.Version = wireVersion

	// encoding/json v1 on purpose, its escaping is part of the key bytes.
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	sig := ed25519.Sign(priv, payload)

	// [wireVersion (1B)] + [payload (NB)] + [sig (64B)]
	data := make([]byte, 0, 1+len(payload)+len(sig))
	data = append(data, wireVersion)
	data = append(data, payload...)
	data = append(data, sig...)

	encoded := base64.RawStdEncoding.EncodeToString(data)
	return strings.ToUpper(app) + "-" + chunkKey(encoded), nil
}

func chunkKey(encoded string) string {
	var b strings.Builder
	b.Grow(len(encoded) + len(encoded)/chunkSize)
	for i := 0; i < len(encoded); i += chunkSize {
		if i > 0 {
			b.WriteByte('-')
		}
		end := min(i+chunkSize, len(encoded))
		b.WriteString(encoded[i:end])
	}
	return b.String()
}

// Verify extracts and validates claims against a public key.
//
// app must match the signed Claims.App exactly (case-sensitive), and the
// key must carry the matching uppercase prefix, e.g. a key issued with
// Issue("myapp", ...) verifies with Verify(key, "myapp").
//
// An expired key returns the parsed claims alongside ErrExpired, so
// callers can show plan or email details in expiry messages.
//
// The public key is injected at build time via ldflags:
//
//	-X github.com/mizuchilabs/inkan/license.pubkey=<hex>
//
// or at runtime with SetPublicKey. Several comma-separated keys may be
// given, a license signed by any of them verifies. That is how a signing
// key is rotated: ship the new key next to the old one, then drop the old
// one once its licenses have run out.
func Verify(rawKey, app string) (*Claims, error) {
	pubs, err := decodePubKeys(pubkey)
	if err != nil {
		return nil, err
	}
	return VerifyWith(rawKey, app, pubs...)
}

// VerifyWith is Verify against the given public keys instead of the
// injected ones, for issuers that hold the keys of several apps.
func VerifyWith(rawKey, app string, pubs ...ed25519.PublicKey) (*Claims, error) {
	prefix := strings.ToUpper(app) + "-"

	enc, ok := strings.CutPrefix(strings.TrimSpace(rawKey), prefix)
	if !ok {
		return nil, ErrInvalidFormat
	}

	clean := strings.ReplaceAll(enc, "-", "")
	raw, err := base64.RawStdEncoding.DecodeString(clean)
	if err != nil || len(raw) < 1+ed25519.SignatureSize+1 {
		return nil, ErrInvalidFormat
	}

	if raw[0] != wireVersion {
		return nil, ErrInvalidFormat
	}

	payload := raw[1 : len(raw)-ed25519.SignatureSize]
	sig := raw[len(raw)-ed25519.SignatureSize:]

	if !slices.ContainsFunc(pubs, func(pub ed25519.PublicKey) bool {
		return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, payload, sig)
	}) {
		return nil, ErrInvalidSig
	}

	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, ErrInvalidFormat
	}

	if c.App != app {
		return nil, ErrAppMismatch
	}
	if c.Version != wireVersion {
		return nil, ErrInvalidVersion
	}
	if c.IsExpired() {
		return &c, ErrExpired
	}

	return &c, nil
}

// SetPublicKey sets the verification keys at runtime, for tests and
// embedders that do not use ldflags. Keys are hex-encoded and
// comma-separated, the same format the ldflags-injected pubkey var
// expects. An empty string clears them.
func SetPublicKey(pubHex string) error {
	pubHex = strings.TrimSpace(pubHex)
	if pubHex != "" {
		if _, err := decodePubKeys(pubHex); err != nil {
			return err
		}
	}
	pubkey = pubHex
	return nil
}

// HasPublicKey reports whether valid verification keys are available, via
// ldflags injection or SetPublicKey. Verifiers that surface a distinct
// "not configured" state use it before Verify.
func HasPublicKey() bool {
	_, err := decodePubKeys(pubkey)
	return err == nil
}

// One malformed entry rejects the whole list, a typo must not silently
// drop a key.
func decodePubKeys(pubHex string) ([]ed25519.PublicKey, error) {
	pubHex = strings.TrimSpace(pubHex)
	if pubHex == "" {
		return nil, errors.New("no license public key injected")
	}
	var pubs []ed25519.PublicKey
	for part := range strings.SplitSeq(pubHex, ",") {
		b, err := hex.DecodeString(strings.TrimSpace(part))
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, errors.New("invalid ed25519 public key hex")
		}
		pubs = append(pubs, ed25519.PublicKey(b))
	}
	return pubs, nil
}
