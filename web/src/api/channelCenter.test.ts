import {
  buildChannelFinalUrl,
  channelAcquisitionAssetDto,
  channelAcquisitionAssetReady,
  channelAcquisitionPreviewDto,
  channelEntrantDto,
  channelPageDto,
} from './admin';

const assert = (value: unknown, message: string): void => {
  if (!value) throw new Error(message);
};

const rejects = (call: () => unknown, message: string): void => {
  try {
    call();
  } catch {
    return;
  }
  throw new Error(message);
};

export function runChannelCenterAdapterTests(): void {
  const channel = channelPageDto({
    id: 7,
    channel_name: '秋季活动',
    channel_code: 'autumn-2026',
    channel_type: 'qrcode',
    carrier_type: 'qrcode',
    status: 'active',
    assignee_count: 1,
    channel_contact_count: 3,
    created_at: '2026-09-03T00:00:00Z',
    updated_at: '2026-09-03T00:00:00Z',
  });
  assert(channel.resourceId === 7 && channel.code === 'autumn-2026' && channel.status === 'active', 'channel list projection must retain the canonical local resource');

  const entrant = channelEntrantDto({
    customer_id: 42,
    display_name: '渠道用户',
    added_at: '2026-09-03T00:00:00Z',
    last_interact_at: null,
    external_userid: 'must-not-project',
    phone: 'must-not-project',
    state: 'must-not-project',
  });
  assert(entrant.customerId === 42 && !('external_userid' in entrant) && !('phone' in entrant) && !('state' in entrant), 'entrant projection must expose canonical customer data only');

  const preview = channelAcquisitionPreviewDto({
    channel_id: 7,
    channel_code: 'autumn-2026',
    channel_name: '秋季活动',
    assignees: [],
    lifecycle: { state: 'draft', readiness_blockers: ['provider_disabled'] },
    local_only: true,
    provider_execution_eligible: false,
    real_external_call_executed: false,
  });
  assert(preview.localOnly && !preview.providerExecutionEligible && !preview.realExternalCallExecuted, 'preview must not overstate Provider readiness');
  rejects(() => channelAcquisitionPreviewDto({ ...preview, channel_id: 7, channel_code: 'autumn-2026', channel_name: '秋季活动', lifecycle: { state: 'draft', readiness_blockers: [] }, assignees: [], local_only: false, provider_execution_eligible: true, real_external_call_executed: true }), 'unsafe Provider preview was accepted');

  const pending = channelAcquisitionAssetDto({ effect_id: 'effect-7', channel_id: 7, kind: 'contact_way_qrcode', asset_version: 1, state: 'queued', download_url: '/api/admin/channels/7/qrcode/download' });
  const executed = channelAcquisitionAssetDto({ effect_id: 'effect-8', channel_id: 7, kind: 'contact_way_qrcode', asset_version: 2, state: 'executed', download_url: '/api/admin/channels/7/qrcode/download' });
  const unknown = channelAcquisitionAssetDto({ effect_id: 'effect-9', channel_id: 7, kind: 'customer_acquisition_link', asset_version: 3, state: 'outcome_unknown', asset_url: 'https://work.weixin.qq.com/ca/example' });
  assert(!channelAcquisitionAssetReady(pending) && channelAcquisitionAssetReady(executed) && !channelAcquisitionAssetReady(unknown), 'only a controlled executed asset may be used');
  const mismatchedDownload = channelAcquisitionAssetDto({ effect_id: 'effect-10', channel_id: 7, kind: 'contact_way_qrcode', asset_version: 4, state: 'executed', download_url: '/api/admin/channels/8/qrcode/download' });
  assert(!channelAcquisitionAssetReady(mismatchedDownload), 'cross-channel download URL must not become ready');

  const finalUrl = buildChannelFinalUrl('https://example.test/acquisition?source=crm', 'autumn-2026');
  assert(finalUrl === 'https://example.test/acquisition?source=crm&customer_channel=autumn-2026', 'channel URL must preserve existing parameters and encode the channel');
}
