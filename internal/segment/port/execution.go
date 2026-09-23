package port

import "context"

type ExecutionConfiguration struct {
	PackageID              PackageID
	PackageVersion         int64
	ConfigurationVersionID ConfigurationVersionID
	Snapshot               Snapshot
	AgentID                int64
	AgentPublishedVersion  int64
	ContentDigest          [32]byte
	MaterialsDigest        [32]byte
	BindingVersion         int64
	SenderSetVersion       int64
	SenderStaffIDs         []int64
	Ready                  bool
	Reasons                []string
}
type ExecutionConfigurationReader interface {
	AudienceExecutionConfiguration(context.Context, PackageID) (ExecutionConfiguration, error)
}
