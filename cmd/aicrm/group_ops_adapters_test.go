package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	media "github.com/qianlan33333-png/AI-CRM-v3/internal/media"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestCompositionProviderDisabledAdaptersFailClosed(t *testing.T) {
	directory := providerDisabledGroupOpsDirectory{}
	if _, err := directory.ListOwnedGroups(context.Background(), 7, 1); !errors.Is(err, groupopsapp.ErrProviderDisabled) {
		t.Fatalf("directory list err=%v, want provider disabled", err)
	}
	if _, err := directory.RefreshOperationMembers(context.Background(), 1); !errors.Is(err, groupopsapp.ErrProviderDisabled) {
		t.Fatalf("directory refresh err=%v, want provider disabled", err)
	}
	evidence := providerDisabledGroupOpsEvidence{}
	if _, err := evidence.VerifyReconciliationEvidence(context.Background(), groupopsport.ReconciliationEvidence{ExternalEffectID: "effect-1"}); !errors.Is(err, groupopsapp.ErrProviderDisabled) {
		t.Fatalf("evidence verify err=%v, want provider disabled", err)
	}
}

func TestWeComGroupOpsDirectoryReadsAccessWithinShortUOWBeforeProviderRead(t *testing.T) {
	access := transactionRequiredGroupOpsAccess{owner: accessdomain.User{ID: 7, Active: true, WeComUserID: "staff-seven", DisplayName: "王七"}, users: []accessdomain.User{
		{ID: 7, Active: true, WeComUserID: "staff-seven", DisplayName: "王七"},
		{ID: 8, Active: true, WeComUserID: "staff-eight", DisplayName: "李八"},
	}}
	uow := &groupOpsDirectoryUOW{}
	provider := &groupOpsDirectoryProvider{staff: []string{"staff-seven", "staff-eight"}, profiles: []wecomport.ContactStaffProfile{{UserID: "staff-seven", DisplayName: "真实王七"}, {UserID: "staff-eight", DisplayName: "真实李八"}}, profileState: "ready", groups: []wecomport.GroupChatListItem{{ChatID: "chat-seven", Status: 0}}, details: map[string]wecomport.GroupChat{
		"chat-seven": {ChatID: "chat-seven", OwnerUserID: "staff-seven", Name: "七号运营群", MemberCount: 3},
	}}
	directory := &wecomGroupOpsDirectory{uow: uow, enabled: true, groups: provider, staffs: provider, profiles: provider, staff: groupOpsStaffAdapter{access: access}}

	groups, err := directory.ListOwnedGroups(context.Background(), 7, 100)
	if err != nil || !groups.Complete || len(groups.Items) != 1 || groups.Items[0].DisplayName != "七号运营群" || groups.Items[0].OwnerStaffID != 7 {
		t.Fatalf("group directory=%+v err=%v", groups, err)
	}
	members, err := directory.RefreshOperationMembers(context.Background(), 100)
	if err != nil || len(members) != 2 || members[0].StaffID != 7 || members[0].DisplayName != "真实王七" || members[0].NameSource != "wecom_profile" || members[0].SenderUserID != "staff-seven" || members[1].StaffID != 8 || members[1].DisplayName != "真实李八" || members[1].SenderUserID != "staff-eight" {
		t.Fatalf("operation members=%+v err=%v", members, err)
	}
	if uow.calls != 2 || provider.calledWithinUOW {
		t.Fatalf("uow calls=%d provider_called_within_uow=%t", uow.calls, provider.calledWithinUOW)
	}
}

func TestWeComGroupOpsDirectoryPreservesLocalReadFailure(t *testing.T) {
	lookupErr := errors.New("database unavailable")
	directory := &wecomGroupOpsDirectory{
		uow:     &groupOpsDirectoryUOW{err: lookupErr},
		enabled: true,
		groups:  &groupOpsDirectoryProvider{},
		staff:   groupOpsStaffAdapter{access: transactionRequiredGroupOpsAccess{}},
	}
	_, err := directory.ListOwnedGroups(context.Background(), 7, 100)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("ListOwnedGroups error=%v, want local lookup error", err)
	}
}

type groupOpsDirectoryTransactionKey struct{}

type groupOpsDirectoryUOW struct {
	calls int
	err   error
}

func (uow *groupOpsDirectoryUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	uow.calls++
	if uow.err != nil {
		return uow.err
	}
	return callback(context.WithValue(ctx, groupOpsDirectoryTransactionKey{}, true))
}

