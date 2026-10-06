package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
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
				Name:      "env",
				Usage:     "Print the worker settings of a product from the local database",
				ArgsUsage: "<prefix>",
				Action:    workerEnv,
			},
		},
	}
}

// workerEnv prints secrets on purpose, a worker cannot sign without them.
func workerEnv(ctx context.Context, cmd *cli.Command) error {
	prefix := strings.ToUpper(cmd.Args().First())
	if prefix == "" {
		return errors.New("which product? pass its key prefix")
	}
	q, err := db.Open(ctx, cmd.String("data-dir"))
	if err != nil {
		return err
	}
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

	fmt.Printf("INKAN_PREFIX=%s\n", p.Prefix)
	fmt.Printf("INKAN_PRIVATE_KEY=%s\n", p.PrivateKey)
	if p.OldPublicKeys != "" {
		// From before a rotation. A refresh trades their keys for one of the new key.
		fmt.Printf("INKAN_OLD_PUBLIC_KEYS=%s\n", p.OldPublicKeys)
	}
	fmt.Printf("INKAN_WORKER_TOKEN=%s\n", p.WorkerToken)
	fmt.Printf("INKAN_PLAN=%s\n", envValue(p.Plan))
	fmt.Printf("INKAN_GRACE_DAYS=%d\n", p.GraceDays)
	fmt.Printf("INKAN_MAX_KEY_DAYS=%d\n", p.MaxKeyDays)

	// Secrets of Stripe and the mail service are not stored in inkan. They and
	// what the product does not have yet are printed as comments, so the file
	// shows what is left to fill in.
	fmt.Println()
	fmt.Println("# Stripe. A restricted key that reads subscriptions and customers is enough. One time")
	fmt.Println("# purchases also need Checkout sessions, payment intents and charges.")
	fmt.Println("# INKAN_STRIPE_KEY=rk_live_...")
	printSetting("INKAN_STRIPE_PRODUCT", p.ProviderProduct, "prod_...")

	fmt.Println()
	fmt.Println("# Mailing keys. Add a webhook endpoint in Stripe for https://<worker>/webhook that sends")
	fmt.Println("# invoice.paid, checkout.session.completed and checkout.session.async_payment_succeeded.")
	fmt.Println("# For local tests the secret is the one `stripe listen --print-secret` prints.")
	fmt.Println("# INKAN_STRIPE_WEBHOOK_SECRET=whsec_...")
	fmt.Println("# One mail service, Resend or Scaleway Transactional Email.")
	fmt.Println("# RESEND_API_KEY=re_...")
	fmt.Println("# SCW_SECRET_KEY=...")
	fmt.Println("# SCW_PROJECT_ID=...")
	printSetting("INKAN_MAIL_FROM", p.MailFrom, "licenses@example.com")
	printSetting("INKAN_PRODUCT_NAME", p.Name, "")
	printSetting("INKAN_MAIL_LOGO", p.MailLogo, "https://example.com/logo.png")
	printSetting("INKAN_MAIL_ACCENT", p.MailAccent, "#2f3e8f")
	printSetting("INKAN_MAIL_FOOTER", p.MailFooter, "Questions? Write to support@example.com")
	printSetting("INKAN_MAIL_COMPANY", p.MailCompany, "Example GmbH, Street 1, 12345 Berlin, HRB 12345")
	return nil
}

func printSetting(name, value, hint string) {
	if value == "" {
		fmt.Printf("# %s=%s\n", name, hint)
		return
	}
	fmt.Printf("%s=%s\n", name, envValue(value))
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
