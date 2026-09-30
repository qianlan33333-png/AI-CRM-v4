package port

// MemberEventV1 is the immutable, versioned envelope read by the existing
// snapshot dispatch job. Exactly one payload must match Kind.
type MemberEventV1 struct {
	Kind          string
	MemberEntered *MemberEnteredV1
	PaidQualified *MemberPaidQualifiedV1
}
