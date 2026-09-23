package port

type AssessmentLevel struct {
	MinimumScore      float64  `json:"min_score"`
	MaximumScore      float64  `json:"max_score"`
	Title             string   `json:"title"`
	Greeting          string   `json:"greeting,omitempty"`
	Summary           string   `json:"summary,omitempty"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
	CourseName        string   `json:"course_name,omitempty"`
	CourseURL         string   `json:"course_url,omitempty"`
	CTAText           string   `json:"cta_text,omitempty"`
	Enabled           bool     `json:"enabled"`
	SortOrder         int      `json:"sort_order"`
	TagCodes          []string `json:"tag_codes"`
}

type AssessmentType struct {
	Key               string   `json:"key"`
	Name              string   `json:"name"`
	Title             string   `json:"title"`
	Greeting          string   `json:"greeting,omitempty"`
	Summary           string   `json:"summary,omitempty"`
	Diagnosis         string   `json:"diagnosis,omitempty"`
	ProblemHint       string   `json:"problem_hint,omitempty"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
	CourseName        string   `json:"course_name,omitempty"`
	CourseURL         string   `json:"course_url,omitempty"`
	CTAText           string   `json:"cta_text,omitempty"`
	Enabled           bool     `json:"enabled"`
	ShowInResult      bool     `json:"show_in_result"`
	SortOrder         int      `json:"sort_order"`
	TagCodes          []string `json:"tag_codes"`
}

type AssessmentDimension struct {
	Key                      string            `json:"key"`
	Name                     string            `json:"name"`
	Summary                  string            `json:"summary,omitempty"`
	Weight                   *float64          `json:"weight"`
	ScoringMethod            string            `json:"scoring_method"`
	CategoryMethod           string            `json:"category_method"`
	Enabled                  bool              `json:"enabled"`
	ParticipatesInTotalScore bool              `json:"participates_in_total_score"`
	ShowInResult             bool              `json:"show_in_result"`
	SortOrder                int               `json:"sort_order"`
	TypePriority             []string          `json:"type_priority"`
	Types                    []AssessmentType  `json:"types"`
	Levels                   []AssessmentLevel `json:"levels"`
}

type FinalRecommendation struct {
	Enabled     bool   `json:"enabled"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	CourseName  string `json:"course_name,omitempty"`
	CourseURL   string `json:"course_url,omitempty"`
	CTAText     string `json:"cta_text,omitempty"`
}

type AssessmentConfig struct {
	TemplateID            string                `json:"template_id,omitempty"`
	TemplateName          string                `json:"template_name,omitempty"`
	AssetKind             string                `json:"asset_kind,omitempty"`
	SourceQuestionnaireID *int64                `json:"source_questionnaire_id,omitempty"`
	TotalScoreTitle       string                `json:"total_score_title"`
	StrengthCount         int                   `json:"strength_count"`
	WeaknessCount         int                   `json:"weakness_count"`
	OverallLevels         []AssessmentLevel     `json:"overall_levels"`
	Dimensions            []AssessmentDimension `json:"dimensions"`
	Recommendations       []map[string]any      `json:"recommendations"`
	FinalRecommendation   FinalRecommendation   `json:"final_recommendation"`
}

type AssessmentDimensionResult struct {
	Key          string           `json:"key"`
	Name         string           `json:"name"`
	Score        float64          `json:"score"`
	Level        *AssessmentLevel `json:"level,omitempty"`
	DominantType *AssessmentType  `json:"dominant_type,omitempty"`
	TagCodes     []string         `json:"tag_codes"`
}

type AssessmentResult struct {
	TotalScore            float64                     `json:"total_score"`
	OverallLevel          *AssessmentLevel            `json:"overall_level,omitempty"`
	Dimensions            []AssessmentDimensionResult `json:"dimensions"`
	StrengthDimensionKeys []string                    `json:"strength_dimension_keys"`
	WeaknessDimensionKeys []string                    `json:"weakness_dimension_keys"`
	TagCodes              []string                    `json:"tag_codes"`
}
