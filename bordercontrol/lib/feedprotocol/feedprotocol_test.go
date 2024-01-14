package feedprotocol

import (
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate})
}

func TestFeedProtocol(t *testing.T) {

	// define test protocols
	protoBEAST := BEAST
	protoMLAT := MLAT
	protoUnsupported := Protocol(254)

	t.Run("test GetName BEAST", func(t *testing.T) {
		name, err := GetName(protoBEAST)
		assert.NoError(t, err)
		assert.Equal(t, "BEAST", name)
	})

	t.Run("test GetName MLAT", func(t *testing.T) {
		name, err := GetName(protoMLAT)
		assert.NoError(t, err)
		assert.Equal(t, "MLAT", name)
	})

	t.Run("test .Name BEAST", func(t *testing.T) {
		name := protoBEAST.Name()
		assert.Equal(t, "BEAST", name)
	})

	t.Run("test .Name MLAT", func(t *testing.T) {
		name := protoMLAT.Name()
		assert.Equal(t, "MLAT", name)
	})

	t.Run("test GetName unsupported protocol", func(t *testing.T) {
		_, err := GetName(protoUnsupported)
		assert.Error(t, err)
		assert.Equal(t, ErrUnknownProtocol.Error(), err.Error())
	})

	t.Run("test .Name panic with unsupported protocol", func(t *testing.T) {
		ptf := assert.PanicTestFunc(func() {
			_ = protoUnsupported.Name()
		})
		assert.Panics(t, ptf)
	})

	t.Run("test IsValid BEAST", func(t *testing.T) {
		assert.True(t, IsValid(protoBEAST))
	})

	t.Run("test IsValid MLAT", func(t *testing.T) {
		assert.True(t, IsValid(protoMLAT))
	})

	t.Run("test IsValid unsupported", func(t *testing.T) {
		assert.False(t, IsValid(protoUnsupported))
	})

	t.Run("test GetProtoFromName BEAST", func(t *testing.T) {
		p, err := GetProtoFromName(ProtocolNameBEAST)
		assert.NoError(t, err)
		assert.Equal(t, BEAST, p)
	})

	t.Run("test GetProtoFromName MLAT", func(t *testing.T) {
		p, err := GetProtoFromName(ProtocolNameMLAT)
		assert.NoError(t, err)
		assert.Equal(t, MLAT, p)
	})

	t.Run("test GetProtoFromName unsupported", func(t *testing.T) {
		_, err := GetProtoFromName("unsupported")
		assert.Error(t, err)
		assert.Equal(t, ErrUnknownProtocol.Error(), err.Error())
	})
}
