package port

import (
	"context"
	"encoding/json"
	"time"
)

type SubmitCommand struct {
	Slug                               string
	DefinitionVersion                  int64
	SubmissionKey                      string
	Answers                            []SubmissionAnswer
	Identity                           SubmissionIdentity
	SourceChannel, CampaignID, StaffID string
}

type SubmissionReceipt struct {
	QuestionnaireID   ID     `json:"questionnaire_id"`
	QuestionnaireSlug string `json:"questionnaire_slug"`
	DefinitionVersion int64  `json:"definition_version"`
	SubmissionID      ID     `json:"submission_id"`
	ResultToken       string `json:"result_token,omitempty"`
	// CompletionAction is used by the application to carry the resolved public
	// action to HTTP. The public POST envelope emits it once at top level; it
	// must not be duplicated inside the legacy receipt projection.
	CompletionAction CompletionAction `json:"-"`
}

// CompletionAction is the deliberately small public completion projection.
// It never identifies a customer, submission, result token, provider target,
// or channel internals.
type CompletionAction struct {
	Type        CompletionActionType  `json:"type"`
	RedirectURL string                `json:"redirect_url,omitempty"`
	LeadQR      *CompletionLeadQRCode `json:"lead_qr,omitempty"`
}

type CompletionActionType string

const (
	CompletionActionDefault  CompletionActionType = "default"
	CompletionActionRedirect CompletionActionType = "redirect"
	CompletionActionLeadQR   CompletionActionType = "lead_qr"
)

type CompletionLeadQRCode struct {
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
}

func DefaultCompletionAction() CompletionAction {
	return CompletionAction{Type: CompletionActionDefault}
}

// AlreadySubmittedError preserves the safe completion projection for a
// duplicate public submit. Callers must test errors.Is(err,
// ErrAlreadySubmitted), not the message text.
type AlreadySubmittedError struct {
	CompletionAction CompletionAction
}

func (e *AlreadySubmittedError) Error() string { return ErrAlreadySubmitted.Error() }
func (e *AlreadySubmittedError) Unwrap() error { return ErrAlreadySubmitted }

// PublicSubmissionStatus is intentionally scoped to the current trusted
// Survey session. It does not disclose a submission, Customer, identity, or
// result token.
type PublicSubmissionStatus struct {
	Submitted        bool             `json:"submitted"`
	CompletionAction CompletionAction `json:"completion_action"`
}

type PublicSubmissionStatusReader interface {
	PublicSubmissionStatus(context.Context, string, SubmissionIdentity) (PublicSubmissionStatus, error)
}

type AnswerSnapshot struct {
	ID                      ID                       `json:"id"`
	QuestionID              *ID                      `json:"question_id,omitempty"`
	LegacySourceQuestionID  *int64                   `json:"legacy_source_question_id,omitempty"`
	QuestionType            QuestionType             `json:"question_type"`
	QuestionTitle           string                   `json:"question_title"`
	SortOrder               int                      `json:"sort_order"`
	SelectedOptions         []SelectedOptionSnapshot `json:"selected_options"`
	TextValue               string                   `json:"text_value,omitempty"`
	TextValueMasked         string                   `json:"text_value_masked,omitempty"`
	Score                   float64                  `json:"score"`
	LegacyDefinitionMissing bool                     `json:"legacy_definition_missing"`
	PhoneBindingStatus      string                   `json:"phone_binding_status,omitempty"`
}

type SelectedOptionSnapshot struct {
	OptionID   ID       `json:"option_id"`
	OptionText string   `json:"option_text"`
	Score      float64  `json:"score"`
	TagCodes   []string `json:"tag_codes,omitempty"`
}

type Submission struct {
	ID                                 ID                 `json:"id"`
	QuestionnaireID                    ID                 `json:"questionnaire_id"`
	DefinitionVersion                  int64              `json:"definition_version"`
	QuestionnaireSlug                  string             `json:"questionnaire_slug"`
	QuestionnaireTitle                 string             `json:"questionnaire_title"`
	Mode                               QuestionnaireMode  `json:"mode"`
	Identity                           SubmissionIdentity `json:"identity"`
	TotalScore                         float64            `json:"total_score"`
	Result                             AssessmentResult   `json:"assessment_result"`
	SourceChannel, CampaignID, StaffID string
	SubmittedAt                        time.Time        `json:"submitted_at"`
	Answers                            []AnswerSnapshot `json:"answers"`
}

