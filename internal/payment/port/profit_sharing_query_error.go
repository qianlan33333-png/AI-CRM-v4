package port

import "errors"

// ProfitSharingQueryError carries only the current original-order query's
// finite API code. It is never evidence that the earlier write was rejected,
// that the receiver failed, or that a new money instruction may be submitted.
type ProfitSharingQueryError struct{ code string }

func NewProfitSharingQueryError(code string) *ProfitSharingQueryError {
	switch code {
	case "PARAM_ERROR", "INVALID_REQUEST", "SIGN_ERROR", "SYSTEM_ERROR", "NO_AUTH", "RESOURCE_NOT_EXISTS", "FREQUENCY_LIMITED":
	default:
		code = "UNKNOWN"
	}
	return &ProfitSharingQueryError{code: code}
}

func (e *ProfitSharingQueryError) Error() string {
	return "profit sharing original query unavailable: " + e.ProviderCode()
}

func (e *ProfitSharingQueryError) ProviderCode() string {
	if e == nil || e.code == "" {
		return "UNKNOWN"
	}
	return e.code
}

func (*ProfitSharingQueryError) Unwrap() error { return ErrUnavailable }

func ProfitSharingQueryCode(err error) string {
	var query *ProfitSharingQueryError
	if errors.As(err, &query) {
		return query.ProviderCode()
	}
	return ""
}
