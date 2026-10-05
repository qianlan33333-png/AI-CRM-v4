package port

import "context"

// RetiredSurveyConfigurationReader checks immutable Survey owner-selection
// settings while the caller's Unit of Work is active. The Within suffix is
// intentional: runtimes that already own a transaction must not open a nested
// one while validating a queued action or event.
type RetiredSurveyConfigurationReader interface {
	SurveyOwnerSelectionRetiredAtWithin(context.Context, ConfigurationVersionID) (bool, error)
	SurveyOwnerSelectionRetiredForPackageWithin(context.Context, PackageID) (bool, error)
}
