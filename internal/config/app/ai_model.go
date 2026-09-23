package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"strings"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrAIModelInvalid = errors.New("invalid AI model settings")
var ErrAIModelConflict = errors.New("AI model settings changed")
var ErrAIModelUnavailable = errors.New("AI model settings unavailable")

type AIModelRecord struct {
	Provider, Model string
	Ciphertext      []byte
	Version         int64
}
type AIModelStore interface {
	ReadAIModelRecord(context.Context, bool) (AIModelRecord, error)
	SaveAIModelRecord(context.Context, AIModelRecord, int64, int64) error
}
type AIModelService struct {
	uow    platformport.UnitOfWork
	store  AIModelStore
	cipher cipher.AEAD
}

func NewAIModelService(uow platformport.UnitOfWork, store AIModelStore, key []byte) (*AIModelService, error) {
	if uow == nil || store == nil || len(key) < 32 {
		return nil, ErrAIModelInvalid
	}
	derive := hmac.New(sha256.New, key)
	derive.Write([]byte("aicrm.config.ai-model-key.v1"))
	block, err := aes.NewCipher(derive.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AIModelService{uow: uow, store: store, cipher: aead}, nil
}
func AIModelBaseURL(provider string) string {
	return map[string]string{"deepseek": "https://api.deepseek.com/v1", "qwen": "https://dashscope.aliyuncs.com/compatible-mode/v1", "glm": "https://open.bigmodel.cn/api/paas/v4", "kimi": "https://api.moonshot.cn/v1"}[provider]
}
func modelSelection(r AIModelRecord) configport.AIModelSelection {
	return configport.AIModelSelection{Provider: r.Provider, Model: r.Model, Configured: len(r.Ciphertext) > 0, Version: r.Version}
}
func (s *AIModelService) ReadAIModel(ctx context.Context) (configport.AIModelSelection, error) {
	var r AIModelRecord
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; r, e = s.store.ReadAIModelRecord(tx, false); return e })
	return modelSelection(r), err
}
func (s *AIModelService) SaveAIModel(ctx context.Context, c configport.AIModelCommand) (configport.AIModelSelection, error) {
	if c.ActorID < 1 || c.ExpectedVersion < 0 || AIModelBaseURL(c.Provider) == "" || c.Model == "" || len(c.Model) > 200 || c.Model != strings.TrimSpace(c.Model) || len(c.APIKey) > 8192 || strings.ContainsAny(c.Model+c.APIKey, "\x00\r\n") || c.APIKey != strings.TrimSpace(c.APIKey) {
		return configport.AIModelSelection{}, ErrAIModelInvalid
	}
	var result AIModelRecord
	err := s.uow.Within(ctx, func(tx context.Context) error {
		current, e := s.store.ReadAIModelRecord(tx, true)
		if e != nil {
			return e
		}
		if current.Version != c.ExpectedVersion {
			return ErrAIModelConflict
		}
		encrypted := current.Ciphertext
		if c.APIKey == "" {
			if len(encrypted) == 0 || current.Provider != c.Provider {
				return ErrAIModelInvalid
			}
		} else {
			nonce := make([]byte, s.cipher.NonceSize())
			if _, e = rand.Read(nonce); e != nil {
				return ErrAIModelUnavailable
			}
			encrypted = s.cipher.Seal(nonce, nonce, []byte(c.APIKey), []byte("ai-model:"+c.Provider))
		}
		result = AIModelRecord{Provider: c.Provider, Model: c.Model, Ciphertext: encrypted, Version: current.Version + 1}
		return s.store.SaveAIModelRecord(tx, result, current.Version, c.ActorID)
	})
	return modelSelection(result), err
}
func (s *AIModelService) ReadAIModelRuntime(ctx context.Context) (configport.AIModelRuntime, bool, error) {
	var r AIModelRecord
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; r, e = s.store.ReadAIModelRecord(tx, false); return e })
	if err != nil {
		return configport.AIModelRuntime{}, false, err
	}
	if r.Version == 0 {
		return configport.AIModelRuntime{}, false, nil
	}
	size := s.cipher.NonceSize()
	if len(r.Ciphertext) <= size {
		return configport.AIModelRuntime{}, false, ErrAIModelUnavailable
	}
	key, e := s.cipher.Open(nil, r.Ciphertext[:size], r.Ciphertext[size:], []byte("ai-model:"+r.Provider))
	if e != nil {
		return configport.AIModelRuntime{}, false, ErrAIModelUnavailable
	}
	return configport.AIModelRuntime{BaseURL: AIModelBaseURL(r.Provider), Model: r.Model, APIKey: string(key)}, true, nil
}
