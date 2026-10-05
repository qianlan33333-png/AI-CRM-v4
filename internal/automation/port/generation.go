package port

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

// GenerationContext is frozen before an AI generation effect is accepted. It
// intentionally has the product-approved sources, including the database customer name; adapters may use
// their owning read ports, but Completion must never re-read any of them.
type GenerationContext struct {
	CustomerName  string `json:"customer_name,omitempty"`
	Questionnaire string `json:"questionnaire"`
	RecentChats   string `json:"recent_chats"`
	Tags          string `json:"tags"`
	Activation    string `json:"activation"`
	// PolicyDelivery is an opaque, frozen automatic-send continuation. It is
	// never included in model messages; manual runs leave it nil.
	PolicyDelivery *GeneratedPolicyDelivery `json:"policy_delivery,omitempty"`
}

type GeneratedPolicyDelivery struct {
	EnrollmentID int64 `json:"enrollment_id"`
	RecipientID  int64 `json:"recipient_id"`
	// Byte encoding preserves the exact opaque snapshot through JSONB; a
	// nested RawMessage would be reformatted and invalidate its digest.
	ContentTemplate []byte          `json:"content_template"`
	TemplateDigest  [32]byte        `json:"template_digest"`
	PolicyDigest    [32]byte        `json:"policy_digest"`
	QuietHours      json.RawMessage `json:"quiet_hours"`
}

func (c GenerationContext) Valid() bool {
	for _, value := range []string{c.CustomerName, c.Questionnaire, c.RecentChats, c.Tags, c.Activation} {
		if len(value) > 16000 {
			return false
		}
	}
	if d := c.PolicyDelivery; d != nil {
		if d.EnrollmentID < 1 || d.RecipientID < 1 || !json.Valid(d.ContentTemplate) || sha256.Sum256(d.ContentTemplate) != d.TemplateDigest || d.PolicyDigest == ([32]byte{}) || !json.Valid(d.QuietHours) {
			return false
		}
	}
	return true
}

// ValidGeneratedText rejects malformed model output before it becomes a human
// review candidate. It deliberately checks only the frozen prompts and prompt
// delimiters, never re-reading customer context.
func ValidGeneratedText(value, rolePrompt, taskPrompt string) bool {
	text := strings.TrimSpace(value)
	if text == "" || utf8.RuneCountInString(text) > 8000 || strings.Contains(text, "{{") || strings.Contains(text, "}}") {
		return false
	}
	for _, marker := range []string{"【用户名】", "【问卷】", "【最近20条聊天】", "【用户标签】", "【激活信息】"} {
		if strings.Contains(text, marker) {
			return false
		}
	}
	return !obviousPromptEcho(text, rolePrompt) && !obviousPromptEcho(text, taskPrompt)
}

func obviousPromptEcho(text, prompt string) bool {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return false
	}
	if text == prompt {
		return true
	}
	// Longer prompt content included in a response is an obvious role/task
	// reflection. Short phrases remain valid so ordinary wording is not
	// falsely excluded.
	if utf8.RuneCountInString(prompt) < 24 {
		return false
	}
	normalize := func(raw string) string { return strings.Join(strings.Fields(strings.ToLower(raw)), "") }
	return strings.Contains(normalize(text), normalize(prompt))
}

// GenerationContextReader is a composition-provided, read-only aggregation
// seam. It has no identity resolve/provision or Provider-write capability.
type GenerationContextReader interface {
	FreezeGenerationContext(context.Context, customerdomain.CustomerID) (GenerationContext, error)
}

type GenerationModelPolicy struct {
	Mode        string  `json:"mode"`
	Endpoint    string  `json:"endpoint"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
}

func (p GenerationModelPolicy) Valid() bool {
	return p.Mode != "" && len(p.Mode) <= 40 && p.Endpoint != "" && len(p.Endpoint) <= 2000 && p.Model != "" && len(p.Model) <= 200 && p.Temperature == 0.4
}

// GenerationModelPolicyReader returns a secret-free policy summary. The
// provider owns endpoints and credentials separately; this record exists only
// to make the accepted generation explainable and reproducible.
type GenerationModelPolicyReader interface {
	GenerationModelPolicy(context.Context) (GenerationModelPolicy, error)
}

type PublishedGeneration struct {
	AgentID          AgentID
	PublishedVersion int64
	AgentCode        string
	RolePrompt       string
	TaskPrompt       string
	Materials        FixedContentPackage
	ContentDigest    [32]byte
	MaterialsDigest  [32]byte
}

func (p PublishedGeneration) Valid() bool {
	return p.AgentID > 0 && p.PublishedVersion > 0 && p.AgentCode != "" && len(p.AgentCode) <= 120 && p.RolePrompt != "" && p.TaskPrompt != ""
}

type PublishedGenerationReader interface {
	PublishedGeneration(context.Context, AgentID, int64) (PublishedGeneration, bool, error)
}

type GenerationDispatch struct {
	ItemID        int64
	RunID         int64
	EffectID      string
	AgentCode     string
	RolePrompt    string
	TaskPrompt    string
	Context       GenerationContext
	ModelPolicy   GenerationModelPolicy
	PayloadDigest effectport.Digest
	AcceptedAt    time.Time
}

type GenerationDispatchReader interface {
	GenerationDispatch(context.Context, string) (GenerationDispatch, bool, error)
}

type GenerationCompletion struct {
	EffectID      string
	State         effectport.State
	Attempt       effectport.Attempt
	ReceiptDigest effectport.Digest
	Artifact      effectport.ResultArtifact
	FailureCode   string
	CompletedAt   time.Time
}

// GenerationCompletionWriter is invoked by the External Effects completion
// router in the same transaction that settles the effect attempt.
type GenerationCompletionWriter interface {
	CompleteGeneration(context.Context, GenerationCompletion) error
}

type GenerationItem struct {
	ID                 int64                     `json:"id"`
	RunID              int64                     `json:"run_id"`
	CustomerID         customerdomain.CustomerID `json:"customer_id"`
	SenderStaffID      int64                     `json:"sender_staff_id"`
	EffectID           string                    `json:"effect_id,omitempty"`
	State              string                    `json:"state"`
	FailureCode        string                    `json:"failure_code,omitempty"`
	AttemptCount       int32                     `json:"attempt_count"`
	GeneratedText      string                    `json:"-"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
	ContextSnapshot    json.RawMessage           `json:"-"`
	ModelPolicySummary json.RawMessage           `json:"-"`
}

type GenerationProgress struct {
	Total     int64 `json:"total"`
	Queued    int64 `json:"queued"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Unknown   int64 `json:"unknown"`
}

// RenderCustomerNamePrompt performs a single replacement; names are data,
// never recursively interpreted as template syntax or prompt instructions.
func RenderCustomerNamePrompt(prompt, name string) string {
	if strings.TrimSpace(name) == "" {
		name = "朋友"
	}
	return strings.NewReplacer("{{用户名}}", name, "{{客户名}}", name).Replace(prompt)
}
