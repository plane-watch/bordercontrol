package feedprotocol

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		assert.Equal(t, ErrUnknownProtocol(protoUnsupported).Error(), err.Error())
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
		assert.Equal(t, ErrUnknownProtocol("unsupported").Error(), err.Error())
	})
}

func TestProtocolMarshalUnmarshal(t *testing.T) {

	var err error
	// var TestProto = Protocol(BEAST)
	var JSONbytes []byte

	type TestStruct struct {
		Proto Protocol
	}

	tsToMarshal := TestStruct{
		Proto: BEAST,
	}

	t.Run("MarshalText", func(t *testing.T) {
		p := Protocol(BEAST)
		b, err := p.MarshalText()
		require.NoError(t, err)
		assert.Equal(t, ProtocolNameBEAST, string(b))
	})

	t.Run("MarshalJSON working", func(t *testing.T) {
		JSONbytes, err = json.Marshal(tsToMarshal)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Proto":"BEAST"}`, string(JSONbytes))
	})

	t.Run("UnmarshalJSON bad JSON", func(t *testing.T) {
		p := Protocol(0)
		err := p.UnmarshalJSON([]byte(`BEAST"`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid")
	})

	t.Run("UnmarshalJSON unknown protocol", func(t *testing.T) {
		tsFromUnmarshal := TestStruct{}
		err := json.Unmarshal([]byte(`{"Proto":"telnet"}`), &tsFromUnmarshal)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown protocol")
	})

	t.Run("UnmarshalJSON working", func(t *testing.T) {
		tsFromUnmarshal := TestStruct{}
		err := json.Unmarshal(JSONbytes, &tsFromUnmarshal)
		require.NoError(t, err)
		assert.Equal(t, tsToMarshal, tsFromUnmarshal)
	})
}

func TestErrUnknownProtocol(t *testing.T) {
	err := ErrUnknownProtocol("BLORT")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown protocol: BLORT")
}
