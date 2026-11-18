package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/json/v2"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
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
	files, err := workerFiles(p, "")
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
		assert.NotRegexp(
			t,
			`(?m)^\s*"limits"`,
			byName["wrangler.jsonc"].body,
			"the free plan refuses a deploy with cpu limits",
		)

		files, err = workerFiles(p, "<p>{{key}}</p>")
		require.NoError(t, err, "every built worker has a place for a mail")
		for _, f := range files {
			if f.name == "cloudflare.js" || f.name == "node.js" {
				assert.Contains(t, f.body, `"<p>{{key}}</p>"`, f.name)
			}
		}
	}

	// The export finds the place of the mail by this string, the build keeps it as it is.
	template, err := os.ReadFile("../../worker/src/template.ts")
	require.NoError(t, err)
	assert.Contains(t, string(template), "'"+strings.Trim(mailMark, `"`)+"'")
}

// A mail of the user's own goes into the built worker as it is, whatever it holds.
func TestWithMail(t *testing.T) {
	t.Parallel()
	mail := "<p class=\"key\">{{key}}</p>\n<!-- 'it' \\ </script> \u2028 -->"
	built := "var template = " + mailMark + ";\nexport { template };\n"
	literal := regexp.MustCompile(`var template = (".*");`)

	got, err := withMail(built, mail)
	require.NoError(t, err)
	var sends string
	require.NoError(t, json.Unmarshal([]byte(literal.FindStringSubmatch(got)[1]), &sends))
	assert.Equal(t, mail, sends)
	assert.NotContains(t, got, "\u2028", "old engines end a line there")

	_, err = withMail("export {};\n", mail)
	require.ErrorContains(t, err, "no place for a mail")

	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	for name, body := range map[string]string{"handler.js": built, "package.json": `{"type":"module"}`} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(w, body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	out, err := zipWithMail(zipped.String(), mail)
	require.NoError(t, err)
	zr, err := zip.NewReader(strings.NewReader(out), int64(len(out)))
	require.NoError(t, err)
	handler, err := fs.ReadFile(zr, "handler.js")
	require.NoError(t, err)
	assert.Equal(t, got, string(handler), "the same worker as outside the zip")
	manifest, err := fs.ReadFile(zr, "package.json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"module"}`, string(manifest), "the rest of the zip is as it was")
}

// A mail.html in the folder is found by itself, a file named with --mail has to be there.
func TestOwnMail(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), mailName)
	mail, err := ownMail(file, false)
	require.NoError(t, err)
	assert.Empty(t, mail, "no file, so the built-in mail")
	_, err = ownMail(file, true)
	require.ErrorIs(t, err, fs.ErrNotExist)

	require.NoError(t, os.WriteFile(file, []byte("<p>{{name}}</p>"), 0o600))
	_, err = ownMail(file, false)
	require.ErrorContains(t, err, "no {{key}}")
	require.NoError(t, os.WriteFile(file, []byte("<p>{{ key }}</p>"), 0o600))
	mail, err = ownMail(file, false)
	require.NoError(t, err)
	assert.Equal(t, "<p>{{ key }}</p>", mail)
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
		assert.False(t, h.ModTime.After(time.Now()), "tar warns about a file from the future")
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		assert.Equal(t, f.body, string(body))
	}
	_, err := tr.Next()
	require.ErrorIs(t, err, io.EOF)
}

// A token that leaked can be replaced. Without the flag it stays, or every
// export would cut the deployed worker off. So would a new token that was
// stored for an export that then wrote nothing.
func TestNewToken(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dataDir, out := t.TempDir(), filepath.Join(t.TempDir(), "worker")
	q, closeDB, err := db.Open(ctx, dataDir)
	require.NoError(t, err)
	_, err = q.CreateProduct(ctx, &db.CreateProductParams{
		Name: "App", Prefix: "APP", Plan: "pro", PrivateKey: "key", WorkerToken: "the-first-token",
	})
	require.NoError(t, err)
	closeDB()

	export := func(args ...string) error {
		root := &cli.Command{
			Flags:    []cli.Flag{&cli.StringFlag{Name: "data-dir"}},
			Commands: []*cli.Command{workerCommand()},
		}
		return root.Run(ctx, append([]string{"inkan", "--data-dir", dataDir, "worker", "export"}, args...))
	}
	stored := func() string {
		q, closeDB, err := db.Open(ctx, dataDir)
		require.NoError(t, err)
		defer closeDB()
		p, err := q.GetProductByPrefix(ctx, "APP")
		require.NoError(t, err)
		return p.WorkerToken
	}

	require.NoError(t, export("APP", out))
	assert.Equal(t, "the-first-token", stored())

	require.Error(t, export("--new-token", "--mail", filepath.Join(out, "typo.html"), "APP", out))
	assert.Equal(t, "the-first-token", stored(), "an export that wrote nothing changes no token")

	require.NoError(t, export("--new-token", "APP", out))
	token := stored()
	assert.NotEqual(t, "the-first-token", token)
	assert.GreaterOrEqual(t, len(token), 32, "a worker turns a shorter token down")
	keys, err := os.ReadFile(filepath.Join(out, "worker.keys.env"))
	require.NoError(t, err)
	assert.Contains(
		t,
		string(keys),
		"INKAN_WORKER_TOKEN="+token+"\n",
		"the dashboard reads with the one the files carry",
	)

	require.ErrorContains(t, export("NOPE", out), "no product has the prefix NOPE")
}
