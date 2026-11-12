package cmd

import (
	"archive/tar"
	"bytes"
	"encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

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

// The export holds what only the dashboard knows. Every other setting of a
// worker has its home where the worker runs.
func TestWorkerFiles(t *testing.T) {
	t.Parallel()
	p := &db.Product{
		Prefix: "MYAPP", PrivateKey: "key", WorkerToken: "token", Plan: "pro plus", Name: `My "App" #1`,
		OldPublicKeys: "0a,0b",
	}
	files, err := workerFiles(p)
	require.NoError(t, err)
	byName := map[string]exported{}
	for _, f := range files {
		byName[f.name] = f
	}

	want := map[string]string{
		"INKAN_PREFIX":          "MYAPP",
		"INKAN_PRIVATE_KEY":     "key",
		"INKAN_OLD_PUBLIC_KEYS": "0a,0b",
		"INKAN_WORKER_TOKEN":    "token",
		"INKAN_PLAN":            "pro plus",
		"INKAN_PRODUCT_NAME":    `My "App" #1`,
	}
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(byName["worker.keys.json"].body), &got))
	assert.Equal(t, want, got, "a value is not quoted for JSON")

	env := byName["worker.keys.env"]
	assert.Equal(t, os.FileMode(0o600), env.mode, "it holds the signing key")
	set := regexp.MustCompile(`(?m)^([A-Z_]+)=(.*)$`).FindAllStringSubmatch(env.body, -1)
	assert.Len(t, set, len(want), "the env file names the same six")
	assert.Contains(t, env.body, "INKAN_PLAN='pro plus'\n")
	assert.Contains(t, env.body, "INKAN_PRODUCT_NAME='My \"App\" #1'\n")

	p.OldPublicKeys = ""
	assert.NotContains(t, keysEnv(p), "INKAN_OLD_PUBLIC_KEYS", "left out without a rotation")

	assert.Contains(t, byName[envExampleName].body, "INKAN_STRIPE_KEY=")
	assert.NotContains(t, byName, envName, "worker.env is the user's, see TestWriteDir")
	if _, built := byName["cloudflare.js"]; built {
		assert.Contains(t, byName["wrangler.jsonc"].body, `"name": "inkan-myapp"`, "one name per product")
		assert.Contains(t, byName["wrangler.jsonc"].body, `"main": "cloudflare.js"`)
	}
}

// A second export brings new keys and must leave the filled in settings alone.
func TestWriteDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "inkan-worker-myapp")
	files := []exported{
		{"worker.keys.env", 0o600, "INKAN_PREFIX=X\n"},
		{envExampleName, 0o644, "INKAN_STRIPE_KEY=\n"},
	}
	require.NoError(t, writeDir(dir, files))
	env := filepath.Join(dir, envName)
	started, err := os.ReadFile(env)
	require.NoError(t, err)
	assert.Contains(t, string(started), "INKAN_STRIPE_KEY=", "worker.env starts as the example")

	require.NoError(t, os.WriteFile(env, []byte("INKAN_STRIPE_KEY=rk_live_1\n"), 0o600))
	files[0].body = "INKAN_PREFIX=Y\n"
	require.NoError(t, writeDir(dir, files))
	kept, err := os.ReadFile(env)
	require.NoError(t, err)
	assert.Equal(t, "INKAN_STRIPE_KEY=rk_live_1\n", string(kept))
	keys, err := os.ReadFile(filepath.Join(dir, "worker.keys.env"))
	require.NoError(t, err)
	assert.Equal(t, "INKAN_PREFIX=Y\n", string(keys))
	st, err := os.Stat(filepath.Join(dir, "worker.keys.env"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestWriteTar(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	files := []exported{
		{"worker.keys.env", 0o600, "INKAN_PREFIX=X\n"},
		{"node.js", 0o644, "export {};\n"},
	}
	require.NoError(t, writeTar(&buf, "inkan-worker-x", files))

	tr := tar.NewReader(&buf)
	for _, f := range files {
		h, err := tr.Next()
		require.NoError(t, err)
		assert.Equal(t, "inkan-worker-x/"+f.name, h.Name, "unpacking makes the folder")
		assert.Equal(t, int64(f.mode), h.Mode, "the mode travels with the file")
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		assert.Equal(t, f.body, string(body))
	}
	_, err := tr.Next()
	require.ErrorIs(t, err, io.EOF)
}

// A token that leaked can be replaced. Without the flag it stays, or every
// export would cut the deployed worker off.
func TestNewToken(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q, closeDB, err := db.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(closeDB)
	_, err = q.CreateProduct(ctx, &db.CreateProductParams{
		Name: "App", Prefix: "APP", Plan: "pro", PrivateKey: "key", WorkerToken: "the-first-token",
	})
	require.NoError(t, err)

	p, err := productFor(ctx, q, "APP", false)
	require.NoError(t, err)
	assert.Equal(t, "the-first-token", p.WorkerToken)

	p, err = productFor(ctx, q, "APP", true)
	require.NoError(t, err)
	assert.NotEqual(t, "the-first-token", p.WorkerToken)
	assert.GreaterOrEqual(t, len(p.WorkerToken), 32, "a worker turns a shorter token down")
	stored, err := q.GetProductByPrefix(ctx, "APP")
	require.NoError(t, err)
	assert.Equal(t, p.WorkerToken, stored.WorkerToken, "the dashboard reads with the new one from now on")

	_, err = productFor(ctx, q, "NOPE", false)
	require.ErrorContains(t, err, "no product has the prefix NOPE")
}
