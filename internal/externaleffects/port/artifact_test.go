package port

import "testing"

func TestResultArtifactValidatesDigestKindAndSize(t *testing.T) {
	payload := []byte(`{"groups":[]}`)
	artifact := ResultArtifact{Kind: "wecom.tag_catalog.snapshot.v1", Payload: payload, Digest: Hash("external-effect.artifact.v1", "wecom.tag_catalog.snapshot.v1", string(payload))}
	if !artifact.Valid() {
		t.Fatal("valid artifact rejected")
	}
	artifact.Digest = Hash("other")
	if artifact.Valid() {
		t.Fatal("digest drift accepted")
	}
	artifact = ResultArtifact{Kind: "x", Digest: Hash("external-effect.artifact.v1", "x", "x"), Payload: make([]byte, 256<<10+1)}
	if artifact.Valid() {
		t.Fatal("oversize artifact accepted")
	}
}
