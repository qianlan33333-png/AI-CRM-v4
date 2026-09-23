package app

import (
	"context"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"testing"
)

type activationStore struct {
	Store
	pkg    segmentdomain.Package
	config segmentdomain.ConfigurationVersion
	err    error
}

func (s activationStore) LockPackage(context.Context, int64) (segmentdomain.Package, error) {
	return s.pkg, nil
}
func (s activationStore) CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return s.config, s.err
}

func TestActivationPrecheckDoesNotRelaxConfiguredExecution(t *testing.T) {
	id := int64(2)
	cfg := segmentdomain.ConfigurationVersion{ID: 2, PackageID: 1, Definition: []byte(`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"30"}}`), RefreshMode: "every_3m"}
	for _, kind := range []string{"rule", "binding", "senders", "missing", "invalid_schedule", "invalid_definition"} {
		t.Run(kind, func(t *testing.T) {
			st := activationStore{pkg: segmentdomain.Package{ID: 1, CurrentConfigurationVersionID: &id}, config: cfg}
			switch kind {
			case "binding":
				st.pkg.CurrentAutomationBindingID = &id
			case "senders":
				st.pkg.CurrentSenderSetID = &id
			case "missing":
				st.err = segmentstore.ErrNotFound
			case "invalid_schedule":
				st.config.RefreshMode = "unsupported"
			case "invalid_definition":
				st.config.Definition = []byte(`{}`)
			}
			execution, _ := NewExecutionService(directUOW{}, executionStoreStub{pkg: st.pkg, config: cfg, bindingErr: segmentstore.ErrNotFound, sendersErr: segmentstore.ErrNotFound}, publishedAgentStub{}, staffReaderStub{}, false)
			f := NewRuntimeFacade(NewService(directUOW{}, st), nil, execution)
			result, e := f.ActivationPrecheck(context.Background(), 1)
			if e != nil {
				t.Fatal(e)
			}
			if result.Ready != (kind == "rule") {
				t.Fatalf("kind=%s readiness=%v", kind, result.Ready)
			}
		})
	}
}
