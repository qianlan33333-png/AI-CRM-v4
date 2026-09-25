package outbound

import (
	"context"
	"errors"
	"testing"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type channelAssetReaderStub struct{}

func (channelAssetReaderStub) ReadPublishedConfig(context.Context, string) (channelport.PublishedConfig, error) {
	return channelport.PublishedConfig{Kind: "contact_way_qrcode", Operation: "create", ChannelName: "test channel", StaffProviderRefs: []string{"staff-fixture"}}, nil
}

type channelAssetWriterStub struct {
	err   error
	calls int
}

func (writer *channelAssetWriterStub) CreateContactWay(context.Context, wecomport.AcquisitionAssetRequest) (wecomport.AcquisitionAssetResult, error) {
	writer.calls++
	return wecomport.AcquisitionAssetResult{}, writer.err
}
func (*channelAssetWriterStub) GetContactWay(context.Context, string) (wecomport.AcquisitionAssetResult, error) {
	return wecomport.AcquisitionAssetResult{}, nil
}
func (*channelAssetWriterStub) UpdateContactWay(context.Context, string, wecomport.AcquisitionAssetRequest) (wecomport.AcquisitionAssetResult, error) {
	return wecomport.AcquisitionAssetResult{}, nil
}
func (*channelAssetWriterStub) DeleteContactWay(context.Context, string) error { return nil }
func (*channelAssetWriterStub) CreateCustomerAcquisitionLink(context.Context, wecomport.AcquisitionAssetRequest) (wecomport.AcquisitionAssetResult, error) {
	return wecomport.AcquisitionAssetResult{}, nil
}

func channelAssetTestEnvelope() effectport.Envelope {
	return effectport.Envelope{
		Owner: effectport.OwnerOutbound, Kind: effectport.KindChannelAsset,
		SourceRefDigest: effectport.Hash("source"), TargetRefDigest: effectport.Hash("target"),
		PayloadDigest: effectport.Hash("payload"), PolicyVersionHash: effectport.Hash("policy"),
	}
}

func TestChannelAssetProviderDistinguishesDefiniteRejectionFromUnknown(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		state  effectport.State
		code   string
		hasErr bool
	}{
		{"definite WeCom rejection", wecomport.WrapProviderWriteDispositionWithCode(errors.New("provider rejected"), true, false, false, 48002), effectport.StateFinalFailed, "wecom_errcode_48002", false},
		{"ambiguous post-call failure", wecomport.WrapProviderWriteError(errors.New("transport closed"), true), effectport.StateUnknown, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &channelAssetWriterStub{err: test.err}
			result, err := NewChannelAssetProvider(channelAssetReaderStub{}, writer).Execute(context.Background(), channelAssetTestEnvelope(), effectport.Attempt{Number: 1})
			if (err != nil) != test.hasErr || result.Completion != test.state || result.FailureCode != test.code || !result.CallAttempted || writer.calls != 1 {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, writer.calls)
			}
		})
	}
}
