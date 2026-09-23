package app

import (
	"encoding/json"
	"time"
)

type RuntimeReceipt struct {
	ID                           int64
	Operation, ActorScope, State string
	KeyDigest, PayloadDigest     [32]byte
	Result                       json.RawMessage
}
type RuntimeReservation struct {
	Operation, ActorScope    string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}
type RuntimeFact struct {
	Kind                 string
	ID                   int64
	Operation, EventType string
	Actor                int64
	Payload              json.RawMessage
	Key                  string
	At                   time.Time
}
