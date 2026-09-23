// Package credential owns local password and opaque browser credential handling.
package credential

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
	tokenBytes       = 32
)

var ErrInvalidPassword = errors.New("password does not satisfy local policy")

type PasswordHasher struct{}

func (PasswordHasher) Hash(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password salt: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	base64Encoding := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonIterations, argonParallelism,
		base64Encoding.EncodeToString(salt), base64Encoding.EncodeToString(digest)), nil
}

func (PasswordHasher) Verify(password, encoded string) bool {
	if len(password) == 0 || len(password) > 1024 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory uint64
	var iterations uint64
	var parallelism uint64
	for _, parameter := range strings.Split(parts[3], ",") {
		pair := strings.SplitN(parameter, "=", 2)
		if len(pair) != 2 {
			return false
		}
		value, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return false
		}
		switch pair[0] {
		case "m":
			memory = value
		case "t":
			iterations = value
		case "p":
			parallelism = value
		default:
			return false
		}
	}
	// Reject malicious database values before allocating memory or work.
	if memory < 8*1024 || memory > 256*1024 || iterations < 1 || iterations > 10 || parallelism < 1 || parallelism > 16 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false
	}
	candidate := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(candidate, expected) == 1
}

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 1024 {
		return ErrInvalidPassword
	}
	return nil
}

func IssueOpaque(prefix string) (string, [32]byte, error) {
	random := make([]byte, tokenBytes)
	if _, err := rand.Read(random); err != nil {
		return "", [32]byte{}, fmt.Errorf("issue credential: %w", err)
	}
	value := prefix + base64.RawURLEncoding.EncodeToString(random)
	return value, Digest(value), nil
}

func Digest(value string) [32]byte {
	return sha256.Sum256([]byte(value))
}

func Matches(value string, expected [32]byte) bool {
	candidate := Digest(value)
	return subtle.ConstantTimeCompare(candidate[:], expected[:]) == 1
}
