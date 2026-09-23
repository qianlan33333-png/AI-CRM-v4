package port

import (
	"context"
	"encoding/json"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

// MessageArchiveReader is the narrow, read-only WeCom archive boundary.  It
// is implemented by the WeCom adapter and may use the local SDK runner; it
// never exposes a Provider write, database handle, customer ID, or scheduler.
type MessageArchiveReader interface {
	ArchiveHealth(context.Context) (ArchiveHealth, error)
	GetChatData(context.Context, uint64, uint32) ([]EncryptedArchiveRecord, error)
	DecryptArchiveData(context.Context, []EncryptedArchiveRecord) ([]PlainArchiveRecord, error)
	GetArchiveMedia(context.Context, ArchiveMediaRequest) (ArchiveMediaChunk, error)
}

type ArchiveHealth struct {
	RunnerAvailable bool
	LibraryLoadable bool
	InitOK          bool
	ErrorCode       string
}

type EncryptedArchiveRecord struct {
	Seq              uint64 `json:"seq"`
	MsgID            string `json:"msgid"`
	PublicKeyVersion uint32 `json:"publickey_ver"`
	EncryptedKey     string `json:"encrypt_random_key"`
	EncryptedMessage string `json:"encrypt_chat_msg"`
}

// PlainArchiveRecord is produced only after a trusted SDK reader has fetched
// and decrypted a record. Payload stays in the private archive boundary and
// is never suitable for request logging.
type PlainArchiveRecord struct {
	Seq                uint64                           `json:"seq"`
	MsgID              string                           `json:"msgid"`
	Payload            json.RawMessage                  `json:"payload"`
	ExternalIdentities []TrustedArchiveExternalIdentity `json:"-"`
}

// TrustedArchiveExternalIdentity is attached by the WeCom SDK adapter while
// handling this exact decrypted provider record.  It prevents the archive
// domain (and especially HTTP or import DTOs) from promoting an arbitrary
// string into a verified identity input.
type TrustedArchiveExternalIdentity struct {
	Value string
	Fact  identitydomain.VerifiedFact
}

type ArchiveMediaRequest struct {
	FileID   string
	IndexBuf string
}

type ArchiveMediaChunk struct {
	Data         []byte
	NextIndexBuf string
	Finished     bool
}
