package logging

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

var (
	isCli bool
)

const (
	Debug = "debug"
	Quiet = "quiet"
)

// IncludeVerbosityFlags adds debug and quiet flags to app
func IncludeVerbosityFlags(app *cli.App) {
	app.Flags = append(app.Flags,
		&cli.BoolFlag{
			Name:    Debug,
			Usage:   "Show Extra Debug Information",
			EnvVars: []string{"DEBUG"},
		},
		&cli.BoolFlag{
			Name:    Quiet,
			Usage:   "Only show important messages",
			EnvVars: []string{"QUIET"},
		},
	)
}

// SetLoggingLevel sets the log based on whether the user has asked for debug and/or quiet logging
func SetLoggingLevel(c *cli.Context) {
	SetVerboseOrQuiet(
		c.Bool(Debug),
		c.Bool(Quiet),
	)
}

// SetVerboseOrQuiet sets the log based on whether the user has asked for debug and/or quiet logging
func SetVerboseOrQuiet(verbose, quiet bool) {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if verbose {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	if quiet {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	}
}

// cliWriter writes output to the console
func cliWriter() zerolog.ConsoleWriter {
	return zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate}
}

// ConfigureForCli sets log output to CLI
func ConfigureForCli() {
	isCli = true
	log.Logger = log.Output(cliWriter())
}
