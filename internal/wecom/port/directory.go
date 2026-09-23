package port

import "context"

var ErrDirectoryDisabled = directoryError("wecom directory provider disabled")

type directoryError string

func (err directoryError) Error() string { return string(err) }

// DirectoryFailure exposes a stable, non-PII classification for a failed
// read from the WeCom customer directory. Callers must not infer a provider
// write from this interface: DirectoryProvider remains read-only.
type DirectoryFailure interface {
	error
	DirectoryFailureCode() string
	DirectoryFailureRetryable() bool
}

// DirectoryFailureAttemptLimit optionally narrows River's normal attempt
// budget for one specific Provider classification. Zero keeps the job's
// configured limit. Callers must not infer a limit from the failure code.
type DirectoryFailureAttemptLimit interface {
	DirectoryFailure
	DirectoryFailureMaxAttempts() int
}

type ExternalContact struct {
	ExternalUserID string
	Name           string
	AvatarURL      string
	Gender         int16
	Type           int16
	CorpName       string
	UnionID        string
	FollowInfo     []ExternalContactFollowInfo
}

type ExternalContactFollowInfo struct {
	EmployeeID string
	// Remark is nil when the Provider response did not project this field. An
	// explicit empty string remains distinct from not-yet-projected history.
	Remark *string
	// Description is the follow-employee-level external-contact description.
	// It is deliberately separate from Remark: the legacy ID projection wrote
	// this provider field, while Remark remains a read-only display projection.
	// Nil means the provider did not include description; an explicit empty
	// string must remain distinguishable for a safe compare-before-write flow.
	Description *string
	// DescriptionProjected is true only when the Provider explicitly returned
	// the description field (including an explicit empty string). A missing
	// field is not evidence that the description is empty and must not enable a
	// destructive replacement write.
	DescriptionProjected bool
	Tags                 []ExternalContactTag
}

type ExternalContactTag struct {
	ProviderTagID string
	Name          string
	Type          int16
}

type ExternalContactPage struct {
	Contacts   []ExternalContact
	NextCursor string
}

// DirectoryProvider is read-only. It has no method capable of changing a
// WeCom contact, remark, tag or ownership relationship.
type DirectoryProvider interface {
	DirectoryReady() bool
	ListContactStaff(context.Context) ([]string, error)
	BatchExternalContacts(context.Context, string, string, int) (ExternalContactPage, error)
}

// ContactStaffProfile is a read-only, provider-verified display projection for
// a known customer-contact capable employee. The caller supplies the eligible
// userid set from ListContactStaff; this type must never expand that set or
// grant the employee a local role.
type ContactStaffProfile struct {
	UserID      string
	DisplayName string
}

// ContactStaffProfileSnapshot intentionally retains only safe display facts
// and one stable aggregate failure classification. A partial profile failure
// leaves Items populated for every successfully read employee while allowing
// callers to keep their last verified local name for the rest.
type ContactStaffProfileSnapshot struct {
	Items            []ContactStaffProfile
	ProfileReadState string
	ProfileErrorCode string
}

// ContactStaffProfileReader is an optional refinement of DirectoryProvider.
// The external-contact follow-user list remains the authority for eligibility;
// implementations may only enrich the requested subset with display names.
// The bounded input avoids an unbounded user/get fan-out on an admin refresh.
type ContactStaffProfileReader interface {
	ReadContactStaffProfiles(context.Context, []string) (ContactStaffProfileSnapshot, error)
}

// ExternalContactReader reads one known external contact. It is a Provider-read
// boundary only; it cannot mark, unmark, or otherwise mutate WeCom state.
type ExternalContactReader interface {
	ReadExternalContact(context.Context, string) (ExternalContact, error)
}

// ExternalContactDescriptionTarget is the minimum trusted projection needed
// to compare and update one employee's description for one external contact.
// Projected distinguishes an explicitly returned string (including "") from a
// missing or null provider field, which must never be treated as empty text.
type ExternalContactDescriptionTarget struct {
	Description string
	Projected   bool
}

// ExternalContactDescriptionTargetReader reads only the requested employee's
// description. Implementations must validate the returned external contact and
// require exactly one matching follow relationship, while ignoring unrelated
// relationship metadata such as tags.
type ExternalContactDescriptionTargetReader interface {
	ReadExternalContactDescriptionTarget(context.Context, string, string) (ExternalContactDescriptionTarget, error)
}
