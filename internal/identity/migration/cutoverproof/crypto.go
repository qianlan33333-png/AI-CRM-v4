package cutoverproof

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

const magic = "AICRM-CUTOVER-IDENTITY-PROOF-V1\x00"

func protectedRead(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 32<<20 {
		return nil, errors.New("proof input must be regular 0600 and bounded")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, errors.New("read protected proof input")
	}
	return b, nil
}
func keyCipher(path string) (cipher.AEAD, error) {
	b, e := protectedRead(path)
	if e != nil {
		return nil, e
	}
	key, e := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if e != nil || len(key) != 32 {
		return nil, errors.New("proof key must be 32-byte base64")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, ErrInvalid
	}
	return cipher.NewGCM(block)
}
func Seal(s Snapshot, path, keyPath string) ([32]byte, error) {
	d, e := s.Digest()
	if e != nil {
		return d, e
	}
	a, e := keyCipher(keyPath)
	if e != nil {
		return d, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return d, errors.New("proof random nonce")
	}
	plain, _ := json.Marshal(s)
	output := append([]byte(magic), nonce...)
	output = a.Seal(output, nonce, plain, []byte(magic))
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return d, errors.New("create protected proof")
	}
	defer f.Close()
	if _, e = f.Write(output); e != nil {
		return d, errors.New("write protected proof")
	}
	if e = f.Sync(); e != nil {
		return d, errors.New("sync protected proof")
	}
	return d, nil
}
func Load(path, keyPath string) (Snapshot, [32]byte, error) {
	var s Snapshot
	var d [32]byte
	a, e := keyCipher(keyPath)
	if e != nil {
		return s, d, e
	}
	b, e := protectedRead(path)
	if e != nil {
		return s, d, e
	}
	if len(b) < len(magic)+a.NonceSize()+a.Overhead() || string(b[:len(magic)]) != magic {
		return s, d, ErrInvalid
	}
	b = b[len(magic):]
	plain, e := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte(magic))
	if e != nil {
		return s, d, errors.New("proof authentication failed")
	}
	if e = json.Unmarshal(plain, &s); e != nil {
		return s, d, ErrInvalid
	}
	d, e = s.Digest()
	return s, d, e
}
