package app

import (
	"context"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

type directUOW struct{}

func (directUOW) Within(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type readerStub struct {
	encrypted   []wecomport.EncryptedArchiveRecord
	plain       []wecomport.PlainArchiveRecord
	calls       int
	emptyAfter  int
	mediaChunks []wecomport.ArchiveMediaChunk
	mediaCalls  []wecomport.ArchiveMediaRequest
}

func (s *readerStub) ArchiveHealth(context.Context) (wecomport.ArchiveHealth, error) {
	return wecomport.ArchiveHealth{}, nil
}
func (s *readerStub) GetChatData(context.Context, uint64, uint32) ([]wecomport.EncryptedArchiveRecord, error) {
	s.calls++
	if s.emptyAfter > 0 && s.calls > s.emptyAfter {
		return nil, nil
	}
	return s.encrypted, nil
}
func (s *readerStub) DecryptArchiveData(context.Context, []wecomport.EncryptedArchiveRecord) ([]wecomport.PlainArchiveRecord, error) {
	return s.plain, nil
}
func (s *readerStub) GetArchiveMedia(_ context.Context, request wecomport.ArchiveMediaRequest) (wecomport.ArchiveMediaChunk, error) {
	s.mediaCalls = append(s.mediaCalls, request)
	if len(s.mediaChunks) == 0 {
		return wecomport.ArchiveMediaChunk{}, ErrProviderPage
	}
	chunk := s.mediaChunks[0]
	s.mediaChunks = s.mediaChunks[1:]
	return chunk, nil
}

type resolverStub struct{ calls int }

func (s *resolverStub) Resolve(context.Context, identitydomain.Reference) (identityport.ResolveResult, error) {
	s.calls++
	return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
}

type lineageStub struct{}

func (lineageStub) CanonicalLineage(context.Context, customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return []customerdomain.CustomerID{1}, nil
}

type staffStub struct {
	calls int
	user  accessdomain.User
	err   error
}

func (s *staffStub) UserByWeComUserID(_ context.Context, wecomUserID string, _ bool) (accessdomain.User, error) {
	s.calls++
	if s.err != nil {
		return accessdomain.User{}, s.err
	}
	if s.user.ID == 0 {
		return accessdomain.User{ID: 1, WeComUserID: wecomUserID}, nil
	}
	return s.user, nil
}

type storeStub struct {
	cursor           uint64
	commits, blocked int
	concurrent       bool
	issue            IngestIssue
	mediaRef         MediaReference
	page             archiveport.CustomerPage
	externalPage     archiveport.ExternalChatRecordPage
	externalQuery    archiveport.ExternalChatRecordQuery
	staffIDs         []int64
}

type v1StoreStub struct {
	storeStub
	v1Page  archiveport.V1ChatRecordPage
	v1Query archiveport.V1ChatRecordQuery
	v1Err   error
}

func (s *v1StoreStub) V1ChatRecords(_ context.Context, query archiveport.V1ChatRecordQuery) (archiveport.V1ChatRecordPage, error) {
	s.v1Query = query
	return s.v1Page, s.v1Err
}

func (s *storeStub) CommittedCursor(context.Context, string) (uint64, error) { return s.cursor, nil }
func (s *storeStub) StartRun(context.Context, SyncRun) (int64, error)        { return 1, nil }
func (s *storeStub) CommitBatch(_ context.Context, b Batch) (BatchResult, error) {
	s.commits++
	if s.concurrent && s.commits == 1 {
		s.cursor = b.EndSeq
		return BatchResult{}, ErrCursorAdvanced
	}
	s.cursor = b.EndSeq
	return BatchResult{CommittedCursor: b.EndSeq}, nil
}
func (s *storeStub) RecordBlockedIssue(_ context.Context, _ string, issue IngestIssue, _ time.Time) error {
	s.blocked++
	s.issue = issue
	return nil
}
func (s *storeStub) FinishRun(context.Context, int64, SyncRunFinish) error { return nil }
func (s *storeStub) CustomerMessages(context.Context, archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	return s.page, nil
}
func (s *storeStub) ExternalCustomerMessages(_ context.Context, query archiveport.ExternalChatRecordQuery) (archiveport.ExternalChatRecordPage, error) {
	s.externalQuery = query
	return s.externalPage, nil
}
func (s *storeStub) CustomerStaffIDs(context.Context, []customerdomain.CustomerID) ([]int64, error) {
	if s.staffIDs != nil {
		return s.staffIDs, nil
	}
	return []int64{1}, nil
}
func (s *storeStub) MediaAccess(context.Context, MediaQuery) (MediaReference, error) {
	if s.mediaRef.ProviderFileRef == "" {
		return MediaReference{}, ErrProviderPage
	}
	return s.mediaRef, nil
}

var _ platformport.UnitOfWork = directUOW{}

func validPage() ([]wecomport.EncryptedArchiveRecord, []wecomport.PlainArchiveRecord) {
	payload := []byte(`{"msgid":"m1","from":"employee","tolist":["wm_customer"],"msgtype":"text","msgtime":1,"text":{"content":"ok"}}`)
	e := []wecomport.EncryptedArchiveRecord{{Seq: 1, MsgID: "m1"}}
	p := []wecomport.PlainArchiveRecord{{Seq: 1, MsgID: "m1", Payload: payload}}
	return e, p
}
func serviceFor(r *readerStub, s *storeStub) Service {
	return Service{Enabled: true, ReadEnabled: true, CorpScope: "wecom-corp:wx-corp", Reader: r, Identity: &resolverStub{}, Lineage: lineageStub{}, Staff: &staffStub{}, StaffDirectory: staffDirectoryStub{}, Store: s, UOW: directUOW{}, PageLimit: 100, PageBudget: 1, Now: func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) }}
}

