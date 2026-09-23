package app

import "testing"

func TestValidCredentialIdentifierRejectsPathDotSegments(t *testing.T) {
	for _, value := range []string{".", ".."} {
		if validCredentialIdentifier(value) {
			t.Fatalf("dot segment %q must be rejected", value)
		}
	}
	for _, value := range []string{"client.one", "client..two", "_client"} {
		if !validCredentialIdentifier(value) {
			t.Fatalf("ordinary identifier %q must remain valid", value)
		}
	}
}
