package port

import (
	"encoding/json"
	e "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"strconv"
)

func NativeInvitationPayload(ids []string, o w.InvitationJoinWayOptions) []byte {
	raw, _ := json.Marshal(struct {
		ChatIDs []string                   `json:"chat_ids"`
		Options w.InvitationJoinWayOptions `json:"native_options"`
	}{ids, o})
	return raw
}

func NativeInvitationEnvelope(id int64, source e.Digest, ids []string, o w.InvitationJoinWayOptions) e.Envelope {
	return e.Envelope{Owner: e.OwnerOutbound, Kind: e.KindInvitationCode, SourceRefDigest: source,
		TargetRefDigest:   e.Hash("invitation.plan.target.v3", strconv.FormatInt(id, 10)),
		PayloadDigest:     e.Hash("invitation.plan-code.v3", string(NativeInvitationPayload(ids, o))),
		PolicyVersionHash: e.Hash("invitation.code.policy.v3")}
}