type transactionRequiredGroupOpsAccess struct {
	accessport.Repository
	owner accessdomain.User
	users []accessdomain.User
}

func (access transactionRequiredGroupOpsAccess) UserByID(ctx context.Context, id int64, _ bool) (accessdomain.User, error) {
	if ctx.Value(groupOpsDirectoryTransactionKey{}) != true {
		return accessdomain.User{}, errors.New("access read must use UOW")
	}
	if id != access.owner.ID {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return access.owner, nil
}

func (access transactionRequiredGroupOpsAccess) ListUsers(ctx context.Context) ([]accessdomain.User, error) {
	if ctx.Value(groupOpsDirectoryTransactionKey{}) != true {
		return nil, errors.New("access read must use UOW")
	}
	return append([]accessdomain.User(nil), access.users...), nil
}

type groupOpsDirectoryProvider struct {
	staff           []string
	profiles        []wecomport.ContactStaffProfile
	profileState    string
	profileCode     string
	profileErr      error
	groups          []wecomport.GroupChatListItem
	details         map[string]wecomport.GroupChat
	calledWithinUOW bool
}

func (provider *groupOpsDirectoryProvider) DirectoryReady() bool { return true }

func (provider *groupOpsDirectoryProvider) ListContactStaff(ctx context.Context) ([]string, error) {
	if ctx.Value(groupOpsDirectoryTransactionKey{}) != nil {
		provider.calledWithinUOW = true
	}
	return append([]string(nil), provider.staff...), nil
}

func (provider *groupOpsDirectoryProvider) ReadContactStaffProfiles(ctx context.Context, ids []string) (wecomport.ContactStaffProfileSnapshot, error) {
	if ctx.Value(groupOpsDirectoryTransactionKey{}) != nil {
		provider.calledWithinUOW = true
	}
	if provider.profileErr != nil {
		return wecomport.ContactStaffProfileSnapshot{}, provider.profileErr
	}
	return wecomport.ContactStaffProfileSnapshot{Items: append([]wecomport.ContactStaffProfile(nil), provider.profiles...), ProfileReadState: provider.profileState, ProfileErrorCode: provider.profileCode}, nil
}

func (provider *groupOpsDirectoryProvider) BatchExternalContacts(context.Context, string, string, int) (wecomport.ExternalContactPage, error) {
	return wecomport.ExternalContactPage{}, errors.New("not used by Group Ops directory")
}

func (provider *groupOpsDirectoryProvider) ListGroupChats(ctx context.Context, _ string, _ string, _ int) (wecomport.GroupChatPage, error) {
	if ctx.Value(groupOpsDirectoryTransactionKey{}) != nil {
		provider.calledWithinUOW = true
	}
	return wecomport.GroupChatPage{Items: append([]wecomport.GroupChatListItem(nil), provider.groups...)}, nil
}

func (provider *groupOpsDirectoryProvider) GetGroupChat(ctx context.Context, chatID string) (wecomport.GroupChat, error) {
	if ctx.Value(groupOpsDirectoryTransactionKey{}) != nil {
		provider.calledWithinUOW = true
	}
	value, found := provider.details[chatID]
	if !found {
		return wecomport.GroupChat{}, errors.New("group chat not found")
	}
	return value, nil
}

func TestGroupOpsDispatchReaderRejectsChangedSenderBeforeProviderCall(t *testing.T) {
	value := groupopsport.DispatchExecution{ExecutionID: 9, ExternalEffectID: "eer_9", State: groupopsport.ExecutionAccepted, TargetReference: "chat-9", SenderUserID: "owner-9"}
	reader := groupOpsDispatchReader{uow: preparationUOWStub{}, execution: dispatchExecutionStub{value: value}, senders: dispatchSenderStub{sender: "owner-9", found: true}}
	loaded, err := reader.LoadDispatchExecution(context.Background(), "eer_9")
	if err != nil || loaded.ExecutionID != 9 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	reader.senders = dispatchSenderStub{sender: "replacement-owner", found: true}
	if _, err = reader.LoadDispatchExecution(context.Background(), "eer_9"); err == nil {
		t.Fatal("changed group sender reached outbound provider boundary")
	}
}

type dispatchExecutionStub struct {
	value groupopsport.DispatchExecution
}

func (stub dispatchExecutionStub) LoadDispatchExecution(_ context.Context, effectID string) (groupopsport.DispatchExecution, error) {
	if effectID != stub.value.ExternalEffectID {
		return groupopsport.DispatchExecution{}, errors.New("unexpected effect")
	}
	return stub.value, nil
}

type dispatchSenderStub struct {
	sender string
	found  bool
}

func (stub dispatchSenderStub) ResolveExecutionSender(context.Context, string) (string, bool, error) {
	return stub.sender, stub.found, nil
}

func TestGroupOpsMaterialCompositionBindsMediaPortsAndFailsClosedWithoutPreparedReceipt(t *testing.T) {
	cap := materialCapturerStub{capture: func(_ context.Context, plan mediaport.GroupOpsMaterialPlan) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
		return mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{
			Reference:    plan.References[0],
			SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}}}, nil
	}}
	mediaBindings, err := media.NewModuleRegistration().BindContentDelivery(contentDeliveryServiceStub{}, cap)
	if err != nil || mediaBindings.SourceCapturer == nil || mediaBindings.ContentDelivery == nil {
		t.Fatalf("media bindings=%+v err=%v", mediaBindings, err)
	}
	preparationStore := &preparationStoreStub{}
	preparationBindings, err := media.NewModuleRegistration().BindMaterialPreparation(preparationReaderStub{}, mediaapp.NewGroupOpsMaterialPreparationWriter(preparationUOWStub{}, preparationStore))
	if err != nil || preparationBindings.Reader == nil || preparationBindings.Writer == nil {
		t.Fatalf("preparation bindings=%+v err=%v", preparationBindings, err)
	}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newGroupOpsMaterialAdapter(mediaBindings.SourceCapturer, freezer)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = resolver.ResolveMaterialSnapshot(context.Background(), groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: 7}}}, time.Now().UTC().Add(time.Hour))
	if err == nil {
		t.Fatal("material resolver accepted a snapshot without a Media preparation receipt")
	}
}

