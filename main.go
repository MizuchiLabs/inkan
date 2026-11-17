package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/urfave/cli/v3"

	"github.com/mizuchilabs/kata/buildinfo"
	"github.com/mizuchilabs/kata/logx"
	"github.com/mizuchilabs/kata/sigx"
	"github.com/mizuchilabs/nushi"

	"github.com/mizuchilabs/inkan/internal/api"
	"github.com/mizuchilabs/inkan/internal/cmd"
	"github.com/mizuchilabs/inkan/internal/db"
	"github.com/mizuchilabs/inkan/internal/mirror"
)

func main() {
	app := &cli.Command{
		EnableShellCompletion: true,
		Suggest:               true,
		Name:                  "inkan",
		Version:               buildinfo.String(),
		Usage:                 "issue and manage offline license keys",
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			logx.Init(cmd.Bool("debug"))
			return ctx, nil
		},
		Action:   run,
		Commands: cmd.Commands,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "Enable debug logging",
				Sources: cli.EnvVars("INKAN_DEBUG"),
			},
			&cli.StringFlag{
				Name:    "addr",
				Usage:   "Address to listen on. Anything but localhost needs a sign in",
				Value:   "127.0.0.1:3000",
				Sources: cli.EnvVars("INKAN_ADDR"),
			},
			&cli.StringFlag{
				Name:    "data-dir",
				Usage:   "Directory for the database",
				Value:   "data",
				Sources: cli.EnvVars("INKAN_DATA_DIR"),
			},
			&cli.StringFlag{
				Name:    "backup-dir",
				Usage:   "Directory for the daily backups of the database, best on another disk. Defaults to backups in the data dir",
				Sources: cli.EnvVars("INKAN_BACKUP_DIR"),
			},
		},
	}

	if err := app.Run(sigx.NotifyContext(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", app.Name, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	guard, err := nushi.FromEnv("inkan")
	if err != nil {
		return err
	}
	dataDir := cmd.String("data-dir")
	q, closeDB, err := db.Open(ctx, dataDir)
	if err != nil {
		return err
	}
	defer closeDB()
	// The jobs end with the server, also when it fails to start, and before the database closes.
	var jobs sync.WaitGroup
	defer jobs.Wait()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	jobs.Go(func() { mirror.Run(ctx, q) })
	jobs.Go(func() {
		db.KeepBackups(ctx, dataDir, cmp.Or(cmd.String("backup-dir"), filepath.Join(dataDir, "backups")))
	})
	return api.New(q, guard).Start(ctx, cmd.String("addr"))
}
