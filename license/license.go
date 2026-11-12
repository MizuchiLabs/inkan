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
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	wireVersion = 1
	chunkSize   = 5
	// The largest whole number every language reads the same, 2^53 - 1.
	maxExp = 1<<53 - 1
)

var (
	ErrInvalidFormat = errors.New("license format is invalid")
	ErrInvalidSig    = errors.New("signature verification failed")
	ErrExpired       = errors.New("license has expired")
	ErrAppMismatch   = errors.New("license issued for a different application")
	ErrBound         = errors.New("license is bound to another machine")
	ErrNoKey         = errors.New("no license key set")
	ErrNoPublicKey   = errors.New("no license public key")
)

type Claims struct {
	App       string `json:"app"` // Must equal the app passed to Issue/Verify (case-sensitive)
	Plan      string `json:"plan"`
	Email     string `json:"email"`
	ExpiresAt int64  `json:"exp"` // Unix timestamp, 0 = perpetual
	// Stable id of the license, e.g. the billing subscription id. Omitted
	// when empty, so keys issued without it keep their exact bytes.
	ID string `json:"id,omitempty"`
	// The machine the key is bound to, as the app names it. Empty binds to
	// nothing. [Verify] compares it with the fingerprint it is given.
	Fingerprint string `json:"fp,omitempty"`
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
// key prefix, e.g. Issue("myapp", ...) emits "MYAPP-XXXXX-...". Only a to z
// are uppercased.
func Issue(app string, claims Claims, priv ed25519.PrivateKey) (string, error) {
	if app == "" {
		return "", errors.New("app name is required")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("invalid ed25519 private key")
	}
	claims.App = app

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
	return prefixOf(app) + chunkKey(encoded), nil
}

// prefixOf uppercases a to z only. Languages disagree on the rest, like ß.
func prefixOf(app string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}, app) + "-"
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

// Verify extracts and validates claims against the public key of the app.
//
// app must match the signed Claims.App exactly (case-sensitive), and the
// key must carry the matching uppercase prefix, e.g. a key issued with
// Issue("myapp", ...) verifies with Verify(key, "myapp", publicKeys).
//
// fingerprint is optional and names this machine, in whatever way the app
// picks, like a hash of /etc/machine-id or an install id it keeps in its data
// dir. A key bound to another machine returns its claims alongside
// [ErrBound]. A key that is bound to nothing verifies on every machine. An
// app that does not bind keys leaves it out, then nothing is compared. Only
// the first one counts.
//
// An expired key returns the parsed claims alongside ErrExpired, so
// callers can show plan or email details in expiry messages.
//
// Whitespace anywhere in rawKey and quotes around it are ignored, see [Clean].
//
// The payload is read strictly, so that every verifier agrees on every key.
// Claim names match exactly and the last of two equal names counts.
//
// publicKeys is the hex public key of the app. It is no secret and can live
// in the code:
//
//	const publicKey = "03a107bf..."
//
// An app whose source is open can leave it out there, so that only its own
// builds take keys, and set a variable at build time instead:
//
//	var publicKey string // go build -ldflags "-X main.publicKey=<hex>"
//
// Without a public key the error is [ErrNoPublicKey]. Several
// comma-separated keys may be given, a license signed by any of them
// verifies. That is how a signing key is rotated: ship the new key next to
// the old one, then drop the old one once its licenses have run out.
func Verify(rawKey, app, publicKeys string, fingerprint ...string) (*Claims, error) {
	pubs, err := decodePubKeys(publicKeys)
	if err != nil {
		return nil, err
	}
	return VerifyWith(rawKey, app, first(fingerprint), pubs...)
}

func first(fingerprint []string) string {
	if len(fingerprint) == 0 {
		return ""
	}
	return fingerprint[0]
}

