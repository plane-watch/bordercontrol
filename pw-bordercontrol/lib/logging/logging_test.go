package logging

import (
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v2"
)

func TestIncludeVerbosityFlags(t *testing.T) {

	var (
		debugFound bool
		quietFound bool
	)

	app := cli.App{}
	IncludeVerbosityFlags(&app)

	// ensure debug & quiet options added
	assert.Len(t, app.Flags, 2)
	for i, _ := range app.Flags {
		for j, _ := range app.Flags[i].Names() {
			if app.Flags[i].Names()[j] == "debug" {
				debugFound = true
			}
			if app.Flags[i].Names()[j] == "quiet" {
				quietFound = true
			}
		}
	}
	assert.True(t, debugFound)
	assert.True(t, quietFound)
}

func TestSetLoggingLevel(t *testing.T) {
	c := cli.Context{}
	SetLoggingLevel(&c)
	assert.Equal(t, zerolog.InfoLevel, zerolog.GlobalLevel())
}

func TestSetVerboseOrQuiet(t *testing.T) {

	SetVerboseOrQuiet(false, false)
	assert.Equal(t, zerolog.InfoLevel, zerolog.GlobalLevel())

	SetVerboseOrQuiet(true, false)
	assert.Equal(t, zerolog.DebugLevel, zerolog.GlobalLevel())

	SetVerboseOrQuiet(false, true)
	assert.Equal(t, zerolog.ErrorLevel, zerolog.GlobalLevel())

	SetVerboseOrQuiet(true, true)
	assert.Equal(t, zerolog.ErrorLevel, zerolog.GlobalLevel())

}

func TestCliWriter(t *testing.T) {
	zcw := cliWriter()
	assert.Equal(t, zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate}, zcw)
}

func TestConfigureForCli(t *testing.T) {
	assert.False(t, isCli)
	ConfigureForCli()
	assert.True(t, isCli)
	assert.Equal(t, log.Output(cliWriter()), log.Logger)
}
