package worker

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mizuchilabs/inkan/license"
)

var update = flag.Bool("update", false, "rewrite testdata/mails.json")

type mailCase struct {
	Name    string `json:"name"`
	Key     string `json:"key"`
	App     string `json:"app"`
	Plan    string `json:"plan"`
	Email   string `json:"email"`
	Expires int64  `json:"exp"`
	Brand   struct {
		Name    string `json:"name"`
		Logo    string `json:"logo"`
		Accent  string `json:"accent"`
		Footer  string `json:"footer"`
		Company string `json:"company"`
	} `json:"brand"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

// The cases are shared with the Cloudflare worker, which has to send the very
// same mail. After a deliberate change of the built in mail run it once with
// -update.
func TestMailCases(t *testing.T) {
	t.Parallel()
	const key = "APP-AXsid-iI6MS-wiYXB-wIjoi-QVBQI-iwicG-xhbiI-6InBy-byIsI-mVtYW-lsIjo-ib3Bz-QGFjb" +
		"-WUuaW-8iLCJ-leHAi-OjE4M-DAwMD-AwMDA-sImlk-Ijoic-3ViXz-EifUd-+/Kcx-1mPQw-Zr4+T-9aXbN"
	cases := []mailCase{
		{Name: "no brand", Key: key, App: "APP", Plan: "pro", Email: "ops@acme.io", Expires: periodEnd},
		{Name: "every setting", Key: key, App: "APP", Plan: "team", Email: "ops@acme.io", Expires: periodEnd},
		{Name: "pale accent and no end", Key: key, App: "APP", Plan: "lifetime", Email: "ops@acme.io"},
		{Name: "nothing gets in that could harm", Key: key, App: "APP", Plan: "<i>pro</i>", Email: "a+b@acme.io"},
	}
	full := &cases[1].Brand
	full.Name = "Acme App"
	full.Logo = "https://acme.io/logo.png?size=64&theme=dark"
	full.Accent = "#0e8a8f"
	full.Footer = "Questions about your license? Write to support@acme.io."
	full.Company = "Acme GmbH, Musterstraße 1, 12345 Berlin, HRB 12345. https://acme.io/legal/"
	pale := &cases[2].Brand
	pale.Name = "Acme App"
	pale.Accent = "#FACC15"
	pale.Footer = "Help (https://acme.io/help), or mail help@acme.io!"
	hostile := &cases[3].Brand
	hostile.Name = `Acme <b>"Pro"</b> & 'Co'`
	hostile.Logo = "javascript:alert(1)"
	hostile.Accent = "red; background: url(https://evil.io)"
	hostile.Footer = `<script>alert(1)</script> "x" https://acme.io/?a=1&b=<2>`
	hostile.Company = "Acme & Sons <legal@acme.io>"

	for i := range cases {
		c := &cases[i]
		claims := license.Claims{App: c.App, Plan: c.Plan, Email: c.Email, ExpiresAt: c.Expires}
		var err error
		c.Subject, c.Text, c.HTML, err = defaultTemplates.Render(c.Key, claims, Brand(c.Brand))
		require.NoError(t, err, c.Name)
	}
	built, err := json.Marshal(cases, jsontext.WithIndent("  "))
	require.NoError(t, err)
	built = append(built, '\n')

	const path = "testdata/mails.json"
	if *update {
		require.NoError(t, os.WriteFile(path, built, 0o600))
	}
	stored, err := os.ReadFile(path)
	require.NoError(t, err)
	if string(stored) != string(built) {
		t.Fatal("testdata/mails.json no longer matches the built in mail, see the comment on this test")
	}

	bad := cases[3].HTML
	assert.NotContains(t, bad, "<b>")
	assert.NotContains(t, bad, "<i>")
	assert.NotContains(t, bad, "<script>")
	assert.NotContains(t, bad, "javascript:")
	assert.NotContains(t, bad, "evil.io")
	assert.Contains(t, bad, defaultAccent)
}
