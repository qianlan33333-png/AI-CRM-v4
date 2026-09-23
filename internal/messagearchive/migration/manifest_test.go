package migration

import (
	"encoding/json"
	"testing"
)

func TestHistoricalManifestKeepsParticipantKindsWithoutPromotingIdentity(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"schema_version": SchemaVersion, "source_name": "offline-export", "corp_scope": "wecom-corp:wx-corp", "records": []map[string]any{{"source_row_key": "row-1", "seq": 1, "msgid": "m-1", "payload": map[string]any{"msgid": "m-1", "from": "employee", "tolist": []string{"wm_customer"}, "msgtype": "text", "msgtime": 1, "text": map[string]string{"content": "fixture"}}}}})
	manifest, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	message, err := manifest.Normalized(manifest.Records[0])
	if err != nil || message.Participants[0].ActorType != "staff" || message.Participants[0].StaffUserID != 0 || message.Participants[1].CustomerID != 0 || message.Participants[1].ResolutionStatus != "not_found" {
		t.Fatalf("message=%+v err=%v", message, err)
	}
}
