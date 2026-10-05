package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type fixedNameReplayStore struct {
	RuntimeStore
	run automationdomain.RuntimeRun
}

func (s fixedNameReplayStore) RuntimeReceipt(context.Context, string, string, [32]byte, [32]byte) (RuntimeReceipt, bool, error) {
	result, err := json.Marshal(s.run)
	return RuntimeReceipt{State: "completed", Result: result}, true, err
}

func TestFixedScriptCustomerNamesConfirmReplayUsesFrozenRun(t *testing.T) {
	run := automationdomain.RuntimeRun{ID: 44, AIPlanID: 45, TargetCount: 2}
	reader := &fixedNameReader{err: errors.New("later database unavailable")}
	service := &RuntimeService{uow: directRuntimeUOW{}, store: fixedNameReplayStore{run: run}, reviewPlans: &previewDriftReviewPlans{}, content: previewDriftContent{}, customerNames: reader}
	got, err := service.ConfirmRun(context.Background(), RunConfirmCommand{PackageID: 1, PackageVersion: 1, SnapshotID: 1, AgentID: 1, AgentPublishedVersion: 1, PreviewDigest: strings.Repeat("ab", 32), Actor: 1, IdempotencyKey: "fixed-name-replay-confirm-0001"})
	if err != nil || !reflect.DeepEqual(got, run) || len(reader.calls) != 0 {
		t.Fatalf("confirmation replay changed the frozen run or re-read names: err=%v calls=%v", err, reader.calls)
	}
}

type fixedNameReader struct {
	values map[customerdomain.CustomerID]string
	calls  []int
	err    error
}

func (r *fixedNameReader) DisplayNames(_ context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	r.calls = append(r.calls, len(ids))
	return r.values, r.err
}

func TestFixedScriptCustomerNamesBatchFallbackAndLiteralData(t *testing.T) {
	reader := &fixedNameReader{values: map[customerdomain.CustomerID]string{1: " 昵称{{客户名}} ", 2: "  "}}
	service := &RuntimeService{}
	if err := service.SetCustomerNames(reader); err != nil {
		t.Fatal(err)
	}
	ids := make([]customerdomain.CustomerID, 401)
	for i := range ids {
		ids[i] = customerdomain.CustomerID(i + 1)
	}
	content := automationport.FixedContentPackage{ContentText: "{{用户名}} / {{客户名}} / {{保留变量}}", ImageLibraryIDs: []int64{19}, AttachmentLibraryIDs: []int64{20}}
	result, err := service.fixedContentWithin(context.Background(), content, ids)
	if err != nil || len(result) != len(ids) {
		t.Fatalf("result count=%d err=%v", len(result), err)
	}
	if !reflect.DeepEqual(reader.calls, []int{200, 200, 1}) {
		t.Fatalf("Customer Port batch sizes=%v", reader.calls)
	}
	for i, item := range result {
		want := "朋友 / 朋友 / {{保留变量}}"
		if i == 0 {
			want = "昵称{{客户名}} / 昵称{{客户名}} / {{保留变量}}"
		}
		if item.ContentText != want || !reflect.DeepEqual(item.ImageLibraryIDs, content.ImageLibraryIDs) || !reflect.DeepEqual(item.AttachmentLibraryIDs, content.AttachmentLibraryIDs) {
			t.Fatal("name expansion changed recipient data or material references")
		}
	}
	if content.ContentText != "{{用户名}} / {{客户名}} / {{保留变量}}" {
		t.Fatal("rendering mutated the published template")
	}
}

func TestFixedScriptCustomerNamesFailClosedAndPreserveLegacy(t *testing.T) {
	service := &RuntimeService{}
	ids := []customerdomain.CustomerID{1}
	legacy := automationport.FixedContentPackage{ContentText: "普通固定话术 {{unknown}}", ImageLibraryIDs: []int64{1}}
	result, err := service.fixedContentWithin(context.Background(), legacy, ids)
	if err != nil || !reflect.DeepEqual(result, []automationport.FixedContentPackage{legacy}) {
		t.Fatal("legacy content unexpectedly required a nickname reader")
	}
	template := automationport.FixedContentPackage{ContentText: "{{用户名}}"}
	if _, err = service.fixedContentWithin(context.Background(), template, ids); !errors.Is(err, ErrRuntimeNotReady) {
		t.Fatalf("missing reader err=%v", err)
	}
	lookupErr := errors.New("customer projection unavailable")
	reader := &fixedNameReader{err: lookupErr}
	if err = service.SetCustomerNames(reader); err != nil {
		t.Fatal(err)
	}
	if result, err = service.fixedContentWithin(context.Background(), template, ids); !errors.Is(err, lookupErr) || result != nil {
		t.Fatal("a failed database read must not silently send a fallback")
	}
	reader.err = nil
	reader.values = map[customerdomain.CustomerID]string{1: strings.Repeat("名", 100)}
	template.ContentText = strings.Repeat("文", maxContentText-10) + "{{用户名}}"
	if _, err = service.fixedContentWithin(context.Background(), template, ids); !errors.Is(err, ErrRuntimeInvalid) {
		t.Fatalf("expanded content over the existing limit err=%v", err)
	}
}
