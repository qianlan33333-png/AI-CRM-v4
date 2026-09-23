package adapter

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/archivesdk"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	wecomprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/provider"
)

var ErrArchiveUnavailable = errors.New("wecom archive SDK unavailable")

type MessageArchiveConfig struct {
	Enabled                                 bool
	CorpID, Secret, RunnerPath, LibraryPath string
	PrivateKeyPaths                         map[uint32]string
	Timeout                                 time.Duration
}
type MessageArchiveReader struct {
	config MessageArchiveConfig
	keys   map[uint32]*rsa.PrivateKey
	invoke func(context.Context, archivesdk.Request) (archivesdk.Response, error) // test-only child boundary fixture
}

var _ wecomport.MessageArchiveReader = (*MessageArchiveReader)(nil)

func NewMessageArchiveReader(config MessageArchiveConfig) (*MessageArchiveReader, error) {
	if !config.Enabled {
		return &MessageArchiveReader{config: config, keys: map[uint32]*rsa.PrivateKey{}}, nil
	}
	if invalid(config.CorpID) || invalid(config.Secret) || config.RunnerPath == "" || config.LibraryPath == "" || len(config.PrivateKeyPaths) == 0 {
		return nil, ErrArchiveUnavailable
	}
	if config.Timeout <= 0 || config.Timeout > time.Minute {
		return nil, ErrArchiveUnavailable
	}
	keys := map[uint32]*rsa.PrivateKey{}
	for version, path := range config.PrivateKeyPaths {
		if version == 0 || path == "" {
			return nil, ErrArchiveUnavailable
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, ErrArchiveUnavailable
		}
		key, err := parseArchivePrivateKey(raw)
		if err != nil {
			return nil, ErrArchiveUnavailable
		}
		keys[version] = key
	}
	config.PrivateKeyPaths = nil
	return &MessageArchiveReader{config: config, keys: keys}, nil
}
func parseArchivePrivateKey(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, ErrArchiveUnavailable
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrArchiveUnavailable
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, ErrArchiveUnavailable
	}
	return key, nil
}
func (r *MessageArchiveReader) ArchiveHealth(ctx context.Context) (wecomport.ArchiveHealth, error) {
	if r == nil || !r.config.Enabled {
		return wecomport.ArchiveHealth{ErrorCode: "disabled"}, ErrArchiveUnavailable
	}
	response, err := r.call(ctx, archivesdk.Request{Operation: "health"})
	if err != nil || response.ErrorCode != "" {
		return wecomport.ArchiveHealth{ErrorCode: "sdk_unavailable"}, ErrArchiveUnavailable
	}
	return wecomport.ArchiveHealth{RunnerAvailable: true, LibraryLoadable: response.LibraryLoadable, InitOK: false}, nil
}
func (r *MessageArchiveReader) GetChatData(ctx context.Context, seq uint64, limit uint32) ([]wecomport.EncryptedArchiveRecord, error) {
	if r == nil || !r.config.Enabled || limit < 1 || limit > 1000 {
		return nil, ErrArchiveUnavailable
	}
	response, err := r.call(ctx, archivesdk.Request{Operation: "fetch", Seq: seq, Limit: limit})
	if err != nil || response.ErrorCode != "" {
		return nil, ErrArchiveUnavailable
	}
	var envelope struct {
		ErrCode  int                                `json:"errcode"`
		ChatData []wecomport.EncryptedArchiveRecord `json:"chatdata"`
	}
	if json.Unmarshal(response.Data, &envelope) != nil || envelope.ErrCode != 0 || len(envelope.ChatData) > int(limit) {
		return nil, ErrArchiveUnavailable
	}
	for _, item := range envelope.ChatData {
		if item.Seq == 0 || item.MsgID == "" || item.EncryptedKey == "" || item.EncryptedMessage == "" || item.PublicKeyVersion == 0 {
			return nil, ErrArchiveUnavailable
		}
	}
	return envelope.ChatData, nil
}
func (r *MessageArchiveReader) DecryptArchiveData(ctx context.Context, encrypted []wecomport.EncryptedArchiveRecord) ([]wecomport.PlainArchiveRecord, error) {
	if r == nil || !r.config.Enabled || len(encrypted) > 1000 {
		return nil, ErrArchiveUnavailable
	}
	items := make([]archivesdk.DecryptItem, 0, len(encrypted))
	for _, item := range encrypted {
		key := r.keys[item.PublicKeyVersion]
		if key == nil {
			return nil, ErrArchiveUnavailable
		}
		wrapped, err := base64.StdEncoding.DecodeString(item.EncryptedKey)
		if err != nil {
			return nil, ErrArchiveUnavailable
		}
		randomKey, decryptErr := rsa.DecryptPKCS1v15(rand.Reader, key, wrapped)
		if decryptErr != nil || len(randomKey) == 0 {
			return nil, ErrArchiveUnavailable
		}
		items = append(items, archivesdk.DecryptItem{DecryptKey: string(randomKey), EncryptedMessage: item.EncryptedMessage})
	}
	// One bounded page maps to one short-lived child. The child loads the SDK
	// once and decrypts every record before it exits; a failure rejects the
	// whole page and therefore leaves the durable cursor unchanged.
	response, err := r.call(ctx, archivesdk.Request{Operation: "decrypt_batch", DecryptItems: items})
	if err != nil || response.ErrorCode != "" || len(response.Items) != len(encrypted) {
		return nil, ErrArchiveUnavailable
	}
	out := make([]wecomport.PlainArchiveRecord, 0, len(encrypted))
	for index, payload := range response.Items {
		if !json.Valid(payload) {
			return nil, ErrArchiveUnavailable
		}
		identities, identityErr := r.trustedExternalIdentities(payload)
		if identityErr != nil {
			return nil, identityErr
		}
		out = append(out, wecomport.PlainArchiveRecord{Seq: encrypted[index].Seq, MsgID: encrypted[index].MsgID, Payload: append(json.RawMessage(nil), payload...), ExternalIdentities: identities})
	}
	return out, nil
}
func (r *MessageArchiveReader) GetArchiveMedia(ctx context.Context, request wecomport.ArchiveMediaRequest) (wecomport.ArchiveMediaChunk, error) {
	if r == nil || !r.config.Enabled || strings.TrimSpace(request.FileID) != request.FileID || request.FileID == "" || strings.TrimSpace(request.IndexBuf) != request.IndexBuf {
		return wecomport.ArchiveMediaChunk{}, ErrArchiveUnavailable
	}
	response, err := r.call(ctx, archivesdk.Request{Operation: "media", FileID: request.FileID, IndexBuf: request.IndexBuf})
	if err != nil || response.ErrorCode != "" {
		return wecomport.ArchiveMediaChunk{}, ErrArchiveUnavailable
	}
	return wecomport.ArchiveMediaChunk{Data: append([]byte(nil), response.Data...), NextIndexBuf: response.NextIndexBuf, Finished: response.Finished}, nil
}
func (r *MessageArchiveReader) call(ctx context.Context, request archivesdk.Request) (archivesdk.Response, error) {
	deadline := r.config.Timeout
	if deadline <= 0 {
		deadline = 15 * time.Second
	}
	child, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	request.LibraryPath = r.config.LibraryPath
	request.CorpID = r.config.CorpID
	request.Secret = r.config.Secret
	if r.invoke != nil {
		return r.invoke(child, request)
	}
	return archivesdk.Call(child, r.config.RunnerPath, request)
}
func (r *MessageArchiveReader) trustedExternalIdentities(payload []byte) ([]wecomport.TrustedArchiveExternalIdentity, error) {
	var envelope struct {
		From   string   `json:"from"`
		ToList []string `json:"tolist"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return nil, ErrArchiveUnavailable
	}
	values := append([]string{envelope.From}, envelope.ToList...)
	seen := map[string]bool{}
	out := []wecomport.TrustedArchiveExternalIdentity{}
	for _, value := range values {
		if !(strings.HasPrefix(value, "wm") || strings.HasPrefix(value, "wo")) || seen[value] {
			continue
		}
		seen[value] = true
		fact, err := wecomprovider.VerifiedExternalContact(r.config.CorpID, value, "wecom.message_archive")
		if err != nil {
			return nil, ErrArchiveUnavailable
		}
		out = append(out, wecomport.TrustedArchiveExternalIdentity{Value: value, Fact: fact})
	}
	return out, nil
}