func TestMediaPreparedPlanReaderBuildsInviteFromCapturedFacts(t *testing.T) {
	const inviteURL = "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef"
	sources := mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{
		Reference:      mediaport.GroupOpsMaterialReference{Kind: "group_invite", ID: 10},
		SourceDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProviderFields: mediaport.GroupOpsProviderReadyAttachment{MsgType: "link", Title: "加入体验群", URL: inviteURL, Description: "资料"},
	}}}
	reader := mediaPreparedPlanReader{}
	prepared, err := reader.ReadPreparedGroupOpsPlan(context.Background(), sources, time.Now().UTC().Add(time.Hour))
	if err != nil || len(prepared.Items) != 1 || prepared.Items[0].ReceiptDigest != "" || !prepared.Items[0].ReadyUntil.IsZero() || prepared.Items[0].Attachment.URL != inviteURL {
		t.Fatalf("prepared=%+v err=%v", prepared, err)
	}
	freezer, err := groupopsmaterial.NewFreezer(reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := freezer.FreezeGroupOpsMaterial(context.Background(), sources, time.Now().UTC().Add(time.Hour))
	if err != nil || len(snapshot.Attachments) != 1 || snapshot.Attachments[0].Title != "加入体验群" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	resolver, err := newGroupOpsMaterialAdapter(materialCapturerStub{capture: func(_ context.Context, plan mediaport.GroupOpsMaterialPlan) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
		if len(plan.References) != 1 || plan.References[0].Kind != "group_invite" {
			t.Fatalf("plan=%+v", plan)
		}
		return sources, nil
	}}, freezer)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := resolver.ResolveMaterialSnapshot(context.Background(), groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "group_invite", ID: 10}}}, time.Now().UTC().Add(time.Hour))
	if err != nil || !strings.Contains(string(raw), "加入体验群") || !strings.Contains(string(raw), inviteURL) {
		t.Fatalf("resolved=%s err=%v", raw, err)
	}
}

