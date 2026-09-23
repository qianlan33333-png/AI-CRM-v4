// Package readshare defines immutable read-only capability metadata. Business
// data, authorization and storage remain with the owning domain.
package readshare

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
)

type Share struct {
	ID         int64           `json:"id"`
	ResourceID int64           `json:"resource_id"`
	Config     json.RawMessage `json:"config"`
	Fields     []string        `json:"fields"`
	Mode       string          `json:"mode"`
	Enabled    bool            `json:"enabled"`
	Version    int64           `json:"version"`
	Digest     []byte          `json:"-"`
}
type Store interface {
	ReadShareKey(context.Context) ([]byte, error)
	SaveReadShare(context.Context, Share) (Share, error)
	ReadShare(context.Context, []byte) (Share, error)
	ListReadShares(context.Context, int64) ([]Share, error)
}

func Token(key []byte, domain string, actor int64, requestKey string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("dashboard-share-v2\x00" + domain + "\x00" + strconv.FormatInt(actor, 10) + "\x00" + requestKey))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func Digest(token string) []byte { v := sha256.Sum256([]byte(token)); return v[:] }
func ValidFields(fields []string, allowed map[string]bool) bool {
	if len(fields) > len(allowed) {
		return false
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if !allowed[f] || seen[f] {
			return false
		}
		seen[f] = true
	}
	return true
}

// ProjectMetrics enforces the published metric selection on the server.
// Presentation is never trusted to hide an otherwise returned metric.
func ProjectMetrics(metrics map[string]any, presentation json.RawMessage) map[string]any {
	var value struct {
		Metrics *[]string `json:"metrics"`
	}
	if json.Unmarshal(presentation, &value) != nil || value.Metrics == nil {
		return metrics
	}
	out := map[string]any{}
	for _, key := range *value.Metrics {
		if count, ok := metrics[key]; ok {
			out[key] = count
		}
	}
	return out
}
