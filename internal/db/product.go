package db

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"

	"github.com/mizuchilabs/inkan/license"
)

// PublicKey is the hex public half of the signing key, the one a product builds in.
func (p *Product) PublicKey() (string, error) {
	priv, err := license.ParsePrivateKey(p.PrivateKey)
	if err != nil {
		return "", err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	return hex.EncodeToString(pub), nil
}

// OldKeys are the hex public keys from before a rotation, newest first.
func (p *Product) OldKeys() []string {
	if p.OldPublicKeys == "" {
		return nil
	}
	return strings.Split(p.OldPublicKeys, ",")
}
