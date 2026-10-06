package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/mizuchilabs/inkan/internal/db"
)

func backupCommand() *cli.Command {
	return &cli.Command{
		Name:      "backup",
		Usage:     "Write a copy of the database, also while inkan runs. It holds the signing keys",
		ArgsUsage: "[file]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			out, err := db.Backup(ctx, cmd.String("data-dir"), cmd.Args().First())
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
}

func keysCommand() *cli.Command {
	return &cli.Command{
		Name:   "keys",
		Usage:  "Print the prefix and the signing key of every product, to keep somewhere safe",
		Action: keys,
	}
}

// keys prints secrets on purpose. A signing key that is lost cannot be made
// again, and no app that ships with its public key takes a key from another.
func keys(ctx context.Context, cmd *cli.Command) error {
	q, err := db.Open(ctx, cmd.String("data-dir"))
	if err != nil {
		return err
	}
	products, err := q.ListProducts(ctx)
	if err != nil {
		return err
	}
	for _, p := range products {
		fmt.Printf("%s\t%s\n", p.Prefix, p.PrivateKey)
	}
	return nil
}
