package domain

// VerifiedFact is an opaque, normalized identity fact emitted by a trusted
// provider adapter after it has completed its provider-specific verification.
// Application commands accept this type instead of a raw Reference so a
// declared HTTP DTO cannot promote itself to verified by merely setting an
// assurance field. The adapter boundary remains responsible for authenticating
// the provider context before calling NewVerifiedFact.
type VerifiedFact struct {
	reference NormalizedReference
}

// ProviderVerifiedIdentityInput deliberately has no Assurance field. It is
// constructed only by an internal adapter after provider verification; HTTP
// DTOs must remain References with declared assurance and cannot be converted
// through the application provisioning command.
type ProviderVerifiedIdentityInput struct {
	Kind   Kind
	Scope  string
	Value  string
	Source string
}

// NewVerifiedFact is the sole constructor for the verified application input.
// It fixes assurance internally rather than trusting a caller-selected value.
func NewVerifiedFact(input ProviderVerifiedIdentityInput) (VerifiedFact, error) {
	normalized, err := Normalize(Reference{
		Kind: input.Kind, Scope: input.Scope, Value: input.Value,
		Assurance: AssuranceVerified, Source: input.Source,
	})
	if err != nil {
		return VerifiedFact{}, ErrInvalidReference
	}
	return VerifiedFact{reference: normalized}, nil
}

func (fact VerifiedFact) Reference() NormalizedReference {
	return fact.reference
}

func (fact VerifiedFact) Valid() bool {
	return fact.reference.Assurance == AssuranceVerified &&
		validKind(fact.reference.Kind) &&
		validScope(fact.reference.Kind, fact.reference.Scope) &&
		fact.reference.NormalizedValue != "" && fact.reference.Source != ""
}