type SubmissionPage struct {
	Items         []Submission `json:"items"`
	Total         int64        `json:"total"`
	Limit, Offset int32
}

type CustomerHistoryQuery struct {
	CustomerID int64
	Limit      int32
	Watermark  time.Time
	AfterAt    time.Time
	AfterID    ID
}

type CustomerHistoryWindow struct {
	Items []Submission
}

// CustomerHistoryReader is the stable, customer_id-only read boundary used by
// the Customer profile composition adapter. Returned free text is already
// masked by Survey and must never be rehydrated by consumers.
type CustomerHistoryReader interface {
	CustomerHistoryWindow(context.Context, CustomerHistoryQuery) (CustomerHistoryWindow, error)
}

// ExternalSubmissionQuery describes the frozen donor external-read filter
// after the Host has authenticated the machine principal and resolved the
// supplied identity through OneID. HistoricalUnionIDs are Survey-owned source
// facts, not Identity inputs: Survey never resolves, provisions, or links a
// customer from them.
type ExternalSubmissionQuery struct {
	// CustomerID is supplied only after the Host's scoped OneID resolution. It
	// selects V3-native submissions; historical rows remain filtered by their
	// separate source union projection.
	CustomerID            int64
	HistoricalUnionIDs    []string
	QuestionnaireSourceID int64
	// SourceSystem and SourceRecordID are the stable provenance selector. A
	// non-empty pair is applied by Survey before pagination; callers must not
	// scan a customer page and match source IDs themselves.
	SourceSystem   string
	SourceRecordID string
	SubmittedFrom  time.Time
	SubmittedTo    time.Time
	// SubmittedEndExclusive gives V1 an explicit [start,end) boundary while
	// retaining the frozen legacy route's inclusive end compatibility.
	SubmittedEndExclusive bool
	// BeforeSubmittedAt and BeforeSubmissionID form a stable descending
	// (submitted_at,id) keyset boundary for snapshot paging. They must either
	// both be absent or both be supplied.
	BeforeSubmittedAt  time.Time
	BeforeSubmissionID ID
	Limit              int32
	Offset             int64
}

// ExternalSubmission is the unmasked compatibility projection required by the
// authorized external questionnaire API. It deliberately has no Customer or
// Identity fields; the API Host supplies current identity aliases separately.
type ExternalSubmission struct {
	SubmissionID ID `json:"submission_id"`
	// SourceSystem and SourceRecordID preserve the native or imported source
	// record without exposing its historical identity value.
	SourceSystem          string                     `json:"source_system"`
	SourceRecordID        string                     `json:"source_record_id"`
	HistoricalUnionID     string                     `json:"unionid"`
	Legacy                bool                       `json:"-"`
	QuestionnaireSourceID int64                      `json:"questionnaire_id"`
	DefinitionVersion     int64                      `json:"definition_version"`
	QuestionnaireTitle    string                     `json:"questionnaire_title"`
	SubmittedAt           time.Time                  `json:"submitted_at"`
	FinalTags             json.RawMessage            `json:"final_tags"`
	AssessmentResult      json.RawMessage            `json:"assessment_result_snapshot"`
	Answers               []ExternalSubmissionAnswer `json:"answers"`
}

type ExternalSubmissionAnswer struct {
	QuestionTitle       string   `json:"question_title_snapshot"`
	SelectedOptionTexts []string `json:"selected_option_texts_snapshot"`
	TextValue           string   `json:"text_value"`
	ScoreContribution   float64  `json:"score_contribution"`
}

type ExternalSubmissionPage struct {
	Items  []ExternalSubmission
	Total  int64
	Limit  int32
	Offset int64
}

// ExternalSubmissionReader is an owner-scoped projection for the frozen
// external questionnaire API. Callers must establish identity and machine
// authorization before passing historic union values to this read boundary.
type ExternalSubmissionReader interface {
	ExternalSubmissions(context.Context, ExternalSubmissionQuery) (ExternalSubmissionPage, error)
}

type Analytics struct {
	QuestionnaireID   ID                  `json:"questionnaire_id"`
	DefinitionVersion int64               `json:"definition_version"`
	Slug              string              `json:"slug"`
	State             QuestionnaireStatus `json:"state"`
	SubmissionCount   int64               `json:"submission_count"`
	AverageScore      float64             `json:"average_score"`
}

