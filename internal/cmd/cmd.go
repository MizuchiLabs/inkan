package cmd

import "github.com/urfave/cli/v3"

var Commands = []*cli.Command{
	openapiCommand(),
	backupCommand(),
	keysCommand(),
	workerCommand(),
}
