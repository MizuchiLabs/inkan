package main

import (
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
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "smtp-user",
				Sources: cli.EnvVars("INKAN_SMTP_USER"),
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "smtp-pass",
				Sources: cli.EnvVars("INKAN_SMTP_PASS"),
				Local:   true,
			},
			&cli.StringFlag{
				Name:    "mail-from",
				Usage:   "Sender of the key mails, an address or \"Name <address>\"",
				Sources: cli.EnvVars("INKAN_MAIL_FROM"),
				Local:   true,
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
		WebhookSecret: cmd.String("stripe-webhook-secret"),
	}
	if cfg.WebhookSecret != "" {
		mailer := worker.SMTP{
			Addr: cmd.String("smtp-addr"),
			User: cmd.String("smtp-user"),
			Pass: cmd.String("smtp-pass"),
			From: cmd.String("mail-from"),
		}
		if mailer.Addr == "" || mailer.From == "" {
			return errors.New("the webhook mails keys, so it needs --smtp-addr and --mail-from")
		}
		cfg.Send = mailer.Send
	}
	handler, err := worker.New(cfg)
	if err != nil {
		return err
	}
	slog.Info("Worker ready", "product", cfg.Prefix, "webhook", cfg.Send != nil)
	return api.Serve(ctx, cmd.String("addr"), handler)
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
	return nil
}
