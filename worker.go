package main

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/mizuchilabs/inkan/internal/api"
	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/worker"
)

func workerCommand() *cli.Command {
	return &cli.Command{
		Name:    "worker",
		Aliases: []string{"w"},
		Usage:   "Serve the public side of one product: the Stripe webhook and key refresh",
		Action:  runWorker,
		Commands: []*cli.Command{
			{
				Name:      "env",
				Usage:     "Print the worker settings of a product from the local database",
				ArgsUsage: "<prefix>",
				Action:    workerEnv,
			},
			{
				Name:      "mail",
				Usage:     "Send the key mail with a made up key, to see how it looks and whether the mail server works",
				ArgsUsage: "<address>",
				Action:    workerMail,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "html",
						Usage: "Print the html of the mail and send nothing, inkan worker mail --html > mail.html",
					},
				},
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "prefix",
				Usage:   "Key prefix of the product",
				Sources: cli.EnvVars("INKAN_PREFIX"),
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "private-key",
				Usage:   "Base64 Ed25519 private key of the product",
				Sources: cli.EnvVars("INKAN_PRIVATE_KEY"),
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "stripe-key",
				Usage:   "Stripe API key that can read subscriptions and customers",
				Sources: cli.EnvVars("INKAN_STRIPE_KEY"),
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "stripe-product",
				Usage:   "Stripe product id. Its subscriptions are licenses",
				Sources: cli.EnvVars("INKAN_STRIPE_PRODUCT"),
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "plan",
				Usage:   "Plan claim for Stripe prices without plan metadata",
				Sources: cli.EnvVars("INKAN_PLAN"),
				Local:   true,
			},
			&cli.Int64Flag{
				Name:    "grace-days",
				Usage:   "Days a key stays valid past its paid period",
				Value:   14,
				Sources: cli.EnvVars("INKAN_GRACE_DAYS"),
				Local:   true,
			},
			&cli.Int64Flag{
				Name:    "key-days",
				Usage:   "Longest life of a key in days, 0 has no limit. Apps refresh for the next one",
				Sources: cli.EnvVars("INKAN_KEY_DAYS"),
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "stripe-webhook-secret",
				Usage:   "Signing secret of the Stripe webhook endpoint. Without it only refresh is served",
				Sources: cli.EnvVars("INKAN_STRIPE_WEBHOOK_SECRET"),
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "smtp-addr",
				Usage:   "Mail server that sends the keys, host:port with STARTTLS",
				Sources: cli.EnvVars("INKAN_SMTP_ADDR"),
			},
			&cli.StringFlag{
				Name:    "smtp-user",
				Sources: cli.EnvVars("INKAN_SMTP_USER"),
			},
			&cli.StringFlag{
				Name:    "smtp-pass",
				Sources: cli.EnvVars("INKAN_SMTP_PASS"),
			},
			&cli.StringFlag{
				Name:    "mail-from",
				Usage:   "Sender of the key mails, an address or \"Name <address>\"",
				Sources: cli.EnvVars("INKAN_MAIL_FROM"),
			},
			&cli.StringFlag{
				Name:    "product-name",
				Usage:   "How the key mail calls the product, the prefix when empty",
				Sources: cli.EnvVars("INKAN_PRODUCT_NAME"),
			},
			&cli.StringFlag{
				Name:    "mail-logo",
				Usage:   "https url of a logo for the dark header of the key mail",
				Sources: cli.EnvVars("INKAN_MAIL_LOGO"),
			},
			&cli.StringFlag{
				Name:    "mail-accent",
				Usage:   "Color of the key mail, like #2f3e8f",
				Sources: cli.EnvVars("INKAN_MAIL_ACCENT"),
			},
			&cli.StringFlag{
				Name:    "mail-footer",
				Usage:   "One line under the key, like where to get help",
				Sources: cli.EnvVars("INKAN_MAIL_FOOTER"),
			},
			&cli.StringFlag{
				Name:    "mail-company",
				Usage:   "One line at the end of the key mail: legal name, address, register number",
				Sources: cli.EnvVars("INKAN_MAIL_COMPANY"),
			},
			&cli.StringFlag{
				Name:    "mail-templates",
				Usage:   "Directory with subject.txt, body.txt and body.html that replace the built in mail",
				Sources: cli.EnvVars("INKAN_MAIL_TEMPLATES"),
			},
		},
	}
}

