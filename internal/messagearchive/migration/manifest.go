// Package migration validates an explicit, offline historical archive snapshot.
// It has no client for an old service or database and never manufactures OneID
// facts from the snapshot's participant strings.
package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"

	archiveapp "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

const SchemaVersion = "aicrm-message-archive-history-v1"

var ErrInvalidManifest = errors.New("invalid message archive migration manifest")

type SourceRow struct {
	SourceRowKey        string          `json:"source_row_key"`
	Seq                 uint64          `json:"seq"`
	MsgID               string          `json:"msgid"`
	Payload             json.RawMessage `json:"payload"`
	SourcePayloadDigest string          `json:"source_payload_digest,omitempty"`
	HistoricalUnionID   string          `json:"historical_unionid,omitempty"`
	HistoricalGroupName string          `json:"historical_group_name,omitempty"`
}

type Manifest struct {
	SchemaVersion string            `json:"schema_version"`
	SourceName    string            `json:"source_name"`
	CorpScope     string            `json:"corp_scope"`
	Records       []SourceRow       `json:"records"`
	Digest        [sha256.Size]byte `json:"-"`
}

type Summary struct {
	Records int            `json:"records"`
	ByType  map[string]int `json:"by_type"`
}

func Load(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	return Parse(raw)
}

func Parse(raw []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return Manifest{}, ErrInvalidManifest
	}
	manifest.Digest = sha256.Sum256(raw)
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != SchemaVersion || !label(manifest.SourceName, 120) || !strings.HasPrefix(manifest.CorpScope, "wecom-corp:") || len(manifest.CorpScope) <= len("wecom-corp:") || len(manifest.Records) == 0 {
		return ErrInvalidManifest
	}
	keys, messages, sequences := map[string]struct{}{}, map[string]struct{}{}, map[uint64]struct{}{}
	for _, row := range manifest.Records {
		if !label(row.SourceRowKey, 512) || row.Seq == 0 || !label(row.MsgID, 512) || !json.Valid(row.Payload) || !validSourcePayloadDigest(row.SourcePayloadDigest) {
			return ErrInvalidManifest
		}
		if _, found := keys[row.SourceRowKey]; found {
			return ErrInvalidManifest
		}
		if _, found := messages[row.MsgID]; found {
			return ErrInvalidManifest
		}
		if _, found := sequences[row.Seq]; found {
			return ErrInvalidManifest
		}
		if _, err := manifest.HistoricalProjection(row); err != nil {
			return ErrInvalidManifest
		}
		message, err := archiveapp.NormalizeArchiveRecord(manifest.CorpScope, wecomport.PlainArchiveRecord{Seq: row.Seq, MsgID: row.MsgID, Payload: row.Payload})
		if err != nil || !message.Valid() {
			return ErrInvalidManifest
		}
		keys[row.SourceRowKey], messages[row.MsgID], sequences[row.Seq] = struct{}{}, struct{}{}, struct{}{}
	}
	return nil
}

// HistoricalProjection returns the two values that the frozen legacy external
// archive projection took from each archived row. They stay Archive-owned facts
// and never become current OneID evidence or a group-directory lookup.
type HistoricalProjection struct {
	UnionID   string
	GroupName string
}

// validSourcePayloadDigest permits older normalized operator manifests, while
// snapshots extracted from archived_messages must preserve a digest of the
// complete protected source wrapper. The wrapper is not copied to V3.
func validSourcePayloadDigest(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func (manifest Manifest) HistoricalProjection(row SourceRow) (HistoricalProjection, error) {
	projection := HistoricalProjection{UnionID: strings.TrimSpace(row.HistoricalUnionID), GroupName: strings.TrimSpace(row.HistoricalGroupName)}
	if len(projection.UnionID) > 1024 || len(projection.GroupName) > 512 {
		return HistoricalProjection{}, ErrInvalidManifest
	}
	return projection, nil
}

// Normalized keeps the parser's participant categories but never promotes an
// offline value into an identity fact. The import command may subsequently
// read an already-verified OneID association through the Identity Port; it
// cannot create, attach, or upgrade that association from this snapshot.
func (manifest Manifest) Normalized(row SourceRow) (domain.Message, error) {
	message, err := archiveapp.NormalizeArchiveRecord(manifest.CorpScope, wecomport.PlainArchiveRecord{Seq: row.Seq, MsgID: row.MsgID, Payload: row.Payload})
	if err != nil {
		return domain.Message{}, err
	}
	if !message.Valid() {
		return domain.Message{}, ErrInvalidManifest
	}
	return message, nil
}

func (manifest Manifest) Summary() Summary {
	result := Summary{Records: len(manifest.Records), ByType: map[string]int{}}
	for _, row := range manifest.Records {
		message, err := manifest.Normalized(row)
		if err == nil {
			result.ByType[message.MessageType]++
		}
	}
	return result
}

func (manifest Manifest) SortedRecords() []SourceRow {
	values := append([]SourceRow(nil), manifest.Records...)
	sort.Slice(values, func(i, j int) bool { return values[i].Seq < values[j].Seq })
	return values
}

func label(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value
}
