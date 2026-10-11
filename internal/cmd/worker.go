package cmd

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/mizuchilabs/inkan/internal/db"
)

func workerCommand() *cli.Command {
	return &cli.Command{
		Name:    "worker",
		Aliases: []string{"w"},
		Usage:   "Settings for the worker of a product, the public side that reads Stripe, mails keys and refreshes them",
		Commands: []*cli.Command{
			{
				Name:      "export",
				Usage:     "Write the worker settings of a product into its folder: worker.env and src/template.ts. With - as the dir, a tar of them goes to stdout",
				ArgsUsage: "<prefix> [dir|-]",
				Action:    workerExport,
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
	dir := cmp.Or(cmd.Args().Get(1), ".")
	q, closeDB, err := db.OpenExisting(ctx, cmd.String("data-dir"))
	if err != nil {
		return err
	}
	defer closeDB()
	p, err := q.GetProductByPrefix(ctx, prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no product has the prefix %s", prefix)
	}
	if err != nil {
		return err
	}
	// A product from before workers had tokens.
	if p.WorkerToken == "" {
		p.WorkerToken = rand.Text() + rand.Text()
		err := q.SetProductWorkerToken(ctx, &db.SetProductWorkerTokenParams{WorkerToken: p.WorkerToken, ID: p.ID})
		if err != nil {
			return err
		}
	}

	module, err := templateModule(p.MailTemplate)
	if err != nil {
		return err
	}
	settings, err := envJSON(p)
	if err != nil {
		return err
	}
	files := []exported{
		{"worker.env", 0o600, envFile(p)},
		{"worker.json", 0o600, settings},
		{"src/template.ts", 0o644, module},
	}

	// A container has no way to hand files out, a pipe into tar on the host has.
	if dir == "-" {
		if err := writeTar(os.Stdout, files); err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, nextSteps)
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o750); err != nil {
		return err
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), f.mode); err != nil {
			return err
		}
	}
	fmt.Printf("Wrote %s, %s and %s.\n",
		filepath.Join(dir, "worker.env"), filepath.Join(dir, "worker.json"), filepath.Join(dir, "src", "template.ts"))
	fmt.Print(nextSteps)
	return nil
}

const nextSteps = `worker.env and worker.json hold the private key. Fill in what is missing, deploy the worker, then delete them.

In the worker folder of a clone of the repository:
  Cloudflare  pnpm wrangler secret bulk worker.env && pnpm deploy
  Scaleway    pnpm build:scaleway, then upload dist/scaleway.zip and paste worker.json as the secrets
  Node        node --env-file=worker.env src/node.ts
Then enter the url of the worker under Edit on the Products page.
`

type exported struct {
	name string
	mode os.FileMode
	body string
}

