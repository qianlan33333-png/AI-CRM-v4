package outbound

import (
	"context"
	"errors"
	"testing"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type customerTagDispatchStub struct {
	value customerport.TagCommandDispatch
}

func (s customerTagDispatchStub) ReadTagCommandDispatch(context.Context, string) (customerport.TagCommandDispatch, error) {
	return s.value, nil
}

type customerTagContactStub struct {
	value wecomport.CurrentExternalContact
	err   error
}

func (s customerTagContactStub) CurrentExternalContact(context.Context, customerdomain.CustomerID, int64) (wecomport.CurrentExternalContact, error) {
	return s.value, s.err
}

type customerTagBindingStub struct{ values map[int64]string }

func (s customerTagBindingStub) ProviderTagID(_ context.Context, id int64) (string, bool, error) {
	v, ok := s.values[id]
	return v, ok, nil
}

type customerTagWriterStub struct {
	calls              int
	employee, external string
	add, remove        []string
	err                error
}

func (s *customerTagWriterStub) MarkContactTags(_ context.Context, employee, external string, add, remove []string) error {
	s.calls++
	s.employee, s.external = employee, external
	s.add = append([]string(nil), add...)
	s.remove = append([]string(nil), remove...)
	return s.err
}

type customerTagCrossedError struct{}

func (customerTagCrossedError) Error() string               { return "crossed provider boundary" }
func (customerTagCrossedError) ProviderCallAttempted() bool { return true }

func customerTagEnvelope(d customerport.TagCommandDispatch) effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCustomerTagCommand,
		SourceRefDigest: effectport.Hash("source"), TargetRefDigest: effectport.Digest(d.TargetDigest),
		PayloadDigest: effectport.Hash("customer.tag.command.payload.v1", joinIDs64(d.AddTagIDs), joinIDs64(d.RemoveTagIDs), d.BindingDigest), PolicyVersionHash: effectport.Hash("customer.tag.command.policy.v1")}
}
func customerTagDispatch() customerport.TagCommandDispatch {
	add, remove := []string{"provider-a", "provider-b"}, []string{"provider-c"}
	return customerport.TagCommandDispatch{EffectRef: "eer_7", CustomerID: 42, StaffID: 9, AddTagIDs: []int64{1, 2}, RemoveTagIDs: []int64{3},
		TargetDigest: string(effectport.Hash("customer.tag.command.target.v1", "staff-9", "external-42")), BindingDigest: string(effectport.Hash("customer.tag.command.binding.v1", joinProviderIDs(add), joinProviderIDs(remove)))}
}
func customerTagProviderFixture(t *testing.T, enabled bool, d customerport.TagCommandDispatch, contact wecomport.CurrentExternalContact, w *customerTagWriterStub) *CustomerTagProvider {
	t.Helper()
	p, err := NewCustomerTagProvider(CustomerTagProviderConfig{GenericEnabled: enabled, ChannelEntryTagEnabled: enabled}, customerTagDispatchStub{d}, customerTagContactStub{value: contact}, customerTagBindingStub{values: map[int64]string{1: "provider-a", 2: "provider-b", 3: "provider-c"}}, w)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestCustomerTagProviderOneTrustedMultiTagCall(t *testing.T) {
	d := customerTagDispatch()
	w := &customerTagWriterStub{}
	result, err := customerTagProviderFixture(t, true, d, wecomport.CurrentExternalContact{EmployeeUserID: "staff-9", ExternalUserID: "external-42"}, w).Execute(context.Background(), customerTagEnvelope(d), effectport.Attempt{EffectID: "eer_7", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || w.calls != 1 || w.employee != "staff-9" || w.external != "external-42" || len(w.add) != 2 || len(w.remove) != 1 || !result.RealExternalCallExecuted {
		t.Fatalf("result=%+v err=%v writer=%+v", result, err, w)
	}
}
func TestCustomerTagProviderRefusesFrozenTargetOrBindingDriftWithoutCall(t *testing.T) {
	d := customerTagDispatch()
	w := &customerTagWriterStub{}
	p := customerTagProviderFixture(t, true, d, wecomport.CurrentExternalContact{EmployeeUserID: "other-staff", ExternalUserID: "external-42"}, w)
	result, err := p.Execute(context.Background(), customerTagEnvelope(d), effectport.Attempt{EffectID: "eer_7", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || w.calls != 0 {
		t.Fatalf("target result=%+v err=%v calls=%d", result, err, w.calls)
	}
	p = customerTagProviderFixture(t, true, d, wecomport.CurrentExternalContact{EmployeeUserID: "staff-9", ExternalUserID: "external-42"}, w)
	p.tags = customerTagBindingStub{values: map[int64]string{1: "changed", 2: "provider-b", 3: "provider-c"}}
	result, err = p.Execute(context.Background(), customerTagEnvelope(d), effectport.Attempt{EffectID: "eer_7", Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || w.calls != 0 {
		t.Fatalf("binding result=%+v err=%v calls=%d", result, err, w.calls)
	}
}
func TestCustomerTagProviderPostCallFailureIsUnknown(t *testing.T) {
	d := customerTagDispatch()
	w := &customerTagWriterStub{err: customerTagCrossedError{}}
	result, err := customerTagProviderFixture(t, true, d, wecomport.CurrentExternalContact{EmployeeUserID: "staff-9", ExternalUserID: "external-42"}, w).Execute(context.Background(), customerTagEnvelope(d), effectport.Attempt{EffectID: "eer_7", Number: 1, Generation: 1, Fence: 1})
	if !errors.Is(err, w.err) || result.Completion != effectport.StateUnknown || !result.CallAttempted || !result.RealExternalCallExecuted || w.calls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, w.calls)
	}
}

type customerTagCompletionWriterStub struct {
	values []customerport.TagCommandCompletion
	err    error
}

func (s *customerTagCompletionWriterStub) CompleteTagCommand(_ context.Context, value customerport.TagCommandCompletion) error {
	s.values = append(s.values, value)
	return s.err
}

type customerTagChannelCompletionStub struct {
	calls     int
	effectRef string
	state     string
}

func (s *customerTagChannelCompletionStub) CompleteEntrantAction(_ context.Context, value channelport.EntrantActionCompletion) error {
	s.calls++
	s.effectRef, s.state = value.EffectRef, value.State
	return nil
}

func TestCustomerTagCompletionSinkUsesAttemptFenceAndOnlyUpdatesChannelSource(t *testing.T) {
	writer := &customerTagCompletionWriterStub{}
	channel := &customerTagChannelCompletionStub{}
	reader := customerTagDispatchStub{value: customerport.TagCommandDispatch{Source: "admin_customer_ui"}}
	sink, err := NewCustomerTagCompletionSink(writer, reader, channel)
	if err != nil {
		t.Fatal(err)
	}
	envelope := effectport.Envelope{Kind: effectport.KindCustomerTagCommand, SourceRefDigest: effectport.Hash("source")}
	if err = sink.CompleteEffect(context.Background(), "eer_19", envelope, effectport.Attempt{Number: 2, Generation: 3, Fence: 4}, effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("done")}); err != nil {
		t.Fatal(err)
	}
	if len(writer.values) != 1 || writer.values[0].Attempt != 2 || writer.values[0].Generation != 3 || writer.values[0].Fence != 4 || channel.calls != 0 {
		t.Fatalf("writer=%+v channel=%+v", writer.values, channel)
	}
	reader.value.Source = "channel_entry_tag"
	sink.reader = reader
	if err = sink.CompleteEffect(context.Background(), "eer_20", envelope, effectport.Attempt{EffectID: "eer_7", Number: 1, Generation: 1, Fence: 1}, effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("rejected")}); err != nil {
		t.Fatal(err)
	}
	if channel.calls != 1 || channel.effectRef != "eer_20" || channel.state != "final_failed" {
		t.Fatalf("channel=%+v", channel)
	}
}

func TestCustomerTagProviderDefiniteRejectionProjectsSafeFinalReason(t *testing.T) {
	d := customerTagDispatch()
	w := &customerTagWriterStub{err: wecomport.WrapProviderWriteDisposition(errors.New("provider rejected"), true, false, false)}
	result, err := customerTagProviderFixture(t, true, d, wecomport.CurrentExternalContact{EmployeeUserID: "staff-9", ExternalUserID: "external-42"}, w).Execute(context.Background(), customerTagEnvelope(d), effectport.Attempt{EffectID: "eer_7", Number: 1, Generation: 1, Fence: 1})
	if !errors.Is(err, w.err) || result.Completion != effectport.StateFinalFailed || !result.CallAttempted || !result.Artifact.Valid() || string(result.Artifact.Payload) != "provider_rejected" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	writer := &customerTagCompletionWriterStub{}
	sink, sinkErr := NewCustomerTagCompletionSink(writer, customerTagDispatchStub{value: d})
	if sinkErr != nil {
		t.Fatal(sinkErr)
	}
	if sinkErr = sink.CompleteEffect(context.Background(), "eer_7", customerTagEnvelope(d), effectport.Attempt{EffectID: "eer_7", Number: 1, Generation: 1, Fence: 1}, result); sinkErr != nil {
		t.Fatal(sinkErr)
	}
	if len(writer.values) != 1 || writer.values[0].State != "final_failed" || writer.values[0].ResultReason != "provider_rejected" {
		t.Fatalf("completion=%+v", writer.values)
	}
}

type customerTagObservationStub struct {
	calls int
	err   error
}

func (stub *customerTagObservationStub) RefreshCustomerTagObservation(context.Context, string, customerdomain.CustomerID, string, string) error {
	stub.calls++
	return stub.err
}

func TestCustomerTagProviderKeepsExecutedWhenObservationReadFails(t *testing.T) {
	d := customerTagDispatch()
	writer := &customerTagWriterStub{}
	observer := &customerTagObservationStub{err: errors.New("readback unavailable")}
	provider, err := NewCustomerTagProvider(CustomerTagProviderConfig{GenericEnabled: true}, customerTagDispatchStub{d}, customerTagContactStub{value: wecomport.CurrentExternalContact{EmployeeUserID: "staff-9", ExternalUserID: "external-42"}}, customerTagBindingStub{values: map[int64]string{1: "provider-a", 2: "provider-b", 3: "provider-c"}}, writer, observer)
	if err != nil {
		t.Fatal(err)
	}
	result, executeErr := provider.Execute(context.Background(), customerTagEnvelope(d), effectport.Attempt{EffectID: "eer_7", Number: 1, Generation: 1, Fence: 1})
	if executeErr != nil || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted || writer.calls != 1 || observer.calls != 1 {
		t.Fatalf("result=%+v executeErr=%v writes=%d observations=%d", result, executeErr, writer.calls, observer.calls)
	}
}