type OperationReceipt struct {
	ID                       ID        `json:"id"`
	QuestionnaireID          ID        `json:"questionnaire_id"`
	SubmissionID             *ID       `json:"submission_id,omitempty"`
	OperationKind            string    `json:"operation_kind"`
	Status                   string    `json:"status"`
	FailureCategory          string    `json:"failure_category,omitempty"`
	OccurrenceCount          int64     `json:"occurrence_count"`
	OccurredAt               time.Time `json:"occurred_at"`
	ReadOnlyLegacy           bool      `json:"read_only_legacy"`
	Replayable               bool      `json:"replayable"`
	SourcePK                 string    `json:"source_pk,omitempty"`
	ProviderCallAttempted    *bool     `json:"provider_call_attempted,omitempty"`
	ProviderRealCallExecuted *bool     `json:"provider_real_call_executed,omitempty"`
	ProviderResultReceived   *bool     `json:"provider_result_received,omitempty"`
	ProviderAttemptNumber    *int32    `json:"provider_attempt_number,omitempty"`
	RealEffectExecuted       bool      `json:"real_external_call_executed"`
}

type OperationConfiguration struct {
	QuestionnaireID              ID              `json:"-"`
	CompletionNavigationRef      string          `json:"navigation_target_id,omitempty"`
	CompletionTarget             json.RawMessage `json:"completion_target,omitempty"`
	CompletionChannelID          *int64          `json:"channel_id,omitempty"`
	LeadQRTitle                  string          `json:"lead_qr_title,omitempty"`
	LeadQRSubtitle               string          `json:"lead_qr_subtitle,omitempty"`
	ExternalPushEnabled          bool            `json:"external_push_enabled"`
	ExternalPushConfigurationRef string          `json:"configuration_reference,omitempty"`
	ExternalPushURL              string          `json:"webhook_url,omitempty"`
	ExternalPushMetadata         json.RawMessage `json:"metadata,omitempty"`
	Version                      int64           `json:"version"`
	UpdatedAt                    time.Time       `json:"updated_at,omitempty"`
}

type LegacySubmission struct {
	ID, SourceID, QuestionnaireSourceID int64
	QuestionnaireID, CustomerID         *int64
	MatchedBy, SourceChannel            string
	TotalScore                          float64
	FinalTags                           json.RawMessage
	SubmittedAt, CreatedAt              time.Time
}

type LegacyAnswer struct {
	ID, SourceID, SubmissionID, SubmissionSourceID, QuestionSourceID                 int64
	QuestionType, QuestionTitle, TextValue                                           string
	SelectedOptionIDs, SelectedOptionTexts, SelectedOptionScores, SelectedOptionTags json.RawMessage
	ScoreContribution                                                                float64
	CreatedAt                                                                        time.Time
}

type PublicApplication interface {
	ReadPublic(context.Context, string) (Questionnaire, error)
	PublicSubmissionStatusReader
	Submit(context.Context, SubmitCommand) (SubmissionReceipt, error)
	QueryResult(context.Context, string) (Submission, error)
}

type SubmissionApplication interface {
	ListSubmissions(context.Context, ID, int32, int32, IdentityState) (SubmissionPage, error)
	GetSubmission(context.Context, ID) (Submission, error)
	CustomerHistory(context.Context, int64, int32, int32) (SubmissionPage, error)
	Analytics(context.Context, ID) (Analytics, error)
	RecordExport(context.Context, ID, int64, string) error
	ListOperationReceipts(context.Context, ID, int32, int32) ([]OperationReceipt, int64, error)
	ListLegacyUnresolved(context.Context, ID, int32, int32) ([]LegacySubmission, int64, error)
	GetLegacyUnresolved(context.Context, ID) (LegacySubmission, error)
	ListLegacyAnswers(context.Context, ID, int32, int32) ([]LegacyAnswer, int64, error)
	GetOperationConfiguration(context.Context, ID) (OperationConfiguration, error)
	SaveOperationConfiguration(context.Context, OperationConfiguration, int64, string) (OperationConfiguration, error)
	RecordDisabledOperation(context.Context, ID, *ID, string, int64, string) (OperationReceipt, error)
	QueueCompletionTest(context.Context, ID, int64, string) (CompletionTestReceipt, error)
}

type MigrationRecord struct {
	SourceSystem, SourceTable, SourcePK string
	RecordDigest                        [32]byte
	SafeSnapshot                        json.RawMessage
}
