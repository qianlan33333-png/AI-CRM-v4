package port

import "time"

// IncidentEpisode contains permanent, structured governance facts only.
// Unknown observations require explicit attribution before becoming defects.
type IncidentEpisode struct {
	ID              int64      `json:"id"`
	IssueID         int64      `json:"issue_id"`
	CheckID         string     `json:"check_id"`
	Code            string     `json:"code"`
	DetectedRelease string     `json:"detected_release"`
	DetectionOrigin string     `json:"detection_origin"`
	InitialStatus   string     `json:"initial_status"`
	LatestStatus    string     `json:"latest_status"`
	DetectedAt      time.Time  `json:"detected_at"`
	LastObservedAt  time.Time  `json:"last_observed_at"`
	RecoveredAt     *time.Time `json:"recovered_at"`
	Version         int64      `json:"version"`
	IncidentAttribution
	AttributedAt *time.Time `json:"attributed_at"`
}
type IncidentAttribution struct {
	Classification          string     `json:"classification"`
	EscapedDefect           string     `json:"escaped_defect"`
	ChangeFailure           string     `json:"change_failure"`
	CausedByReleaseSequence *int64     `json:"caused_by_release_sequence"`
	RootCause               string     `json:"root_cause"`
	Remediation             string     `json:"remediation"`
	FaultStartedAt          *time.Time `json:"fault_started_at"`
	FaultStartBasis         string     `json:"fault_start_basis"`
	EffortMinutes           *int       `json:"effort_minutes"`
}
type IncidentAttributionCommand struct {
	ExpectedVersion int64 `json:"expected_version"`
	IncidentAttribution
}
type IncidentAttributionReceipt struct {
	EpisodeID int64 `json:"episode_id"`
	Version   int64 `json:"version"`
	ActionID  int64 `json:"action_id"`
	Replay    bool  `json:"replay"`
}
type IncidentEpisodePage struct {
	Items        []IncidentEpisode `json:"items"`
	HasMore      bool              `json:"has_more"`
	NextBeforeID int64             `json:"next_before_id,omitempty"`
}
type GovernanceWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}
type GovernanceDurationMetric struct {
	MeanSeconds  *float64 `json:"mean_seconds"`
	SampleCount  int64    `json:"sample_count"`
	MissingCount int64    `json:"missing_count"`
}
type GovernanceRatioMetric struct {
	Numerator    int64    `json:"numerator"`
	Denominator  int64    `json:"denominator"`
	Unclassified int64    `json:"unclassified"`
	Ratio        *float64 `json:"ratio"`
}
type GovernanceDeploymentMetrics struct {
	EvidenceState                     string                    `json:"evidence_state"`
	EvidenceAt                        *time.Time                `json:"evidence_at"`
	Scope                             string                    `json:"scope"`
	FullChangeFailureRateAvailable    bool                      `json:"full_change_failure_rate_available"`
	HistoricalSequenceGaps            *int64                    `json:"historical_sequence_gaps"`
	VerifiedSuccessfulDeployments     int64                     `json:"verified_successful_deployments"`
	ConfirmedFailedDeployments        int64                     `json:"confirmed_failed_deployments"`
	VerifiedSuccessCohortFailureRatio *float64                  `json:"verified_success_cohort_failure_ratio"`
	MissingFailedDeploymentFacts      bool                      `json:"missing_failed_deployment_facts"`
	UnclassifiedDefectAttributions    int64                     `json:"unclassified_defect_attributions"`
	RevokedReleaseAttributions        int64                     `json:"revoked_release_attributions"`
	Releases                          []GovernanceReleaseChoice `json:"releases"`
	ReleasesTruncated                 bool                      `json:"releases_truncated"`
}
type GovernanceReleaseChoice struct {
	Sequence    int64     `json:"sequence"`
	ReleaseSHA  string    `json:"release_sha"`
	SucceededAt time.Time `json:"succeeded_at"`
}
type GovernanceOutcomes struct {
	Window                         GovernanceWindow            `json:"window"`
	Cohort                         string                      `json:"cohort"`
	HistoryScope                   string                      `json:"history_scope"`
	EpisodeCount                   int64                       `json:"episode_count"`
	OpenCount                      int64                       `json:"open_count"`
	ConfirmedDefects               int64                       `json:"confirmed_defects"`
	UnclassifiedEpisodes           int64                       `json:"unclassified_episodes"`
	OtherClassifiedEpisodes        int64                       `json:"other_classified_episodes"`
	UnknownOrUncoveredObservations int64                       `json:"unknown_or_uncovered_observations"`
	MTTD                           GovernanceDurationMetric    `json:"mttd"`
	DetectedToRecovered            GovernanceDurationMetric    `json:"detected_to_recovered"`
	FaultToRecovered               GovernanceDurationMetric    `json:"fault_to_recovered"`
	OpenConfirmedDefects           int64                       `json:"open_confirmed_defects"`
	EscapedDefects                 GovernanceRatioMetric       `json:"escaped_defects"`
	RepeatedDefects                GovernanceRatioMetric       `json:"repeated_defects"`
	EffortMinutesTotal             int64                       `json:"effort_minutes_total"`
	EffortKnownCount               int64                       `json:"effort_known_count"`
	EffortMissingCount             int64                       `json:"effort_missing_count"`
	Deployments                    GovernanceDeploymentMetrics `json:"deployments"`
}
