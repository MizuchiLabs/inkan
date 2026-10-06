package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/mizuchilabs/inkan/internal/api"
)

func openapiCommand() *cli.Command {
	return &cli.Command{
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
	}
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
