package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	_ "modernc.org/sqlite"

	"github.com/urfave/cli/v3"

	"github.com/mizuchilabs/kata/buildinfo"
	"github.com/mizuchilabs/kata/logx"
	"github.com/mizuchilabs/kata/sigx"

	"github.com/mizuchilabs/inkan/internal/api"
	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/mirror"
)

func main() {
	cmd := &cli.Command{
		EnableShellCompletion: true,
		Suggest:               true,
		Name:                  "inkan",
		Version:               buildinfo.String(),
		Usage:                 "issue and manage offline license keys",
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			logx.Init(cmd.Bool("debug"))
			return ctx, nil
		},
		Action: run,
		Commands: []*cli.Command{
			{
				Name:    "openapi",
				Aliases: []string{"o"},
				Usage:   "Generate the OpenAPI spec without starting the server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "output",
						Aliases: []string{"o"},
						Usage:   "Output file path",
						Value:   "web/openapi.json",
					},
				},
				Action: openapi,
			},
			workerCommand(),
			backupCommand(),
			keysCommand(),
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "Enable debug logging",
				Sources: cli.EnvVars("INKAN_DEBUG"),
			},
			&cli.StringFlag{
				Name:    "addr",
				Usage:   "Address to listen on. Anything but localhost needs a password",
				Value:   "127.0.0.1:3000",
				Sources: cli.EnvVars("INKAN_ADDR"),
			},
			&cli.StringFlag{
				Name:    "password",
				Usage:   "Password for the UI and the API. Without one inkan only listens on localhost",
				Sources: cli.EnvVars("INKAN_PASSWORD"),
			},
			&cli.StringFlag{
				Name:    "api-token",
				Usage:   "Bearer token for scripts that use the API, at least 32 characters",
				Sources: cli.EnvVars("INKAN_API_TOKEN"),
			},
			&cli.StringFlag{
				Name:    "data-dir",
				Usage:   "Directory for the database",
				Value:   "data",
				Sources: cli.EnvVars("INKAN_DATA_DIR"),
			},
		},
	}

	if err := cmd.Run(sigx.NotifyContext(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd.Name, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	q, err := db.Open(ctx, cmd.String("data-dir"))
	if err != nil {
		return err
	}
	srv, err := api.New(q, cmd.String("password"), cmd.String("api-token"))
	if err != nil {
		return err
	}
	go mirror.Run(ctx, q)
	return srv.Start(ctx, cmd.String("addr"))
}

func openapi(_ context.Context, cmd *cli.Command) error {
	b, err := api.Spec().MarshalJSON()
	if err != nil {
		return err
	}
	out := cmd.String("output")
	if err := os.WriteFile(out, b, 0o600); err != nil {
		return fmt.Errorf("writing spec: %w", err)
	}
	slog.Info("OpenAPI spec written", "path", out)
	return nil
}
