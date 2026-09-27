//go:build !debug

package main

import (
	"context"
	"errors"

	"github.com/riipandi/tango/internal/config"
	"github.com/urfave/cli/v3"
)

var rootCmd = &cli.Command{
	Name:            config.AppIdentifier,
	Description:     config.Description,
	Version:         config.AppVersion,
	HideVersion:     true,
	HideHelpCommand: true,
	Commands: []*cli.Command{
		serveCmd,
		migrateUpCmd,
		migrateDownCmd,
		migrateStatusCmd,
		migrateVersionCmd,
		dbExportCmd,
		dbImportCmd,
		adminResetPasswordCmd,
		keyGenerateCmd,
		keyRotateCmd,
		initializeCmd,
		healthCheckCmd,
		configGenerateCmd,
		configValidateCmd,
		configPrintCmd,
	},
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     config.FlagEnvFile,
			Usage:    "Load environment variables from a file",
			Required: false,
		},
		&cli.StringFlag{
			Name:     config.FlagConfigFile,
			Usage:    "Load configuration from a JSON file",
			Required: false,
		},
		&cli.BoolFlag{
			Name:    "version",
			Usage:   "Show the application version",
			Aliases: []string{"V"},
		},
	},
	Before: initConfig,
	// The logger and observer are installed after the configuration is resolved
	// and closed after the command returns, so a run flushes what it queued.
	// Both are attached to the root command because every subcommand inherits
	// them.
	//
	// The observer is closed first: a signal it drains may be reported through
	// the logger, and a logger that is already closed would drop that line.
	After: func(ctx context.Context, cmd *cli.Command) error {
		return errors.Join(closeObserver(ctx), closeLogger(ctx))
	},
}
