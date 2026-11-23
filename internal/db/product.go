package db

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"

	"github.com/mizuchilabs/inkan/license"
)

func (p *Product) PublicKey() (string, error) {
	priv, err := license.ParsePrivateKey(p.PrivateKey)
	if err != nil {
		return "", err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	return hex.EncodeToString(pub), nil
}

func (p *Product) OldKeys() []string {
	if p.OldPublicKeys == "" {
		return []string{}
	}
	return strings.Split(p.OldPublicKeys, ",")
}