type staffDirectoryStub struct{}

func (staffDirectoryStub) MessageArchiveStaff(_ context.Context, ids []int64) ([]accessport.MessageArchiveStaff, error) {
	items := make([]accessport.MessageArchiveStaff, 0, len(ids))
	for _, id := range ids {
		items = append(items, accessport.MessageArchiveStaff{ID: id, DisplayName: "员工"})
	}
	return items, nil
}

type recordingStaffDirectory struct{ batches [][]int64 }

func (directory *recordingStaffDirectory) MessageArchiveStaff(_ context.Context, ids []int64) ([]accessport.MessageArchiveStaff, error) {
	directory.batches = append(directory.batches, append([]int64(nil), ids...))
	staff := make([]accessport.MessageArchiveStaff, 0, len(ids))
	for _, id := range ids {
		staff = append(staff, accessport.MessageArchiveStaff{ID: id, DisplayName: "员工"})
	}
	return staff, nil
}
func delivery() archiveport.InboxDelivery {
	return archiveport.InboxDelivery{ID: 1, ReceivedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), Payload: []byte(`{"corp_id":"wx-corp","event":"msgaudit_notify"}`)}
}
func TestMalformedPageStoresProtectedBytesAndDoesNotAdvanceCursor(t *testing.T) {
	e, p := validPage()
	p[0].Payload = []byte("not-json")
	store := &storeStub{}
	err := serviceFor(&readerStub{encrypted: e, plain: p}, store).ProcessArchiveDelivery(context.Background(), delivery())
	if !errors.Is(err, ErrProviderPage) || store.commits != 0 || store.blocked != 1 || string(store.issue.Payload) != "not-json" || store.cursor != 0 {
		t.Fatalf("err=%v state=%+v", err, store)
	}
}
func TestConcurrentCursorRefetchesRatherThanSkippingSecondDelivery(t *testing.T) {
	e, p := validPage()
	store := &storeStub{concurrent: true}
	r := &readerStub{encrypted: e, plain: p, emptyAfter: 1}
	service := serviceFor(r, store)
	service.PageBudget = 2
	err := service.ProcessArchiveDelivery(context.Background(), delivery())
	if err != nil || store.commits != 1 || r.calls != 2 || store.cursor != 1 {
		t.Fatalf("err=%v commits=%d calls=%d cursor=%d", err, store.commits, r.calls, store.cursor)
	}
}

func TestPrivateMediaReadsBoundedChunksAfterLocalAuthorization(t *testing.T) {
	store := &storeStub{mediaRef: MediaReference{Kind: "image", ProviderFileRef: "sdk-file", ExpectedSize: 5, HasExpectedSize: true}}
	reader := &readerStub{mediaChunks: []wecomport.ArchiveMediaChunk{{Data: []byte("abc"), NextIndexBuf: "next"}, {Data: []byte("de"), Finished: true}}}
	service := serviceFor(reader, store)
	content, err := service.ReadPrivateMedia(context.Background(), 1, 7)
	if err != nil || string(content.Data) != "abcde" || content.Kind != "image" || len(reader.mediaCalls) != 2 || reader.mediaCalls[0].IndexBuf != "" || reader.mediaCalls[1].IndexBuf != "next" {
		t.Fatalf("content=%+v err=%v calls=%+v", content, err, reader.mediaCalls)
	}
}