func runWorker(ctx context.Context, cmd *cli.Command) error {
	cfg := worker.Config{
		Prefix:        cmd.String("prefix"),
		PrivateKey:    cmd.String("private-key"),
		StripeKey:     cmd.String("stripe-key"),
		StripeProduct: cmd.String("stripe-product"),
		Plan:          cmd.String("plan"),
		GraceDays:     cmd.Int64("grace-days"),
		KeyDays:       cmd.Int64("key-days"),
		WebhookSecret: cmd.String("stripe-webhook-secret"),
	}
	if cfg.WebhookSecret != "" {
		mailer, err := mailer(cmd)
		if err != nil {
			return err
		}
		if mailer.Addr == "" || mailer.From == "" {
			slog.Warn("No mail server and sender are set, so keys are not mailed. Only refresh is served")
			cfg.WebhookSecret = ""
		} else {
			cfg.Send = mailer.Send
		}
	}
	handler, err := worker.New(cfg)
	if err != nil {
		return err
	}
	slog.Info("Worker ready", "product", cfg.Prefix, "webhook", cfg.Send != nil)
	return api.Serve(ctx, cmd.String("addr"), handler)
}

func mailer(cmd *cli.Command) (worker.SMTP, error) {
	m := worker.SMTP{
		Addr: cmd.String("smtp-addr"),
		User: cmd.String("smtp-user"),
		Pass: cmd.String("smtp-pass"),
		From: cmd.String("mail-from"),
		Brand: worker.Brand{
			Name:    cmd.String("product-name"),
			Logo:    cmd.String("mail-logo"),
			Accent:  cmd.String("mail-accent"),
			Footer:  cmd.String("mail-footer"),
			Company: cmd.String("mail-company"),
		},
	}
	if dir := cmd.String("mail-templates"); dir != "" {
		var err error
		if m.Templates, err = worker.LoadTemplates(dir); err != nil {
			return m, err
		}
	}
	return m, nil
}

func workerMail(ctx context.Context, cmd *cli.Command) error {
	mailer, err := mailer(cmd)
	if err != nil {
		return err
	}
	to := cmd.Args().First()
	key, claims, err := worker.Sample("SAMPLE", cmp.Or(to, "you@example.com"))
	if err != nil {
		return err
	}
	if cmd.Bool("html") {
		_, _, html, err := mailer.Templates.Render(key, claims, mailer.Brand)
		if err != nil {
			return err
		}
		if html == "" {
			return errors.New("these templates are plain text only, there is no html to print")
		}
		fmt.Print(html)
		return nil
	}
	if to == "" {
		return errors.New("where to? pass an email address, or --html to print the mail")
	}
	if mailer.Addr == "" || mailer.From == "" {
		return errors.New("set --smtp-addr and --mail-from first")
	}
	if err := mailer.Send(ctx, key, claims); err != nil {
		return err
	}
	fmt.Println("Sent a sample key mail to", to)
	return nil
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

	fmt.Printf("INKAN_PREFIX=%s\n", p.Prefix)
	fmt.Printf("INKAN_PRIVATE_KEY=%s\n", p.PrivateKey)
	fmt.Printf("INKAN_STRIPE_KEY=%s\n", p.ProviderKey)
	fmt.Printf("INKAN_STRIPE_PRODUCT=%s\n", p.ProviderProduct)
	fmt.Printf("INKAN_PLAN=%s\n", p.Plan)
	fmt.Printf("INKAN_GRACE_DAYS=%d\n", p.GraceDays)
	fmt.Printf("INKAN_KEY_DAYS=%d\n", p.KeyDays)

	// The rest is only needed to mail keys. What the product does not have
	// yet is printed as a comment, so the file shows what is left to fill in.
	fmt.Println()
	fmt.Println("# Mailing keys. Add a webhook endpoint in Stripe for https://<worker>/webhook that sends")
	fmt.Println("# invoice.paid, checkout.session.completed and checkout.session.async_payment_succeeded.")
	fmt.Println("# For local tests the secret is the one `stripe listen --print-secret` prints.")
	for _, v := range []struct{ name, value, hint string }{
		{"INKAN_STRIPE_WEBHOOK_SECRET", p.WebhookSecret, "whsec_..."},
		{"INKAN_SMTP_ADDR", p.SmtpAddr, "smtp.example.com:587"},
		{"INKAN_SMTP_USER", p.SmtpUser, ""},
		{"INKAN_SMTP_PASS", p.SmtpPass, ""},
		{"INKAN_MAIL_FROM", p.MailFrom, "licenses@example.com"},
		{"INKAN_PRODUCT_NAME", p.Name, ""},
		{"INKAN_MAIL_LOGO", p.MailLogo, "https://example.com/logo.png"},
		{"INKAN_MAIL_ACCENT", p.MailAccent, "#2f3e8f"},
		{"INKAN_MAIL_FOOTER", p.MailFooter, "Questions? Write to support@example.com"},
		{"INKAN_MAIL_COMPANY", p.MailCompany, "Example GmbH, Street 1, 12345 Berlin, HRB 12345"},
	} {
		if v.value == "" {
			fmt.Printf("# %s=%s\n", v.name, v.hint)
			continue
		}
		fmt.Printf("%s=%s\n", v.name, v.value)
	}
	return nil
}
