package store

import "testing"

func TestProfitSharingProviderIntentRequiresExactlyOneOwner(t *testing.T) {
	tests := []struct {
		name                                  string
		receiverID, instructionID, unfreezeID int64
		want                                  bool
	}{
		{name: "receiver", receiverID: 1, want: true},
		{name: "instruction", instructionID: 1, want: true},
		{name: "unfreeze", unfreezeID: 1, want: true},
		{name: "none", want: false},
		{name: "receiver and instruction", receiverID: 1, instructionID: 2, want: false},
		{name: "receiver and unfreeze", receiverID: 1, unfreezeID: 2, want: false},
		{name: "instruction and unfreeze", instructionID: 1, unfreezeID: 2, want: false},
		{name: "all", receiverID: 1, instructionID: 2, unfreezeID: 3, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasExactlyOneProfitSharingIntentOwner(test.receiverID, test.instructionID, test.unfreezeID); got != test.want {
				t.Fatalf("owners receiver=%d instruction=%d unfreeze=%d got=%t want=%t", test.receiverID, test.instructionID, test.unfreezeID, got, test.want)
			}
		})
	}
}
