// Package oaproof retains a protected, live signed Payment response proof for
// scoped OA identity migration. SourceRef is opaque; never a UnionID fact.
package oaproof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	d "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid scoped OA proof")

type Row struct {
	SourceRef       string `json:"source_ref"`
	MerchantOrderNo string `json:"merchant_order_no"`
	AmountMinor     int64  `json:"amount_minor"`
	AppID           string `json:"app_id"`
	OpenID          string `json:"open_id"`
	EvidenceDigest  string `json:"evidence_digest"`
}
type Snapshot struct {
	Version      int       `json:"version"`
	SourceSHA256 string    `json:"source_sha256"`
	VerifiedAt   time.Time `json:"verified_at"`
	Rows         []Row     `json:"rows"`
}

func (s Snapshot) Digest() ([32]byte, error) {
	if b, e := hex.DecodeString(s.SourceSHA256); e != nil || len(b) != 32 {
		return [32]byte{}, ErrInvalid
	}
	if s.Version != 1 || len(s.SourceSHA256) != 64 || s.VerifiedAt.IsZero() || len(s.Rows) != 3 {
		return [32]byte{}, ErrInvalid
	}
	refs := map[string]string{}
	orders := map[string]bool{}
	for _, r := range s.Rows {
		if r.SourceRef == "" || r.MerchantOrderNo == "" || orders[r.MerchantOrderNo] || r.AmountMinor < 1 || r.OpenID == "" || d.ValidateNamespace(d.KindOAOpenID, "wechat-app:"+r.AppID) != nil || !strings.HasPrefix(r.EvidenceDigest, "sha256:") || len(r.EvidenceDigest) != 71 {
			return [32]byte{}, ErrInvalid
		}
		if b, e := hex.DecodeString(strings.TrimPrefix(r.EvidenceDigest, "sha256:")); e != nil || len(b) != 32 {
			return [32]byte{}, ErrInvalid
		}
		v := r.AppID + ":" + r.OpenID
		if refs[r.SourceRef] != "" && refs[r.SourceRef] != v {
			return [32]byte{}, ErrInvalid
		}
		refs[r.SourceRef] = v
		orders[r.MerchantOrderNo] = true
	}
	if len(refs) != 2 {
		return [32]byte{}, ErrInvalid
	}
	b, e := json.Marshal(s)
	return sha256.Sum256(b), e
}
func (s Snapshot) References() (map[string]d.Reference, error) {
	if _, e := s.Digest(); e != nil {
		return nil, e
	}
	out := map[string]d.Reference{}
	for _, r := range s.Rows {
		out[r.SourceRef] = d.Reference{Kind: d.KindOAOpenID, Scope: "wechat-app:" + r.AppID, Value: r.OpenID, Assurance: d.AssuranceVerified, Source: "provider-history:cutover-live-payment-oa-v1"}
	}
	return out, nil
}
