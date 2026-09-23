package wecom

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

const stateDigestPrefix = "hmac-sha256:"

var (
	// ErrStateDigestUnavailable is returned before Inbox construction when a
	// callback carries State but no trusted digester was injected.
	ErrStateDigestUnavailable = errors.New("wecom state digester unavailable")
	ErrInvalidStateDigester   = errors.New("invalid wecom state digester")
)

// StateDigester converts a provider State into a fixed-size, scoped digest.
// Implementations must not return the input or persist it elsewhere. The
// callback handler calls this interface before it constructs the durable
// CallbackEvent payload.
type StateDigester interface {
	DigestState(corpID, state string) ([32]byte, error)
}

// HMACStateDigester is the default protocol adapter for channel-code State.
// A keyed digest prevents an observer of the Inbox from testing guessed State
// values offline. The corp ID and callback protocol version are included in
// the MAC domain so a digest cannot be replayed across enterprises or parser
// versions.
type HMACStateDigester struct {
	key []byte
}

var _ StateDigester = (*HMACStateDigester)(nil)

// NewHMACStateDigester copies key and requires at least a 256-bit secret.
func NewHMACStateDigester(key []byte) (*HMACStateDigester, error) {
	if len(key) < sha256.Size {
		return nil, ErrInvalidStateDigester
	}
	return &HMACStateDigester{key: append([]byte(nil), key...)}, nil
}

func (digester *HMACStateDigester) DigestState(corpID, state string) ([32]byte, error) {
	var zero [32]byte
	if digester == nil || len(digester.key) < sha256.Size || !validCorpID(corpID) || !validOptionalCallbackValue(state) {
		return zero, ErrInvalidStateDigester
	}
	mac := hmac.New(sha256.New, digester.key)
	_, _ = mac.Write([]byte("aicrm:wecom:state:"))
	_, _ = mac.Write([]byte(callbackProtocolVersion))
	if err := writeStateDigestPart(mac, corpID); err != nil {
		return zero, err
	}
	if err := writeStateDigestPart(mac, state); err != nil {
		return zero, err
	}
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest, nil
}

func writeStateDigestPart(mac interface{ Write([]byte) (int, error) }, value string) error {
	if uint64(len(value)) > uint64(^uint32(0)) {
		return ErrInvalidStateDigester
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	if _, err := mac.Write(length[:]); err != nil {
		return err
	}
	_, err := mac.Write([]byte(value))
	return err
}

func formatStateDigest(digest [32]byte) string {
	return stateDigestPrefix + hex.EncodeToString(digest[:])
}

// ParseStateDigest decodes the safe representation carried by CallbackEvent
// into the fixed-size value expected by the channel resolver.
func ParseStateDigest(value string) ([32]byte, error) {
	var digest [32]byte
	if len(value) != len(stateDigestPrefix)+sha256.Size*2 || value[:len(stateDigestPrefix)] != stateDigestPrefix {
		return digest, ErrInvalidStateDigester
	}
	decoded, err := hex.DecodeString(value[len(stateDigestPrefix):])
	if err != nil || len(decoded) != sha256.Size {
		return digest, ErrInvalidStateDigester
	}
	copy(digest[:], decoded)
	return digest, nil
}
