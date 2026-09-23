package domain

import (
	"bytes"
	"testing"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
)

func TestFreezeContentIsDeterministicAndOrdered(t *testing.T) {
	blocks := []aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText, Text: "first"}, {Kind: aiassistantport.ContentText, Text: "second"}}
	one, oneDigest, err := FreezeContent(blocks)
	if err != nil {
		t.Fatal(err)
	}
	two, twoDigest, err := FreezeContent(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) || oneDigest != twoDigest {
		t.Fatal("content freeze is not deterministic")
	}
	reversed := []aiassistantport.ContentBlock{blocks[1], blocks[0]}
	_, reversedDigest, err := FreezeContent(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if reversedDigest == oneDigest {
		t.Fatal("message order must participate in digest")
	}
}

func TestFreezeContentRejectsEmptyBlock(t *testing.T) {
	if _, _, err := FreezeContent([]aiassistantport.ContentBlock{{Kind: aiassistantport.ContentText}}); err == nil {
		t.Fatal("expected invalid empty text")
	}
}
