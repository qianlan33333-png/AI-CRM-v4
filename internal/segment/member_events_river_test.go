package segment

import (
	"context"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type memberEventSourceStub struct{ calls int }

func (s *memberEventSourceStub) MemberEvents(_ context.Context, _ segmentport.SnapshotID, cursor string, _ int) (segmentport.MemberEventPage, error) {
	s.calls++
	if cursor == "" {
		return segmentport.MemberEventPage{Items: []segmentport.MemberEventV1{{Kind: segmentport.EventAudienceMemberEnteredV1, MemberEntered: &segmentport.MemberEnteredV1{EventID: "audmem_9_1", PackageID: 2, SnapshotID: 9, ConfigurationVersionID: 3, CustomerID: customerdomain.CustomerID(1), OccurredAt: time.Unix(1, 0)}}}, NextCursor: "1"}, nil
	}
	return segmentport.MemberEventPage{Items: []segmentport.MemberEventV1{{Kind: segmentport.EventAudienceMemberPaidQualifiedV1, PaidQualified: &segmentport.MemberPaidQualifiedV1{EventID: "audpay_9_2_802", PackageID: 2, SnapshotID: 9, ConfigurationVersionID: 3, CustomerID: customerdomain.CustomerID(2), PaidOrderID: 802, PaidAt: time.Unix(2, 0), OccurredAt: time.Unix(3, 0)}}}}, nil
}

type memberEventSinkStub struct {
	ids          []customerdomain.CustomerID
	paidOrderIDs []int64
}

func (s *memberEventSinkStub) HandleAudienceMemberEntered(_ context.Context, event segmentport.MemberEnteredV1) error {
	s.ids = append(s.ids, event.CustomerID)
	return nil
}

func (s *memberEventSinkStub) HandleAudienceMemberPaidQualified(_ context.Context, event segmentport.MemberPaidQualifiedV1) error {
	s.ids = append(s.ids, event.CustomerID)
	s.paidOrderIDs = append(s.paidOrderIDs, event.PaidOrderID)
	return nil
}

func TestAudienceMemberEventWorkerDispatchesEveryDurableEvent(t *testing.T) {
	source, sink := &memberEventSourceStub{}, &memberEventSinkStub{}
	worker := NewAudienceMemberEventDispatchWorker()
	if err := worker.Bind(source, sink); err != nil {
		t.Fatal(err)
	}
	job := &river.Job[AudienceMemberEventDispatchJobArgs]{JobRow: &rivertype.JobRow{}, Args: AudienceMemberEventDispatchJobArgs{SnapshotID: 9}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if source.calls != 2 || len(sink.ids) != 2 || sink.ids[0] != 1 || sink.ids[1] != 2 || len(sink.paidOrderIDs) != 1 || sink.paidOrderIDs[0] != 802 {
		t.Fatalf("source calls=%d ids=%v paid_order_ids=%v", source.calls, sink.ids, sink.paidOrderIDs)
	}
}
