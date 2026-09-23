package source

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
)

const snapshotMagic = "AICRM-CONFIG-DEFINITIONS-V1\x00"

func ReadKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("read snapshot key")
	}
	// Require the exact owner-only mode.  A 0400 key would be safe from other
	// users but is not the documented key-file contract and often indicates a
	// provisioning mistake; accepting it would make deployment drift silent.
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("snapshot key must be a regular 0600 file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read snapshot key")
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("snapshot key must contain base64 encoded 32 bytes")
	}
	return key, nil
}

func SealToFile(snapshot Snapshot, path, keyPath string) ([32]byte, error) {
	plain, digest, err := snapshot.Canonical()
	if err != nil {
		return digest, err
	}
	key, err := ReadKey(keyPath)
	if err != nil {
		return digest, err
	}
	sealed, err := seal(key, plain)
	if err != nil {
		return digest, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return digest, errors.New("create encrypted snapshot")
	}
	defer file.Close()
	if _, err = file.Write(sealed); err != nil {
		return digest, errors.New("write encrypted snapshot")
	}
	if err = file.Sync(); err != nil {
		return digest, errors.New("sync encrypted snapshot")
	}
	return digest, nil
}

// Seal canonicalizes and authenticates a snapshot in memory.  Callers that
// need persistence should use SealToFile, which additionally enforces 0600 and
// O_EXCL.  The plaintext is never written by this package.
func Seal(snapshot Snapshot, key []byte) ([]byte, [32]byte, error) {
	plain, digest, err := snapshot.Canonical()
	if err != nil {
		return nil, digest, err
	}
	sealed, err := seal(key, plain)
	if err != nil {
		return nil, digest, err
	}
	return sealed, digest, nil
}

// Load authenticates and parses an in-memory encrypted snapshot.
func Load(sealed, key []byte) (Snapshot, [32]byte, error) {
	plain, err := open(key, sealed)
	if err != nil {
		return Snapshot{}, [32]byte{}, err
	}
	return Parse(plain)
}

func LoadFile(path, keyPath string) (Snapshot, [32]byte, error) {
	key, err := ReadKey(keyPath)
	if err != nil {
		return Snapshot{}, [32]byte{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return Snapshot{}, [32]byte{}, errors.New("encrypted snapshot must be a regular 0600 file")
	}
	sealed, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, [32]byte{}, errors.New("read encrypted snapshot")
	}
	plain, err := open(key, sealed)
	if err != nil {
		return Snapshot{}, [32]byte{}, err
	}
	return Parse(plain)
}

func seal(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	prefix := []byte(snapshotMagic)
	out := make([]byte, 0, len(prefix)+len(nonce)+len(plain)+aead.Overhead())
	out = append(out, prefix...)
	out = append(out, nonce...)
	out = append(out, aead.Seal(nil, nonce, plain, prefix)...)
	return out, nil
}

func open(key, sealed []byte) ([]byte, error) {
	prefix := []byte(snapshotMagic)
	if len(sealed) < len(prefix) || string(sealed[:len(prefix)]) != snapshotMagic {
		return nil, errors.New("invalid snapshot header")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid snapshot key")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("invalid snapshot cipher")
	}
	body := sealed[len(prefix):]
	if len(body) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid encrypted snapshot")
	}
	plain, err := aead.Open(nil, body[:aead.NonceSize()], body[aead.NonceSize():], prefix)
	if err != nil {
		return nil, errors.New("snapshot authentication failed")
	}
	return plain, nil
}
