package db

import "strings"

// OldKeys are the hex public keys from before a rotation, newest first.
func (p *Product) OldKeys() []string {
	if p.OldPublicKeys == "" {
		return nil
	}
	return strings.Split(p.OldPublicKeys, ",")
}