// VerifyWith is Verify against public keys that are already decoded, for
// issuers that hold the keys of several apps.
func VerifyWith(rawKey, app, fingerprint string, pubs ...ed25519.PublicKey) (*Claims, error) {
	enc, ok := strings.CutPrefix(Clean(rawKey), prefixOf(app))
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

	c, ok := parseClaims(payload)
	if !ok {
		return nil, ErrInvalidFormat
	}

	if c.App != app {
		return nil, ErrAppMismatch
	}
	if fingerprint != "" && c.Fingerprint != "" && c.Fingerprint != fingerprint {
		return c, ErrBound
	}
	if c.IsExpired() {
		return c, ErrExpired
	}

	return c, nil
}

// parseClaims reads a payload by the rules of FORMAT.md. encoding/json alone
// is too forgiving for that: it matches names without case, takes null for an
// object and patches up broken text.
func parseClaims(payload []byte) (*Claims, bool) {
	if !utf8.Valid(payload) || !bytes.HasPrefix(bytes.TrimLeft(payload, " \t\r\n"), []byte("{")) {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || loneSurrogate(payload) {
		return nil, false
	}

	var c Claims
	for name, into := range map[string]*string{
		"app": &c.App, "plan": &c.Plan, "email": &c.Email, "id": &c.ID, "fp": &c.Fingerprint,
	} {
		if !parseString(fields[name], into) {
			return nil, false
		}
	}
	if !parseExp(fields["exp"], &c.ExpiresAt) {
		return nil, false
	}

	var meta map[string]json.RawMessage
	if raw, ok := fields["meta"]; ok && json.Unmarshal(raw, &meta) != nil {
		return nil, false
	}
	for name, raw := range meta {
		var value string
		if isNull(raw) || !parseString(raw, &value) {
			return nil, false
		}
		if c.Meta == nil {
			c.Meta = make(map[string]string, len(meta))
		}
		c.Meta[name] = value
	}
	return &c, true
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// A missing or null claim keeps its zero value.
func parseString(raw json.RawMessage, into *string) bool {
	if isNull(raw) {
		return true
	}
	return raw[0] == '"' && json.Unmarshal(raw, into) == nil
}

// exp is judged by its value as a float, the only way JavaScript can read
// it. So 4.1e9 is fine, 1.5 and anything from 2^53 on are not.
func parseExp(raw json.RawMessage, into *int64) bool {
	if isNull(raw) {
		return true
	}
	if raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') {
		return false
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) > maxExp {
		return false
	}
	*into = int64(f)
	return true
}

// loneSurrogate finds a \u escape that is half of a surrogate pair. Go would
// turn it into U+FFFD, JavaScript and Python keep it. payload is valid JSON,
// so a backslash only stands in a string and every \u has its four digits.
func loneSurrogate(payload []byte) bool {
	escaped := func(at int) (rune, bool) {
		if at+6 > len(payload) || payload[at] != '\\' || payload[at+1] != 'u' {
			return 0, false
		}
		r, err := strconv.ParseUint(string(payload[at+2:at+6]), 16, 16)
		return rune(r), err == nil
	}
	for i := 0; i < len(payload); i++ {
		if payload[i] != '\\' {
			continue
		}
		r, ok := escaped(i)
		if !ok {
			i++
			continue
		}
		i += 5
		if !utf16.IsSurrogate(r) {
			continue
		}
		low, ok := escaped(i + 1)
		if !ok || utf16.DecodeRune(r, low) == unicode.ReplacementChar {
			return true
		}
		i += 6
	}
	return false
}

// Clean strips what a key picks up on its way to an app: the line breaks a
// mail client adds, stray spaces, the quotes of an env file. [Verify] does it
// by itself, Clean is for whoever stores or compares keys.
//
// It drops every character Unicode calls White_Space, the zero width space
// U+200B and the byte order mark U+FEFF, then the quotes " and ' at both ends.
func Clean(rawKey string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '\u200b' || r == '\ufeff' {
			return -1
		}
		return r
	}, rawKey), `"'`)
}

// One malformed entry rejects the whole list, a typo must not silently
// drop a key.
func decodePubKeys(pubHex string) ([]ed25519.PublicKey, error) {
	pubHex = strings.TrimSpace(pubHex)
	if pubHex == "" {
		return nil, ErrNoPublicKey
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
