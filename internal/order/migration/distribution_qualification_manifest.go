package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

// QualificationSchemaVersion is a deliberately separate snapshot contract.
// The ordinary commerce-history input often has no local product ID/type, so
// it cannot be guessed into promotion evidence by the general order importer.
const QualificationSchemaVersion = "aicrm-order-distribution-qualification-v1"

var ErrInvalidQualificationManifest = errors.New("invalid distribution qualification manifest")

// QualificationManifest is an operator-reviewed mapping from an already
// imported historical order item to the one unified local Product identity.
// Every row is separately bound to the immutable historical order digest;
// caller-provided notes and generic ownership data do not appear here.
type QualificationManifest struct {
	SchemaVersion string                     `json:"schema_version"`
	RunKey        string                     `json:"run_key"`
	SourceSystem  string                     `json:"source_system"`
	SnapshotAt    time.Time                  `json:"snapshot_at"`
	Rows          []QualificationEvidenceRow `json:"rows"`
	Digest        [32]byte                   `json:"-"`
}

type QualificationEvidenceRow struct {
	OrderID               int64     `json:"order_id"`
	OrderItemLine         int32     `json:"order_item_line"`
	ProductID             int64     `json:"product_id"`
	ProductType           string    `json:"product_type"`
	SourceProductCode     string    `json:"source_product_code"`
	PayerCustomerID       int64     `json:"payer_customer_id"`
	BeneficiaryCustomerID int64     `json:"beneficiary_customer_id"`
	ItemPaidMinor         int64     `json:"item_paid_minor"`
	PaymentConfirmedAt    time.Time `json:"payment_confirmed_at"`
	// OrderSourceDigest is the SHA-256 digest of the historical commerce Order
	// row already committed in orders.source_row_digest. It prevents a mapping
	// from being applied to a different legacy order merely because its current
	// customer/amount happens to match.
	OrderSourceDigest string `json:"order_source_digest"`
}

func LoadQualificationManifest(path string) (QualificationManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return QualificationManifest{}, err
	}
	return ParseQualificationManifest(raw)
}

func ParseQualificationManifest(raw []byte) (QualificationManifest, error) {
	var manifest QualificationManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return QualificationManifest{}, ErrInvalidQualificationManifest
	}
	manifest.Digest = sha256.Sum256(raw)
	if err := manifest.Validate(); err != nil {
		return QualificationManifest{}, err
	}
	return manifest, nil
}

func (manifest QualificationManifest) Validate() error {
	if manifest.SchemaVersion != QualificationSchemaVersion || !qualificationText(manifest.RunKey, 1, 200) ||
		!qualificationText(manifest.SourceSystem, 1, 100) || manifest.SnapshotAt.IsZero() ||
		len(manifest.Rows) < 1 || len(manifest.Rows) > 20_000 {
		return ErrInvalidQualificationManifest
	}
	seen := make(map[string]struct{}, len(manifest.Rows))
	for _, row := range manifest.Rows {
		if row.OrderID < 1 || row.OrderItemLine < 1 || row.ProductID < 1 || row.PayerCustomerID < 1 ||
			row.BeneficiaryCustomerID < 1 || row.ItemPaidMinor < 1 ||
			(row.ProductType != "standard_product" && row.ProductType != "service_period") ||
			!qualificationText(row.SourceProductCode, 1, 200) || row.PaymentConfirmedAt.IsZero() ||
			!validSHA256Reference(row.OrderSourceDigest) {
			return ErrInvalidQualificationManifest
		}
		key := decimalKey(row.OrderID) + ":" + decimalKey(int64(row.OrderItemLine))
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidQualificationManifest
		}
		seen[key] = struct{}{}
	}
	return nil
}

// Evidence returns the database-safe fact for one row. Its SourceDigest is
// computed by this parser from the exact protected manifest metadata and row;
// it is never accepted as a free-form CLI field.
func (manifest QualificationManifest) Evidence(row QualificationEvidenceRow) (orderport.HistoricalQualificationEvidence, error) {
	if err := manifest.Validate(); err != nil {
		return orderport.HistoricalQualificationEvidence{}, err
	}
	var sourceOrder [32]byte
	decoded, err := hex.DecodeString(strings.TrimPrefix(row.OrderSourceDigest, "sha256:"))
	if err != nil || len(decoded) != len(sourceOrder) {
		return orderport.HistoricalQualificationEvidence{}, ErrInvalidQualificationManifest
	}
	copy(sourceOrder[:], decoded)
	// Struct JSON fixes the digest input's field order. This binds the type and
	// unified local product ID to the reviewed mapping, the order source digest,
	// the exact subject claims, paid amount, and actual confirmation time.
	payload, err := json.Marshal(struct {
		SchemaVersion, RunKey, SourceSystem string
		SnapshotAt                          time.Time
		Row                                 QualificationEvidenceRow
	}{manifest.SchemaVersion, manifest.RunKey, manifest.SourceSystem, manifest.SnapshotAt.UTC(), row})
	if err != nil {
		return orderport.HistoricalQualificationEvidence{}, ErrInvalidQualificationManifest
	}
	evidence := orderport.HistoricalQualificationEvidence{
		OrderID:               row.OrderID,
		OrderItemLine:         row.OrderItemLine,
		ProductID:             row.ProductID,
		ProductType:           row.ProductType,
		SourceProductCode:     row.SourceProductCode,
		PayerCustomerID:       row.PayerCustomerID,
		BeneficiaryCustomerID: row.BeneficiaryCustomerID,
		ItemPaidMinor:         row.ItemPaidMinor,
		PaymentConfirmedAt:    row.PaymentConfirmedAt.UTC(),
		SourceOrderDigest:     sourceOrder,
		SourceDigest:          sha256.Sum256(payload),
	}
	if !evidence.Valid() {
		return orderport.HistoricalQualificationEvidence{}, ErrInvalidQualificationManifest
	}
	return evidence, nil
}

func (manifest QualificationManifest) DigestHex() string {
	return hex.EncodeToString(manifest.Digest[:])
}

func (manifest QualificationManifest) Summary() map[string]any {
	return map[string]any{"rows": len(manifest.Rows), "source_system": manifest.SourceSystem, "snapshot_at": manifest.SnapshotAt.UTC()}
}

func qualificationText(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum && strings.TrimSpace(value) == value
}

func validSHA256Reference(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func decimalKey(value int64) string {
	return strconv.FormatInt(value, 10)
}
