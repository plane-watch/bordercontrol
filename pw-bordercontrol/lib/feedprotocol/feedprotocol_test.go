package feedprotocol

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
}
