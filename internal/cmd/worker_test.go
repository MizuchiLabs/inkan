package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvValue(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]string{
		"":                               "",
		"pro":                            "pro",
		"prod_Abc123":                    "prod_Abc123",
		"keys@example.com":               "keys@example.com",
		"#2f3e8f":                        "'#2f3e8f'",
		"Acme <keys@acme.io>":            "'Acme <keys@acme.io>'",
		"Example GmbH, Suite #5":         "'Example GmbH, Suite #5'",
		"Don't wait, write to us":        `"Don't wait, write to us"`,
		`Don't call it "the" key`:        "`Don't call it \"the\" key`",
		"two\r\nlines":                   "'two lines'",
		"Don't `quote` \"everything\" #": "'Dont `quote` \"everything\" #'",
	} {
		assert.Equal(t, want, envValue(value), value)
	}
}
