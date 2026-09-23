// Package port defines the stable cross-layer Survey contracts.
package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

var (
	ErrInvalid  = errors.New("invalid survey command")
	ErrNotFound = errors.New("survey questionnaire not found")
	ErrConflict = errors.New("survey questionnaire conflict")
	// ErrAlreadySubmitted is distinct from an idempotency payload conflict. It
	// means this resolved canonical customer has already consumed this
	// questionnaire's post-cutover submission entitlement with another key.
	ErrAlreadySubmitted  = errors.New("survey questionnaire already submitted")
	ErrReferenced        = errors.New("survey questionnaire has retained history")
	ErrUnavailable       = errors.New("survey service unavailable")
	ErrIdentityConflict  = errors.New("survey identity conflict")
	ErrEffectUnavailable = errors.New("survey external effect unavailable")
)

type ID int64
type QuestionType string
type QuestionnaireMode string
type QuestionnaireStatus string
type AnswerDisplayMode string
type IdentityState string

const (
	QuestionSingleChoice QuestionType = "single_choice"
	QuestionMultiChoice  QuestionType = "multi_choice"
	QuestionTextarea     QuestionType = "textarea"
	QuestionMobile       QuestionType = "mobile"

	ModeSurvey     QuestionnaireMode = "survey"
	ModeAssessment QuestionnaireMode = "assessment"

	StatusDraft     QuestionnaireStatus = "draft"
	StatusPublished QuestionnaireStatus = "published"
	StatusDisabled  QuestionnaireStatus = "disabled"
	// StatusArchived is terminal for normal administration and public access.
	// The definition and its submission history remain readable through their
	// retained historical paths.
	StatusArchived QuestionnaireStatus = "archived"

	DisplayAllInOne AnswerDisplayMode = "all_in_one"
	DisplayOneByOne AnswerDisplayMode = "one_by_one"

	IdentityAnonymous  IdentityState = "anonymous"
	IdentityResolved   IdentityState = "resolved"
	IdentityUnresolved IdentityState = "unresolved"
	IdentityConflict   IdentityState = "conflict"
)

type Validation struct {
	MinimumSelections *int `json:"min_selections,omitempty"`
	MaximumSelections *int `json:"max_selections,omitempty"`
	MinimumLength     *int `json:"min_length,omitempty"`
	MaximumLength     *int `json:"max_length,omitempty"`
}

type Option struct {
	ID                 ID       `json:"id,omitempty"`
	Text               string   `json:"option_text"`
	Score              float64  `json:"score"`
	AssessmentTypeKey  string   `json:"assessment_type_key,omitempty"`
	TagCodes           []string `json:"tag_codes"`
	IsOther            bool     `json:"is_other"`
	OtherPlaceholder   string   `json:"other_placeholder,omitempty"`
	OtherMaximumLength int      `json:"other_max_length,omitempty"`
	SortOrder          int      `json:"sort_order"`
}

type Question struct {
	ID                     ID           `json:"id,omitempty"`
	Type                   QuestionType `json:"type"`
	Title                  string       `json:"title"`
	AssessmentDimensionKey string       `json:"assessment_dimension_key,omitempty"`
	SidebarProfileField    string       `json:"sidebar_profile_field,omitempty"`
	Required               bool         `json:"required"`
	SortOrder              int          `json:"sort_order"`
	Placeholder            string       `json:"placeholder_text,omitempty"`
	Validation             Validation   `json:"validation,omitempty"`
	Options                []Option     `json:"options"`
}

type ScoreRule struct {
	MinimumScore *float64 `json:"min_score"`
	MaximumScore *float64 `json:"max_score"`
	TagCodes     []string `json:"tag_codes"`
	SortOrder    int      `json:"sort_order"`
}

type Questionnaire struct {
	ID                ID                  `json:"id"`
	Name              string              `json:"name"`
	Title             string              `json:"title"`
	Description       string              `json:"description"`
	Mode              QuestionnaireMode   `json:"mode"`
	AnswerDisplayMode AnswerDisplayMode   `json:"answer_display_mode"`
	AssessmentConfig  json.RawMessage     `json:"assessment_config"`
	Slug              string              `json:"slug"`
	Status            QuestionnaireStatus `json:"status"`
	Questions         []Question          `json:"questions"`
	ScoreRules        []ScoreRule         `json:"score_rules"`
	CreatedBy         int64               `json:"created_by"`
	Version           int64               `json:"version"`
	DefinitionVersion int64               `json:"definition_version"`
	SubmissionCount   int64               `json:"submission_count"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

type CreateCommand struct {
	Questionnaire  Questionnaire
	ActorID        int64
	IdempotencyKey string
}

type UpdateCommand struct {
	Questionnaire   Questionnaire
	ExpectedVersion int64
	ActorID         int64
	IdempotencyKey  string
}

type Page struct {
	Items  []Questionnaire `json:"items"`
	Total  int64           `json:"total"`
	Limit  int32           `json:"limit"`
	Offset int32           `json:"offset"`
}

// DefinitionReader is the Survey-owned, read-only definition projection for
// consumers that need to validate questionnaire, question, and option refs.
type DefinitionReader interface {
	List(context.Context, int32, int32, string, QuestionnaireStatus) (Page, error)
	Get(context.Context, ID) (Questionnaire, error)
}

type DefinitionApplication interface {
	DefinitionReader
	Create(context.Context, CreateCommand) (Questionnaire, error)
	Update(context.Context, UpdateCommand) (Questionnaire, error)
	Duplicate(context.Context, ID, int64, string) (Questionnaire, error)
	Publish(context.Context, ID, int64, int64, string) (Questionnaire, error)
	SetStatus(context.Context, ID, int64, QuestionnaireStatus, int64, string) (Questionnaire, error)
	DeleteDraft(context.Context, ID, int64, int64, string) error
}

type SubmissionAnswer struct {
	QuestionID ID     `json:"question_id"`
	OptionIDs  []ID   `json:"option_ids"`
	TextValue  string `json:"text_value,omitempty"`
}

type SubmissionIdentity struct {
	State          IdentityState              `json:"state"`
	CustomerID     *customerdomain.CustomerID `json:"customer_id,omitempty"`
	EvidenceDigest string                     `json:"evidence_digest,omitempty"`
}

type SubmissionResult struct {
	SubmissionID ID                 `json:"submission_id"`
	TotalScore   float64            `json:"total_score"`
	Result       AssessmentResult   `json:"assessment_result"`
	Identity     SubmissionIdentity `json:"identity"`
}
