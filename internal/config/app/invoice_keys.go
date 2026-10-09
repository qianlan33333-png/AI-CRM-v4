package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

var ErrInvoiceKeyInvalid = errors.New("invalid invoice APIv3 key")

// InvoiceKeyFiles stages immutable private files. Uploading never rotates a
// live credential. Only a published Config reference consumed at startup does.
// Values and paths are never returned by the admin API or stored in Config.
type InvoiceKeyFiles struct{ Directory string }

func (f InvoiceKeyFiles) StageInvoiceKey(_ context.Context, key string) (string, error) {
	if len(key) != 32 || strings.Trim(key, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" {
		return "", ErrInvoiceKeyInvalid
	}
	if f.Directory == "" {
		return "", ErrInvoiceKeyInvalid
	}
	if e := os.MkdirAll(f.Directory, 0700); e != nil {
		return "", e
	}
	info, e := os.Lstat(f.Directory)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", ErrInvoiceKeyInvalid
	}
	root, e := os.OpenRoot(f.Directory)
	if e != nil {
		return "", e
	}
	defer root.Close()
	random := make([]byte, 16)
	if _, e = rand.Read(random); e != nil {
		return "", e
	}
	id := hex.EncodeToString(random)
	file, e := root.OpenFile(id+".key", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	_, e = io.WriteString(file, key)
	if e == nil {
		e = file.Sync()
	}
	ce := file.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		_ = root.Remove(id + ".key")
		return "", e
	}
	return id, nil
}
func (f InvoiceKeyFiles) Read(id string) (string, error) {
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", ErrInvoiceKeyInvalid
	}
	info, e := os.Lstat(f.Directory)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", ErrInvoiceKeyInvalid
	}
	root, e := os.OpenRoot(f.Directory)
	if e != nil {
		return "", e
	}
	defer root.Close()
	info, e = root.Lstat(id + ".key")
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", ErrInvoiceKeyInvalid
	}
	file, e := root.Open(id + ".key")
	if e != nil {
		return "", e
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(file, 33))
	if e != nil || len(raw) != 32 || strings.Trim(string(raw), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" {
		return "", ErrInvoiceKeyInvalid
	}
	return string(raw), nil
}
