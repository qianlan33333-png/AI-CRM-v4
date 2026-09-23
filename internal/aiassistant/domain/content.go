package domain

import (
	"encoding/json"

	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func FreezeContent(blocks []aiassistantport.ContentBlock) ([]byte, effectport.Digest, error) {
	if len(blocks) == 0 || len(blocks) > aiassistantport.MaxMessagesPerTarget {
		return nil, "", ErrInvalidPlan
	}
	for _, block := range blocks {
		if !block.Valid() {
			return nil, "", ErrInvalidPlan
		}
	}
	payload, err := json.Marshal(blocks)
	if err != nil {
		return nil, "", ErrInvalidPlan
	}
	return payload, effectport.Hash("aiassistant.content.v1", string(payload)), nil
}
