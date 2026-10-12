package jobqueue

// ExpectedRetry marks a successful observation that still awaits a business
// terminal result. It only affects diagnostics; River retry timing and attempt
// budgets are unchanged. Wrapped/joined errors remain normal failures.
type ExpectedRetry interface {
	error
	ExpectedJobRetry() bool
}
