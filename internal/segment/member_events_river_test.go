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
		return segmentport.MemberEventPage{Items: []segmentport.MemberEnteredV1{{EventID: "audmem_9_1", PackageID: 2, SnapshotID: 9, ConfigurationVersionID: 3, CustomerID: customerdomain.CustomerID(1), OccurredAt: time.Unix(1, 0)}}, NextCursor: "1"}, nil
	}
	return segmentport.MemberEventPage{Items: []segmentport.MemberEnteredV1{{EventID: "audmem_9_2", PackageID: 2, SnapshotID: 9, ConfigurationVersionID: 3, CustomerID: customerdomain.CustomerID(2), OccurredAt: time.Unix(1, 0)}}}, nil
}

type memberEventSinkStub struct{ ids []customerdomain.CustomerID }

func (s *memberEventSinkStub) HandleAudienceMemberEntered(_ context.Context, event segmentport.MemberEnteredV1) error {
	s.ids = append(s.ids, event.CustomerID)
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
	if source.calls != 2 || len(sink.ids) != 2 || sink.ids[0] != 1 || sink.ids[1] != 2 {
		t.Fatalf("source calls=%d ids=%v", source.calls, sink.ids)
	}
}