func TestMediaPreparedPlanReaderUsesReadyMaterialForMedia(t *testing.T) {
	sources := mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{
		Reference:      mediaport.GroupOpsMaterialReference{Kind: "image", ID: 7},
		SourceDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProviderFields: mediaport.GroupOpsProviderReadyAttachment{MsgType: "image"},
	}}}
	requiredThrough := time.Now().UTC().Add(time.Hour)
	contentDigest := [32]byte{}
	for index := range contentDigest {
		contentDigest[index] = 0xaa
	}
	status := &groupOpsMaterialStatusStub{result: outboundport.MaterialResult{State: "ready", EffectID: "material-effect-7", MediaID: "provider-image-7", CredentialUsable: true, ExpiresAt: requiredThrough.Add(time.Hour)}}
	reader := mediaPreparedPlanReader{
		sources:     groupOpsMaterialSourceStub{source: outboundport.MaterialSourceSnapshot{SourceRef: "image:7", SourceType: "image", ContentDigest: contentDigest, FileName: "image-7.png", MediaType: "image/png", SizeBytes: 7, SnapshotVersion: 1}},
		preparer:    status,
		scopeDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	freezer, err := groupopsmaterial.NewFreezer(reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := freezer.FreezeGroupOpsMaterial(context.Background(), sources, requiredThrough)
	if err != nil || len(snapshot.Attachments) != 1 || snapshot.Attachments[0].MediaID != "provider-image-7" || status.calls != 1 {
		t.Fatalf("snapshot=%+v calls=%d err=%v", snapshot, status.calls, err)
	}
}

func TestDeterministicPreparationWriterThenFreezerClosure(t *testing.T) {
	requiredThrough := time.Now().UTC().Add(time.Hour)
	sources := mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{
		Reference:      mediaport.GroupOpsMaterialReference{Kind: "image", ID: 7},
		SourceDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProviderFields: mediaport.GroupOpsProviderReadyAttachment{MsgType: "image"},
	}}}
	command := mediaport.GroupOpsMaterialPreparationCommand{
		SourceSnapshot:  sources,
		RequiredThrough: requiredThrough,
		Actor:           7,
		IdempotencyKey:  "deterministic-prep-0001",
		Items: []mediaport.GroupOpsMaterialPreparation{{
			Reference:     sources.References[0].Reference,
			SourceDigest:  sources.References[0].SourceDigest,
			ReceiptDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ReadyUntil:    requiredThrough.Add(time.Hour),
			Attachment:    mediaport.GroupOpsProviderReadyAttachment{MsgType: "image", MediaID: "deterministic-provider-image-7"},
		}},
	}
	store := &preparationStoreStub{}
	writer := mediaapp.NewGroupOpsMaterialPreparationWriter(preparationUOWStub{}, store)
	receipt, err := writer.RecordPreparedGroupOpsMaterials(context.Background(), command)
	if err != nil || receipt.ID < 1 || store.calls != 1 {
		t.Fatalf("receipt=%+v calls=%d err=%v", receipt, store.calls, err)
	}
	contentDigest, err := hex.DecodeString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	var digest [32]byte
	copy(digest[:], contentDigest)
	status := &groupOpsMaterialStatusStub{result: outboundport.MaterialResult{State: "ready", EffectID: "deterministic-material-effect-7", MediaID: "deterministic-provider-image-7", CredentialUsable: true, ExpiresAt: requiredThrough.Add(time.Hour)}}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{
		sources:     groupOpsMaterialSourceStub{source: outboundport.MaterialSourceSnapshot{SourceRef: "image:7", SourceType: "image", ContentDigest: digest, FileName: "image-7.png", MediaType: "image/png", SizeBytes: 7, SnapshotVersion: 1}},
		preparer:    status,
		scopeDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := freezer.FreezeGroupOpsMaterial(context.Background(), sources, requiredThrough)
	if err != nil || len(snapshot.Attachments) != 1 || snapshot.Attachments[0].MediaID != "deterministic-provider-image-7" || status.calls != 1 {
		t.Fatalf("snapshot=%+v calls=%d err=%v", snapshot, status.calls, err)
	}
}

type groupOpsMaterialSourceStub struct {
	source outboundport.MaterialSourceSnapshot
}

func (stub groupOpsMaterialSourceStub) GetSourceSnapshot(_ context.Context, reference string) (outboundport.MaterialSourceSnapshot, error) {
	if reference != stub.source.SourceRef {
		return outboundport.MaterialSourceSnapshot{}, errors.New("wrong material source")
	}
	return stub.source, nil
}

func (groupOpsMaterialSourceStub) ListEnabledSourceSnapshots(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
	return outboundport.MaterialSnapshotPage{}, errors.New("not used")
}

func (groupOpsMaterialSourceStub) ReadSourceBytes(context.Context, outboundport.MaterialSourceSnapshot) (outboundport.MaterialSourceContent, error) {
	return outboundport.MaterialSourceContent{}, errors.New("must not read bytes")
}

type groupOpsMaterialStatusStub struct {
	calls  int
	result outboundport.MaterialResult
}

