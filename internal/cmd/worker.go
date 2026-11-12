package cmd

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/worker"
)

func workerCommand() *cli.Command {
	return &cli.Command{
		Name:    "worker",
		Aliases: []string{"w"},
		Usage:   "The worker of a product, the public side that reads Stripe, mails keys and refreshes them",
		Commands: []*cli.Command{
			{
				Name:      "export",
				Usage:     "Write the worker of a product, ready to deploy, into the folder inkan-worker-<prefix>. With - as the dir, a tar of that folder goes to stdout",
				ArgsUsage: "<prefix> [dir|-]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "new-token",
						Usage: "Replace the token the dashboard reads the worker with. The old one stops working once the worker is deployed with the new one",
					},
				},
				Action: workerExport,
			},
		},
	}
}

// workerExport writes secrets on purpose, a worker cannot sign without them.
func workerExport(ctx context.Context, cmd *cli.Command) error {
	prefix := strings.ToUpper(cmd.Args().First())
	if prefix == "" {
		return errors.New("which product? pass its key prefix")
	}
	q, closeDB, err := db.OpenExisting(ctx, cmd.String("data-dir"))
	if err != nil {
		return err
	}
	defer closeDB()
	p, err := productFor(ctx, q, prefix, cmd.Bool("new-token"))
	if err != nil {
		return err
	}
	files, err := workerFiles(p)
	if err != nil {
		return err
	}

	folder := "inkan-worker-" + strings.ToLower(p.Prefix)
	steps := nextSteps(folder, len(files) > 3)
	// A container has no way to hand files out, a pipe into tar on the host has.
	if cmd.Args().Get(1) == "-" {
		if err := writeTar(os.Stdout, folder, files); err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, steps)
		return nil
	}
	dir := cmd.Args().Get(1)
	if dir == "" {
		dir = folder
	}
	if err := writeDir(dir, files); err != nil {
		return err
	}
	fmt.Printf("Wrote the worker of %s to %s.\n", p.Prefix, dir)
	fmt.Print(steps)
	return nil
}

