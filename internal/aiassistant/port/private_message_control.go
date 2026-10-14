package port

type PrivateMessageControlCommand struct {
	Actor           Actor
	IdempotencyKey  string
	PlanID          PlanID
	RecipientID     RecipientID
	ExpectedVersion int64
	Action          string
	Reason          string
}