func (stub *groupOpsMaterialStatusStub) GetMaterialStatus(_ context.Context, source outboundport.MaterialSourceSnapshot, scopeDigest string) (outboundport.MaterialResult, error) {
	stub.calls++
	if source.SourceRef != "image:7" || scopeDigest == "" {
		return outboundport.MaterialResult{}, errors.New("unexpected material status request")
	}
	return stub.result, nil
}

type contentDeliveryServiceStub struct {
	mediaport.ContentDeliveryService
}

type preparationReaderStub struct {
	items []mediaport.GroupOpsMaterialPreparation
	err   error
}

func (stub preparationReaderStub) ReadPreparedGroupOpsMaterials(context.Context, mediaport.GroupOpsMaterialSourceSnapshot, time.Time) ([]mediaport.GroupOpsMaterialPreparation, error) {
	return stub.items, stub.err
}

type preparationUOWStub struct{}

func (preparationUOWStub) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type preparationStoreStub struct {
	command mediaport.GroupOpsMaterialPreparationCommand
	receipt mediaport.GroupOpsMaterialPreparationReceipt
	calls   int
}

func (stub *preparationStoreStub) RecordPreparedGroupOpsMaterialsWithin(_ context.Context, command mediaport.GroupOpsMaterialPreparationCommand, _ time.Time) (mediaport.GroupOpsMaterialPreparationReceipt, error) {
	stub.calls++
	stub.command = command
	if stub.receipt.ID == 0 {
		stub.receipt = mediaport.GroupOpsMaterialPreparationReceipt{ID: 1, Actor: command.Actor, ItemCount: len(command.Items)}
	}
	return stub.receipt, nil
}

func TestGroupOpsMaterialAdapterCapturesAndFreezesThroughMediaPorts(t *testing.T) {
	var gotPlan mediaport.GroupOpsMaterialPlan
	cap := materialCapturerStub{capture: func(_ context.Context, plan mediaport.GroupOpsMaterialPlan) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
		gotPlan = plan
		return mediaport.GroupOpsMaterialSourceSnapshot{SchemaVersion: 1, References: []mediaport.GroupOpsMaterialSourceReference{{
			Reference:      mediaport.GroupOpsMaterialReference{Kind: "group_invite", ID: 10},
			SourceDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ProviderFields: mediaport.GroupOpsProviderReadyAttachment{MsgType: "link", Title: "加入体验群", URL: "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef"},
		}}}, nil
	}}
	freezer := materialFreezerStub{freeze: func(_ context.Context, source mediaport.GroupOpsMaterialSourceSnapshot, _ time.Time) (mediaport.GroupOpsMaterialSnapshot, error) {
		if len(source.References) != 1 {
			t.Fatalf("source references=%+v", source.References)
		}
		return mediaport.GroupOpsMaterialSnapshot{SchemaVersion: 2, NodeKind: "message", Attachments: []mediaport.GroupOpsProviderReadyAttachment{{MsgType: "link", Title: "加入体验群", URL: "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef"}}}, nil
	}}
	adapter := groupOpsMaterialAdapter{capturer: cap, freezer: freezer}
	raw, digest, err := adapter.ResolveMaterialSnapshot(context.Background(), groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "group_invite", ID: 10}}}, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(gotPlan.References) != 1 || gotPlan.References[0].Kind != "group_invite" || gotPlan.References[0].ID != 10 {
		t.Fatalf("media plan=%+v", gotPlan)
	}
	var snapshot mediaport.GroupOpsMaterialSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := mediaport.ValidateGroupOpsMaterialSnapshot(snapshot); err != nil {
		t.Fatalf("snapshot=%s err=%v", raw, err)
	}
	if digest == "" {
		t.Fatal("missing material snapshot digest")
	}
}

type materialCapturerStub struct {
	capture func(context.Context, mediaport.GroupOpsMaterialPlan) (mediaport.GroupOpsMaterialSourceSnapshot, error)
}

func (stub materialCapturerStub) CaptureGroupOpsMaterialSources(ctx context.Context, plan mediaport.GroupOpsMaterialPlan) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
	return stub.capture(ctx, plan)
}

type materialFreezerStub struct {
	freeze func(context.Context, mediaport.GroupOpsMaterialSourceSnapshot, time.Time) (mediaport.GroupOpsMaterialSnapshot, error)
}

func (stub materialFreezerStub) FreezeGroupOpsMaterial(ctx context.Context, source mediaport.GroupOpsMaterialSourceSnapshot, requiredThrough time.Time) (mediaport.GroupOpsMaterialSnapshot, error) {
	return stub.freeze(ctx, source, requiredThrough)
}
