package cmd

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
				Usage:     "Write the worker settings of a product into its folder: worker.env and src/template.ts",
				ArgsUsage: "<prefix> [dir]",
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

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "worker.env"), []byte(envFile(p)), 0o600); err != nil {
		return err
	}
	module, err := templateModule(p.MailTemplate)
	if err != nil {
		return err
	}
	//nolint:gosec // the mail is source the worker is built from, not a secret
	if err := os.WriteFile(filepath.Join(dir, "src", "template.ts"), []byte(module), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s and %s.\n", filepath.Join(dir, "worker.env"), filepath.Join(dir, "src", "template.ts"))
	fmt.Println("worker.env holds the private key. Fill in the comments, deploy the worker, then delete it.")
	return nil
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
	b.WriteString("# SCW_SECRET_KEY=...\n")
	b.WriteString("# SCW_PROJECT_ID=...\n")
	writeSetting(&b, "INKAN_MAIL_FROM", p.MailFrom, "licenses@example.com")
	writeSetting(&b, "INKAN_PRODUCT_NAME", p.Name, "")
	writeSetting(&b, "INKAN_MAIL_LOGO", p.MailLogo, "https://example.com/logo.png")
	writeSetting(&b, "INKAN_MAIL_ACCENT", p.MailAccent, "#2f3e8f")
	writeSetting(&b, "INKAN_MAIL_FOOTER", p.MailFooter, "Questions? Write to support@example.com")
	writeSetting(&b, "INKAN_MAIL_COMPANY", p.MailCompany, "Example GmbH, Street 1, 12345 Berlin, HRB 12345")
	return b.String()
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
