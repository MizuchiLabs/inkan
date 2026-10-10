package cmd

import (
	"archive/tar"
	"bytes"
	"encoding/json/v2"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mizuchilabs/inkan/internal/db"
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

// worker.json names what worker.env sets, nothing else and nothing less.
func TestEnvJSON(t *testing.T) {
	t.Parallel()
	p := &db.Product{
		Prefix: "MYAPP", PrivateKey: "key", WorkerToken: "token", Plan: "pro", GraceDays: 14,
		Name: "My App", MailFooter: `Write to "us" #1`,
	}
	out, err := envJSON(p)
	require.NoError(t, err)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, `Write to "us" #1`, got["INKAN_MAIL_FOOTER"], "a value is not quoted for an env file")
	assert.NotContains(t, got, "INKAN_MAIL_LOGO", "an empty setting is left out")
	assert.Equal(t, "0", got["INKAN_MAX_KEY_DAYS"])

	set := regexp.MustCompile(`(?m)^([A-Z_]+)=`).FindAllStringSubmatch(envFile(p), -1)
	var names []string
	for _, m := range set {
		names = append(names, m[1])
	}
	assert.ElementsMatch(t, slices.Collect(maps.Keys(got)), names)
}

func TestWriteTar(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	files := []exported{
		{"worker.env", 0o600, "INKAN_PREFIX=X\n"},
		{"src/template.ts", 0o644, "export const template = '';\n"},
	}
	require.NoError(t, writeTar(&buf, files))

	tr := tar.NewReader(&buf)
	for _, f := range files {
		h, err := tr.Next()
		require.NoError(t, err)
		assert.Equal(t, f.name, h.Name)
		assert.Equal(t, int64(f.mode), h.Mode, "the mode travels with the file")
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		assert.Equal(t, f.body, string(body))
	}
	_, err := tr.Next()
	require.ErrorIs(t, err, io.EOF)
}