// productFor finds the product and gives it a token when it has none or a
// new one is asked for.
func productFor(ctx context.Context, q *db.Queries, prefix string, newToken bool) (*db.Product, error) {
	p, err := q.GetProductByPrefix(ctx, prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no product has the prefix %s", prefix)
	}
	if err != nil {
		return nil, err
	}
	if p.WorkerToken == "" || newToken {
		// Two of them, a worker wants at least 32 characters.
		p.WorkerToken = rand.Text() + rand.Text()
		err := q.SetProductWorkerToken(ctx, &db.SetProductWorkerTokenParams{WorkerToken: p.WorkerToken, ID: p.ID})
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

type exported struct {
	name string
	mode os.FileMode
	body string
}

// The settings a user fills in. An export never replaces it.
const (
	envName        = "worker.env"
	envExampleName = "worker.env.example"
)

// workerFiles is the keys of the product in two spellings, the settings to
// fill in and the built worker for every platform.
func workerFiles(p *db.Product) ([]exported, error) {
	keysJSON, err := keysJSON(p)
	if err != nil {
		return nil, err
	}
	files := []exported{
		{"worker.keys.env", 0o600, keysEnv(p)},
		{"worker.keys.json", 0o600, keysJSON},
		{envExampleName, 0o644, worker.EnvExample},
	}
	for _, name := range []string{"cloudflare.js", "node.js", "scaleway.zip"} {
		body, err := worker.Bundle.ReadFile("bundle/" + name)
		// A build without go generate. The keys are still worth writing.
		if errors.Is(err, fs.ErrNotExist) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		files = append(files, exported{name, 0o644, string(body)})
	}
	return append(files, exported{"wrangler.jsonc", 0o644, wrangler(p)}), nil
}

// wrangler is the Cloudflare config of the repository under a name of the
// product's own, so two products do not deploy over each other.
func wrangler(p *db.Product) string {
	return strings.NewReplacer(
		`"name": "inkan-worker"`, `"name": "inkan-`+strings.ToLower(p.Prefix)+`"`,
		`"main": "src/cloudflare.ts"`, `"main": "cloudflare.js"`,
	).Replace(worker.Wrangler)
}

func nextSteps(folder string, built bool) string {
	if !built {
		return `It holds worker.keys.env and worker.keys.json with the signing key, keep them secret.
This build of inkan carries no worker. Deploy the one in the worker folder of the
repository with these keys, its README has the steps.
`
	}
	return fmt.Sprintf(`worker.keys.env and worker.keys.json hold the signing key, keep them secret.

1. Put your Stripe key and the Stripe products into %[1]s/worker.env.
2. Deploy from that folder, on one of:
     Cloudflare  npx wrangler deploy
                 npx wrangler secret bulk worker.keys.env
                 npx wrangler secret bulk worker.env
     Node        node --env-file=worker.keys.env --env-file=worker.env node.js
     Scaleway    create a function from scaleway.zip, then paste worker.keys.json
                 and your settings into its secrets
3. Enter the url of the worker under Edit on the Products page.

Run the export again after a key rotation or a new name or plan, then deploy
again. It never touches worker.env.
`, folder)
}

// writeDir writes the files and starts worker.env from the example, once.
func writeDir(dir string, files []exported) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), f.mode); err != nil {
			return err
		}
		// A mode only applies to a new file, and the keys may replace an older one.
		if err := os.Chmod(filepath.Join(dir, f.name), f.mode); err != nil {
			return err
		}
	}
	env, err := os.OpenFile(filepath.Join(dir, envName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = env.WriteString(worker.EnvExample)
	return errors.Join(err, env.Close())
}

// writeTar leaves worker.env out. Unpacking would replace the one that is filled in.
func writeTar(w io.Writer, folder string, files []exported) error {
	tw := tar.NewWriter(w)
	for _, f := range files {
		err := tw.WriteHeader(&tar.Header{
			Name:    path.Join(folder, f.name),
			Mode:    int64(f.mode),
			Size:    int64(len(f.body)),
			ModTime: time.Now(),
		})
		if err != nil {
			return err
		}
		if _, err := io.WriteString(tw, f.body); err != nil {
			return err
		}
	}
	return tw.Close()
}

// keys is what only the dashboard knows about a worker. Everything else is
// set where the worker runs.
func keys(p *db.Product) [][2]string {
	out := [][2]string{
		{"INKAN_PREFIX", p.Prefix},
		{"INKAN_PRIVATE_KEY", p.PrivateKey},
		{"INKAN_WORKER_TOKEN", p.WorkerToken},
		{"INKAN_PLAN", p.Plan},
		{"INKAN_PRODUCT_NAME", p.Name},
	}
	// From before a rotation. A refresh trades their keys for one of the new
	// key. Left out when empty, not every platform takes an empty secret.
	if p.OldPublicKeys != "" {
		out = append(out, [2]string{"INKAN_OLD_PUBLIC_KEYS", p.OldPublicKeys})
	}
	return out
}

func keysEnv(p *db.Product) string {
	var b strings.Builder
	b.WriteString("# Written by inkan worker export. It holds the signing key, keep it secret.\n")
	for _, k := range keys(p) {
		fmt.Fprintf(&b, "%s=%s\n", k[0], envValue(k[1]))
	}
	return b.String()
}

// keysJSON is the object the Scaleway console takes in its JSON mode.
func keysJSON(p *db.Product) (string, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf, jsontext.Multiline(true), jsontext.WithIndent("  "))
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return "", err
	}
	for _, k := range keys(p) {
		for _, v := range k {
			if err := enc.WriteToken(jsontext.String(v)); err != nil {
				return "", err
			}
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// envValue quotes a value that an env file would read differently bare. A #
// starts a comment there. No parser unescapes a quote inside its own kind, so
// the value gets one it does not hold. A value with all three loses its
// single quotes.
func envValue(value string) string {
	value = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(value)
	if !strings.ContainsFunc(value, func(r rune) bool { return !envBare(r) }) {
		return value
	}
	for _, quote := range []string{"'", `"`, "`"} {
		if !strings.Contains(value, quote) {
			return quote + value + quote
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "") + "'"
}

func envBare(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		strings.ContainsRune("_-.+/=:@,", r)
}