func TestLocalReadWorksWhenProviderIngestionIsDisabled(t *testing.T) {
	store := &storeStub{page: archiveport.CustomerPage{Items: []archiveport.MessageItem{{ID: 1, StaffIDs: []int64{1}}}}}
	service := serviceFor(&readerStub{}, store)
	service.Enabled = false
	service.ReadEnabled = true
	page, err := service.CustomerMessages(context.Background(), archiveport.CustomerQuery{CustomerID: 1})
	if err != nil || len(page.Items) != 1 || len(page.Items[0].StaffNames) != 1 || page.Items[0].StaffNames[0] != "员工" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err = service.ReadPrivateMedia(context.Background(), 1, 7); !errors.Is(err, archiveport.ErrNotReady) {
		t.Fatalf("private media should remain SDK-disabled: %v", err)
	}
}

func TestExternalCustomerMessagesUsesCanonicalLineageAndTrustedIdentity(t *testing.T) {
	store := &storeStub{externalPage: archiveport.ExternalChatRecordPage{Items: []archiveport.ExternalChatRecord{{MessageID: "msg-1", ExternalUserID: "external-1"}}, Total: 1}}
	service := serviceFor(&readerStub{}, store)
	page, err := service.ExternalCustomerMessages(context.Background(), archiveport.ExternalChatRecordQuery{
		CustomerID: 1, ExternalUserID: "external-1", ChatScene: "private", StartAt: time.Unix(1, 0).UTC(), WithUserID: "HuangYouCan", Limit: 20,
	})
	if err != nil || len(page.Items) != 1 || page.Items[0].ExternalUserID != "external-1" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if len(store.externalQuery.CustomerIDs) != 1 || store.externalQuery.CustomerIDs[0] != 1 || store.externalQuery.ExternalUserID != "external-1" {
		t.Fatalf("owner query=%+v", store.externalQuery)
	}
	if _, err = service.ExternalCustomerMessages(context.Background(), archiveport.ExternalChatRecordQuery{CustomerID: 1, ChatScene: "private", StartAt: time.Unix(1, 0).UTC(), Limit: 20}); !errors.Is(err, archiveport.ErrNotReady) {
		t.Fatalf("missing trusted external identity err=%v", err)
	}
}

func TestV1ChatRecordsUsesCanonicalLineageAndFailsClosedForMissingStaffProjection(t *testing.T) {
	store := &v1StoreStub{v1Page: archiveport.V1ChatRecordPage{Items: []archiveport.V1ChatRecord{{MessageID: "archive-message", SourceSystem: "message_archive", SourceRecordID: "7", StaffIDs: []int64{1}}}}}
	service := serviceFor(&readerStub{}, &store.storeStub)
	service.Store = store
	service.Lineage = lineageStub{}
	if _, err := service.V1ChatRecords(context.Background(), archiveport.V1ChatRecordQuery{CustomerID: 1, EndAt: time.Unix(100, 0).UTC(), Limit: 20}); !errors.Is(err, archiveport.ErrNotReady) {
		t.Fatalf("default private without staff error=%v", err)
	}
	page, err := service.V1ChatRecords(context.Background(), archiveport.V1ChatRecordQuery{CustomerID: 1, ChatType: "private", StaffUserID: 1, EndAt: time.Unix(100, 0).UTC(), Limit: 20})
	if err != nil || len(page.Items) != 1 || len(page.Items[0].Staff) != 1 || page.Items[0].Staff[0].DisplayName != "员工" || len(store.v1Query.CustomerIDs) != 1 || store.v1Query.CustomerIDs[0] != 1 {
		t.Fatalf("page=%+v query=%+v err=%v", page, store.v1Query, err)
	}
	service.StaffDirectory = missingStaffDirectory{}
	if _, err = service.V1ChatRecords(context.Background(), archiveport.V1ChatRecordQuery{CustomerID: 1, ChatType: "private", StaffUserID: 1, EndAt: time.Unix(100, 0).UTC(), Limit: 20}); !errors.Is(err, archiveport.ErrNotReady) {
		t.Fatalf("missing staff projection error=%v", err)
	}
}

