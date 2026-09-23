package port

import "context"

// LegacyMaterialReference identifies an immutable source-record key. Consumers
// never send a V3 Media ID as a substitute for a legacy ID.
type LegacyMaterialReference struct {
	SourceSystem string
	MaterialKind string
	LegacyID     string
}

// LegacyMaterialMapping is a Media-owned, verified translation to a local
// Media record. SourceDigest is the source fact observed when the mapping was
// imported; consumers must still capture the current Media source in their
// acceptance UoW and reject drift.
type LegacyMaterialMapping struct {
	Reference          LegacyMaterialReference
	MaterialKind       string
	MaterialID         int64
	SourceDigest       string
	SourceRecordDigest string
}

// LegacyMaterialMappingResolver is read-only at runtime. Mappings are written
// by a separate frozen-snapshot import or authorized Media management command,
// never by an authenticated business intake.
type LegacyMaterialMappingResolver interface {
	ResolveLegacyMaterialMapping(context.Context, LegacyMaterialReference) (LegacyMaterialMapping, bool, error)
}

// LegacyMaterialMappingImporter is intentionally separate from runtime reads.
// Its caller must have verified a frozen source record and the current Media
// source before recording the immutable correspondence.
type LegacyMaterialMappingImporter interface {
	ImportLegacyMaterialMapping(context.Context, LegacyMaterialMapping, string) error
}

// MaterialReference is an opaque, owner-scoped protection fact. The owner
// never supplies a foreign key or business payload to Media.
type MaterialReference struct {
	MaterialKind    string
	MaterialID      int64
	Owner           string
	ReferenceDigest string
}

// MaterialReferenceRegistrar is the stable port future domains use to protect
// Media material. Register must fail if the locked target no longer exists.
type MaterialReferenceRegistrar interface {
	RegisterMediaReference(context.Context, MaterialReference) error
	UnregisterMediaReference(context.Context, MaterialReference) error
}
