package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
)

type withinOrderImporter struct{ repository *orderstore.Repository }

func (a withinOrderImporter) ImportHistorical(ctx context.Context, c orderport.HistoricalImportCommand) (orderdomain.Snapshot, error) {
	order, err := orderdomain.Restore(c.Order)
	if err != nil {
		return orderdomain.Snapshot{}, err
	}
	saved, _, err := a.repository.Import(ctx, c.RunID, c.SourceDigest, order)
	return saved.Snapshot(), err
}
func loadDeltaPreconditions(path string, m ordermigration.Manifest) (map[string]orderport.HistoricalDeltaPrecondition, error) {
	fail := errors.New("invalid protected history delta preconditions")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fail
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fail
	}
	var proof struct {
		ManifestSHA256 string                                           `json:"manifest_sha256"`
		Orders         map[string]orderport.HistoricalDeltaPrecondition `json:"orders"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&proof) != nil || dec.Decode(&struct{}{}) != io.EOF || proof.ManifestSHA256 != hex.EncodeToString(m.Digest[:]) || len(proof.Orders) == 0 {
		return nil, fail
	}
	seen := map[string]bool{}
	for _, row := range m.Orders {
		seen[row.SourceKey] = true
	}
	for key, p := range proof.Orders {
		if !seen[key] || p.Version < 1 || p.SourceDigest == ([32]byte{}) {
			return nil, fail
		}
	}
	return proof.Orders, nil
}
