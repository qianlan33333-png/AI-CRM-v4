package adapter

import "errors"

// DirectoryFailureNumbers exposes only numeric Provider status, never raw
// response text, tokens, customer identifiers or URLs, for migration diagnosis.
func (err *directoryReadError) DirectoryFailureNumbers() (int, int64) {
	var p *providerResponseError
	if errors.As(err.cause, &p) {
		return p.statusCode, p.errCode
	}
	return 0, 0
}
