package port

// PaymentV1Intent is the versioned, digest-only contract shared by Payment and
// External Effects. Provider payloads and credentials never enter EER.
type PaymentV1Intent struct {
	Kind                                                               Kind
	ReceiptKey                                                         Digest
	SourceRefDigest, TargetRefDigest, PayloadDigest, PolicyVersionHash Digest
}

func (intent PaymentV1Intent) AcceptCommand() (AcceptCommand, bool) {
	envelope := Envelope{Owner: OwnerPayment, Kind: intent.Kind, SourceRefDigest: intent.SourceRefDigest, TargetRefDigest: intent.TargetRefDigest, PayloadDigest: intent.PayloadDigest, PolicyVersionHash: intent.PolicyVersionHash}
	command := AcceptCommand{ReceiptKey: intent.ReceiptKey, Envelope: envelope}
	return command, command.Valid()
}