func TestV1ChatStaffIDUsesOnlyLocalAccessProjection(t *testing.T) {
	staff := &staffStub{user: accessdomain.User{ID: 9, WeComUserID: "staff-9"}}
	reader := &readerStub{}
	service := serviceFor(reader, &storeStub{})
	service.Staff = staff
	id, err := service.V1ChatStaffID(context.Background(), "staff-9")
	if err != nil || id != 9 || staff.calls != 1 || reader.calls != 0 {
		t.Fatalf("id=%d err=%v staff_calls=%d provider_calls=%d", id, err, staff.calls, reader.calls)
	}
	staff.err = accessdomain.ErrNotFound
	if _, err = service.V1ChatStaffID(context.Background(), "absent-staff"); !errors.Is(err, archiveport.ErrStaffNotFound) {
		t.Fatalf("absent staff error=%v", err)
	}
	service.Staff = nil
	if _, err = service.V1ChatStaffID(context.Background(), "staff-9"); !errors.Is(err, archiveport.ErrNotReady) {
		t.Fatalf("missing local directory error=%v", err)
	}
}

type missingStaffDirectory struct{}

func (missingStaffDirectory) MessageArchiveStaff(context.Context, []int64) ([]accessport.MessageArchiveStaff, error) {
	return []accessport.MessageArchiveStaff{}, nil
}

func TestCustomerMessageStaffLookupDeduplicatesAcrossMessages(t *testing.T) {
	items := make([]archiveport.MessageItem, 1002)
	for index := range items {
		staffID := int64(index + 1)
		if index == 1001 {
			staffID = 1
		}
		items[index] = archiveport.MessageItem{ID: int64(index + 1), StaffIDs: []int64{staffID}}
	}
	store := &storeStub{page: archiveport.CustomerPage{Items: items}}
	directory := &recordingStaffDirectory{}
	service := serviceFor(&readerStub{}, store)
	service.StaffDirectory = directory
	page, err := service.CustomerMessages(context.Background(), archiveport.CustomerQuery{CustomerID: 1})
	if err != nil || len(directory.batches) != 2 || len(directory.batches[0]) != 1000 || len(directory.batches[1]) != 1 || len(page.Items[1000].StaffNames) != 1 || len(page.Items[1001].StaffNames) != 1 {
		t.Fatalf("batch sizes=%v last=%+v duplicate=%+v err=%v", staffBatchSizes(directory.batches), page.Items[1000], page.Items[1001], err)
	}

	directory.batches = nil
	store.page = archiveport.CustomerPage{}
	store.staffIDs = make([]int64, 0, 1002)
	for id := int64(1); id <= 1001; id++ {
		store.staffIDs = append(store.staffIDs, id)
	}
	store.staffIDs = append(store.staffIDs, 1)
	options, err := service.CustomerStaff(context.Background(), 1)
	if err != nil || len(options) != 1001 || len(directory.batches) != 2 || len(directory.batches[0]) != 1000 || len(directory.batches[1]) != 1 {
		t.Fatalf("options=%d batch sizes=%v err=%v", len(options), staffBatchSizes(directory.batches), err)
	}
}

func staffBatchSizes(batches [][]int64) []int {
	sizes := make([]int, len(batches))
	for index := range batches {
		sizes[index] = len(batches[index])
	}
	return sizes
}

func TestPageResolutionCachesTrustedExternalAndStaffReads(t *testing.T) {
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:wx-corp", Value: "wm_customer", Source: "wecom.message_archive"})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"from":"employee","tolist":["wm_customer"],"msgtype":"text","msgtime":1,"text":{"content":"ok"}}`)
	encrypted := []wecomport.EncryptedArchiveRecord{{Seq: 1, MsgID: "m1"}, {Seq: 2, MsgID: "m2"}}
	plain := []wecomport.PlainArchiveRecord{{Seq: 1, MsgID: "m1", Payload: append([]byte(nil), payload...), ExternalIdentities: []wecomport.TrustedArchiveExternalIdentity{{Value: "wm_customer", Fact: fact}}}, {Seq: 2, MsgID: "m2", Payload: append([]byte(nil), payload...), ExternalIdentities: []wecomport.TrustedArchiveExternalIdentity{{Value: "wm_customer", Fact: fact}}}}
	plain[1].Payload = []byte(`{"from":"employee","tolist":["wm_customer"],"msgtype":"text","msgtime":2,"text":{"content":"ok"}}`)
	resolver := &resolverStub{}
	staff := &staffStub{}
	service := serviceFor(&readerStub{encrypted: encrypted, plain: plain}, &storeStub{})
	service.Identity, service.Staff = resolver, staff
	err = service.ProcessArchiveDelivery(context.Background(), delivery())
	if !errors.Is(err, archiveport.ErrWorkBudgetExceeded) || resolver.calls != 1 || staff.calls != 1 {
		t.Fatalf("err=%v resolve=%d staff=%d", err, resolver.calls, staff.calls)
	}
}
