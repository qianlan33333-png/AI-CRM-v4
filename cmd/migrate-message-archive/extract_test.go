package main

import "testing"

func TestExtractedArchiveSourceRowRequiresDonorWrapperConsistency(t *testing.T) {
	valid := `{"seq":7,"encrypted_record":{"msgid":"source-msg"},"decrypted_message":{"msgid":"source-msg","from":"staff","tolist":["wm-contact"],"msgtype":"text","msgtime":1788336000,"text":{"content":"preserved"}}}`
	row, err := extractedArchiveSourceRow(11, 7, "source-msg", "", "", valid)
	if err != nil || string(row.Payload) == valid || row.SourcePayloadDigest == "" {
		t.Fatalf("valid donor wrapper row=%+v err=%v", row, err)
	}
	seqMismatch := `{"seq":8,"encrypted_record":{"msgid":"source-msg"},"decrypted_message":{"msgid":"source-msg","from":"staff","tolist":["wm-contact"],"msgtype":"text","msgtime":1788336000,"text":{"content":"preserved"}}}`
	if _, err = extractedArchiveSourceRow(11, 7, "source-msg", "", "", seqMismatch); err == nil {
		t.Fatal("mismatched wrapper sequence was accepted")
	}
	messageMismatch := `{"seq":7,"encrypted_record":{"msgid":"different"},"decrypted_message":{"msgid":"different","from":"staff","tolist":["wm-contact"],"msgtype":"text","msgtime":1788336000,"text":{"content":"preserved"}}}`
	if _, err = extractedArchiveSourceRow(11, 7, "source-msg", "", "", messageMismatch); err == nil {
		t.Fatal("mismatched wrapper message ID was accepted")
	}
}
