package externaleffects

import (
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	"testing"
)

func TestOpsNotificationDoesNotStealBusinessDeliveryLane(t *testing.T) {
	e := Envelope{Owner: OwnerAdminOps, Kind: KindFeishuOpsNotification, SourceRefDigest: Hash("s"), TargetRefDigest: Hash("t"), PayloadDigest: Hash("p"), PolicyVersionHash: Hash("v")}
	c := AcceptCommand{ReceiptKey: Hash("r"), Envelope: e, Lane: port.LaneOpsNotification}
	if !c.Valid() || effectQueue(e.Kind, c.Lane) != jobqueue.OpsNotificationQueue || !projectsStaleAttempt(e.Kind) {
		t.Fatal("ops lane not registered")
	}
	for _, lane := range []port.Lane{"", port.LaneOutboundExcel, port.LaneOutboundMedia} {
		c.Lane = lane
		if c.Valid() {
			t.Fatal("ops intent accepted on business lane")
		}
	}
	c.Lane = port.LaneOpsNotification
	c.Envelope = envelopeForTest()
	if c.Valid() {
		t.Fatal("business intent accepted on ops lane")
	}
	e.Owner = OwnerOutbound
	if e.Valid() {
		t.Fatal("ops kind accepted for another owner")
	}
	if CanTransition(StateUnknown, StateQueued) {
		t.Fatal("unknown notifications can be replayed")
	}
}