func writeTar(w io.Writer, files []exported) error {
	tw := tar.NewWriter(w)
	for _, f := range files {
		err := tw.WriteHeader(&tar.Header{
			Name:    f.name,
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

func envFile(p *db.Product) string {
	var b strings.Builder
	fmt.Fprintf(&b, "INKAN_PREFIX=%s\n", p.Prefix)
	fmt.Fprintf(&b, "INKAN_PRIVATE_KEY=%s\n", p.PrivateKey)
	if p.OldPublicKeys != "" {
		// From before a rotation. A refresh trades their keys for one of the new key.
		fmt.Fprintf(&b, "INKAN_OLD_PUBLIC_KEYS=%s\n", p.OldPublicKeys)
	}
	fmt.Fprintf(&b, "INKAN_WORKER_TOKEN=%s\n", p.WorkerToken)
	fmt.Fprintf(&b, "INKAN_PLAN=%s\n", envValue(p.Plan))
	fmt.Fprintf(&b, "INKAN_GRACE_DAYS=%d\n", p.GraceDays)
	fmt.Fprintf(&b, "INKAN_MAX_KEY_DAYS=%d\n", p.MaxKeyDays)

	// Secrets of Stripe and the mail service are not stored in inkan. They and
	// what the product does not have yet are written as comments, so the file
	// shows what is left to fill in.
	b.WriteString("\n")
	b.WriteString("# Stripe. A restricted key that reads subscriptions and customers is enough. One time\n")
	b.WriteString("# purchases also need Checkout sessions, payment intents and charges.\n")
	b.WriteString("# INKAN_STRIPE_KEY=rk_live_...\n")
	writeSetting(&b, "INKAN_STRIPE_PRODUCT", p.ProviderProduct, "prod_...")

	b.WriteString("\n")
	b.WriteString("# Mailing keys. Add a webhook endpoint in Stripe for https://<worker>/webhook that sends\n")
	b.WriteString("# invoice.paid, checkout.session.completed and checkout.session.async_payment_succeeded.\n")
	b.WriteString("# For local tests the secret is the one `stripe listen --print-secret` prints.\n")
	b.WriteString("# INKAN_STRIPE_WEBHOOK_SECRET=whsec_...\n")
	b.WriteString("# One mail service, Resend or Scaleway Transactional Email.\n")
	b.WriteString("# RESEND_API_KEY=re_...\n")
	b.WriteString("# INKAN_SCW_SECRET_KEY=...\n")
	b.WriteString("# INKAN_SCW_PROJECT_ID=...\n")
	writeSetting(&b, "INKAN_MAIL_FROM", p.MailFrom, "licenses@example.com")
	writeSetting(&b, "INKAN_MAIL_REPLY_TO", p.MailReplyTo, "support@example.com")
	writeSetting(&b, "INKAN_PRODUCT_NAME", p.Name, "")
	writeSetting(&b, "INKAN_MAIL_LOGO", p.MailLogo, "https://example.com/logo.png")
	writeSetting(&b, "INKAN_MAIL_ACCENT", p.MailAccent, "#2f3e8f")
	writeSetting(&b, "INKAN_MAIL_FOOTER", p.MailFooter, "Questions? Just reply to this mail.")
	writeSetting(&b, "INKAN_MAIL_COMPANY", p.MailCompany, "Example GmbH, Street 1, 12345 Berlin, HRB 12345")
	return b.String()
}

// envJSON is what worker.env sets, as the object the Scaleway console takes
// in its JSON mode. The secrets of Stripe and the mail service are added
// there, a comment has no place in JSON.
func envJSON(p *db.Product) (string, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf, jsontext.Multiline(true), jsontext.WithIndent("  "))
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return "", err
	}
	for _, s := range envSettings(p) {
		if s[1] == "" {
			continue
		}
		for _, v := range s {
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

// envSettings is every name worker.env sets or comments out, with its value.
func envSettings(p *db.Product) [][2]string {
	return [][2]string{
		{"INKAN_PREFIX", p.Prefix},
		{"INKAN_PRIVATE_KEY", p.PrivateKey},
		{"INKAN_OLD_PUBLIC_KEYS", p.OldPublicKeys},
		{"INKAN_WORKER_TOKEN", p.WorkerToken},
		{"INKAN_PLAN", p.Plan},
		{"INKAN_GRACE_DAYS", strconv.FormatInt(p.GraceDays, 10)},
		{"INKAN_MAX_KEY_DAYS", strconv.FormatInt(p.MaxKeyDays, 10)},
		{"INKAN_STRIPE_PRODUCT", p.ProviderProduct},
		{"INKAN_MAIL_FROM", p.MailFrom},
		{"INKAN_MAIL_REPLY_TO", p.MailReplyTo},
		{"INKAN_PRODUCT_NAME", p.Name},
		{"INKAN_MAIL_LOGO", p.MailLogo},
		{"INKAN_MAIL_ACCENT", p.MailAccent},
		{"INKAN_MAIL_FOOTER", p.MailFooter},
		{"INKAN_MAIL_COMPANY", p.MailCompany},
	}
}

func writeSetting(b *strings.Builder, name, value, hint string) {
	if value == "" {
		fmt.Fprintf(b, "# %s=%s\n", name, hint)
		return
	}
	fmt.Fprintf(b, "%s=%s\n", name, envValue(value))
}

// templateModule is the TypeScript the worker imports its key mail from. An
// empty template gives the file as the repository has it.
func templateModule(template string) (string, error) {
	const head = "// Written by inkan worker export. Empty sends the built-in mail.\n"
	if template == "" {
		return head + "export const template = '';\n", nil
	}
	literal, err := json.Marshal(template)
	if err != nil {
		return "", err
	}
	return head + "export const template =\n\t" + string(literal) + ";\n", nil
}

// envValue quotes a value that an env file would read differently bare. A #
// starts a comment there, so even a color needs it. No parser unescapes a
// quote inside its own kind, so the value gets one it does not hold. A value
// with all three loses its single quotes.
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
