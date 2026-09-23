package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

func TestGroupOpsIntentFreezesSourceWithoutTemporaryProviderID(t *testing.T) {
	frozen := mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{Reference: mediaport.GroupOpsMaterialReference{Kind: "image", ID: 7}, SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	resolver, err := newGroupOpsMaterialAdapter(materialCapturerStub{capture: func(context.Context, mediaport.GroupOpsMaterialPlan) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
		return frozen, nil
	}}, materialFreezerStub{freeze: func(context.Context, mediaport.GroupOpsMaterialSourceSnapshot, time.Time) (mediaport.GroupOpsMaterialSnapshot, error) {
		return mediaport.GroupOpsMaterialSnapshot{}, errors.New("temporary credential freezer must not run during acceptance")
	}})
	if err != nil {
		t.Fatal(err)
	}
	intent := resolver.(groupopsport.MaterialIntentSnapshotResolver)
	raw, _, factsRaw, _, err := intent.ResolveMaterialIntentSnapshot(context.Background(), groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: 7}}}, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot mediaport.GroupOpsMaterialIntentSnapshot
	var facts struct {
		Preparations []json.RawMessage `json:"preparations"`
	}
	if json.Unmarshal(raw, &snapshot) != nil || mediaport.ValidateGroupOpsMaterialIntentSnapshot(snapshot) != nil || len(snapshot.Attachments) != 1 || snapshot.Attachments[0].MsgType != "image" || snapshot.Attachments[0].MediaID != "" || json.Unmarshal(factsRaw, &facts) != nil || facts.Preparations == nil || len(facts.Preparations) != 0 {
		t.Fatalf("snapshot=%s facts=%s", raw, factsRaw)
	}
}
