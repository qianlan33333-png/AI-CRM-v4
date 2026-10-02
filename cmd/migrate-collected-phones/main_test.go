package main

import (
	"context"
	"errors"
	"testing"

	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var _ platformport.UnitOfWork = directUOW{}

type directUOW struct{}

func (directUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type sourceStub struct {
	calls  int
	failID int64
}

func (*sourceStub) List(_ context.Context, after, through int64) ([]int64, error) {
	var ids []int64
	for _, id := range []int64{3, 9, 11} {
		if id > after && (through == 0 || id <= through) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func (s *sourceStub) Replay(_ context.Context, id int64) (identityport.DeclaredAttachResult, error) {
	s.calls++
	if id == s.failID {
		return identityport.DeclaredAttachResult{}, errors.New("private source detail")
	}
	if id == 9 {
		return identityport.DeclaredAttachResult{Status: identityport.DeclaredReplayed, ReplayOf: identityport.DeclaredConflict}, nil
	}
	return identityport.DeclaredAttachResult{Status: identityport.DeclaredAttached}, nil
}
func TestInspectNeverAttemptsBinding(t *testing.T) {
	s := &sourceStub{}
	r, e := replay(context.Background(), directUOW{}, s, "inspect", "order", 0, 9)
	if e != nil || r.Candidates != 2 || s.calls != 0 || r.LastCompletedID != 9 {
		t.Fatalf("report=%+v err=%v calls=%d", r, e, s.calls)
	}
}
func TestApplyReportsReplayOutcomeAndResumesAfterFailedItem(t *testing.T) {
	s := &sourceStub{failID: 11}
	r, e := replay(context.Background(), directUOW{}, s, "apply", "survey", 0, 0)
	if e == nil || r.LastCompletedID != 9 || r.FailedSourceID != 11 || r.Outcomes["replayed:conflict"] != 1 {
		t.Fatalf("report=%+v err=%v", r, e)
	}
	s.failID = 0
	r, e = replay(context.Background(), directUOW{}, s, "apply", "survey", r.LastCompletedID, 0)
	if e != nil || r.Candidates != 1 || r.LastCompletedID != 11 {
		t.Fatalf("report=%+v err=%v", r, e)
	}
}
