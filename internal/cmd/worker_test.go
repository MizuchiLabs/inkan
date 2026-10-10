package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// An export without a mail leaves the file as the repository has it, so a
// clone stays clean.
func TestTemplateModule(t *testing.T) {
	t.Parallel()
	stub, err := os.ReadFile("../../worker/src/template.ts")
	require.NoError(t, err)
	module, err := templateModule("")
	require.NoError(t, err)
	assert.Equal(t, string(stub), module)

	module, err = templateModule("<p>{{key}}</p>\n</script>")
	require.NoError(t, err)
	assert.Contains(t, module, `export const template =
	"<p>{{key}}</p>\n</script>";`)
}
