package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

const (
	profileDefaultLimit = 20
	profileMaximumLimit = 100
)

type profileCursorPayload struct {
	Version    int    `json:"v"`
	Section    string `json:"s"`
	CustomerID int64  `json:"c"`
	Watermark  string `json:"w"`
	AfterAt    string `json:"a"`
	AfterID    int64  `json:"i"`
	FilterHash string `json:"f"`
}

func profilePageQuery(rawLimit, cursor, section, filter string, customerID customerdomain.CustomerID, key []byte) (customerport.PageQuery, int, error) {
	limit := profileDefaultLimit
	if rawLimit != "" {
		value, err := strconv.Atoi(rawLimit)
		if err != nil || value < 1 || value > profileMaximumLimit || strconv.Itoa(value) != rawLimit {
			return customerport.PageQuery{}, 0, customerapp.ErrInvalidQuery
		}
		limit = value
	}
	query := customerport.PageQuery{Limit: limit + 1, Watermark: time.Now().UTC(), Filter: filter}
	if cursor == "" {
		return query, limit, nil
	}
	payload, err := decodeProfileCursor(cursor, key)
	if err != nil || payload.Section != section || payload.CustomerID != int64(customerID) || payload.FilterHash != profileFilterHash(section, customerID, filter) || payload.AfterID < 1 {
		return customerport.PageQuery{}, 0, customerapp.ErrInvalidCursor
	}
	query.Watermark, err = time.Parse(time.RFC3339Nano, payload.Watermark)
	if err != nil {
		return customerport.PageQuery{}, 0, customerapp.ErrInvalidCursor
	}
	query.AfterAt, err = time.Parse(time.RFC3339Nano, payload.AfterAt)
	if err != nil || query.AfterAt.After(query.Watermark) {
		return customerport.PageQuery{}, 0, customerapp.ErrInvalidCursor
	}
	query.AfterID = payload.AfterID
	return query, limit, nil
}

func nextProfileCursor(section string, customerID customerdomain.CustomerID, filter string, query customerport.PageQuery, at time.Time, id int64, key []byte) (string, error) {
	return encodeProfileCursor(profileCursorPayload{Version: 1, Section: section, CustomerID: int64(customerID),
		Watermark: query.Watermark.Format(time.RFC3339Nano), AfterAt: at.Format(time.RFC3339Nano), AfterID: id,
		FilterHash: profileFilterHash(section, customerID, filter)}, key)
}

func profileFilterHash(section string, customerID customerdomain.CustomerID, filter string) string {
	sum := sha256.Sum256([]byte(section + "\x00" + strconv.FormatInt(int64(customerID), 10) + "\x00" + filter))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func encodeProfileCursor(payload profileCursorPayload, key []byte) (string, error) {
	if len(key) < 32 {
		return "", customerapp.ErrInvalidCursor
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeProfileCursor(value string, key []byte) (profileCursorPayload, error) {
	if len(key) < 32 || len(value) > 2048 {
		return profileCursorPayload{}, customerapp.ErrInvalidCursor
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return profileCursorPayload{}, customerapp.ErrInvalidCursor
	}
	raw, err := decodeCanonicalProfileBase64(parts[0])
	if err != nil {
		return profileCursorPayload{}, customerapp.ErrInvalidCursor
	}
	signature, err := decodeCanonicalProfileBase64(parts[1])
	if err != nil {
		return profileCursorPayload{}, customerapp.ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return profileCursorPayload{}, customerapp.ErrInvalidCursor
	}
	var payload profileCursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil || payload.Version != 1 || payload.Section == "" || payload.CustomerID < 1 || payload.Watermark == "" || payload.AfterAt == "" || payload.FilterHash == "" {
		return profileCursorPayload{}, customerapp.ErrInvalidCursor
	}
	return payload, nil
}

func decodeCanonicalProfileBase64(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, customerapp.ErrInvalidCursor
	}
	return decoded, nil
}
