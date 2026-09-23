// V3-owned transaction-domain labels. Date/time conversion is shared from the
// source-owned admin helper so other Hosts do not depend on commerce naming.

export { formatShanghaiDateTime } from './adminDateTime';

export type CommerceStatusKind = 'order' | 'refund' | 'effect';

const statusLabels: Record<CommerceStatusKind, Record<string, string>> = {
  order: {
    awaiting_prepay: '待支付', awaiting_payment: '待支付', pending_payment: '待支付', unpaid: '待支付',
    paid: '已支付', refunding: '退款处理中', partially_refunded: '部分退款', refunded: '已退款',
    closed: '已关闭', cancelled: '已取消', failed: '支付失败', payment_failed: '支付失败',
  },
  refund: {
    requested: '退款申请已提交', effect_accepted: '退款申请已受理', processing: '退款处理中',
    outcome_unknown: '退款结果待核对', completed: '退款完成', retryable_failed: '退款失败，待核对',
    final_failed: '退款失败', history_requested: '历史记录：已申请', history_processing: '历史记录：处理中',
    history_failed: '历史记录：失败', history_closed: '历史记录：已关闭',
  },
  effect: {
    accepted: '已受理', queued: '等待处理', attempted: '已尝试执行', executed: '已执行',
    outcome_unknown: '结果待核对', retryable_failed: '处理失败，待核对', final_failed: '处理失败',
    reconciled: '已核对', failed: '执行失败',
    succeeded: '历史记录：外推成功',
    unknown_after_dispatch: '历史记录：已发起，结果待核对',
    simulated: '历史记录：模拟处理',
    blocked: '历史记录：已拦截，外部结果待核对',
    cancelled: '历史记录：已取消，外部结果待核对',
  },
};

function hasChineseText(value: string): boolean {
  return /[\u3400-\u9fff]/.test(value);
}

// Status words share spellings across domains but not meanings. Callers must
// choose the domain explicitly; this helper intentionally has no global enum
// fallback.
export function commerceStatusLabel(kind: CommerceStatusKind, raw: unknown): string {
  const value = typeof raw === 'string' ? raw.trim() : '';
  if (!value) return kind === 'refund' ? '退款状态待核对' : kind === 'effect' ? '处理状态待核对' : '订单状态待核对';
  return statusLabels[kind][value] || (hasChineseText(value)
    ? value
    : kind === 'refund' ? '退款状态待核对' : kind === 'effect' ? '处理状态待核对' : '订单状态待核对');
}

export function commerceProviderLabel(raw: unknown): string {
  switch (typeof raw === 'string' ? raw.trim() : '') {
    case 'wechat':
    case 'wechat_pay': return '微信支付';
    case 'wechat_shop': return '微信小店';
    case 'alipay': return '支付宝';
    default: return '支付来源待确认';
  }
}
