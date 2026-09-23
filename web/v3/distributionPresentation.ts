// Shared, fact-preserving presentation labels for every V3 distribution view.
// "已分账" means the platform recorded a successful split. It must not be
// presented as a bank-account arrival time.

function valueOf(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

// Reasons originate in Distribution's frozen fact records. Known protocol
// values become staff-readable Chinese; an unfamiliar ASCII code remains a
// pending fact instead of appearing as a successful or raw-engineering state.
export function distributionReasonLabel(value: unknown): string {
  const raw = valueOf(value);
  const labels: Record<string, string> = {
    buyer_refund: '买家退款', buyer_refund_pending: '退款复核中', buyer_refund_final_failed: '退款最终未完成',
    payment_or_refund_pending: '支付或退款待确认', qualification_refund_evidence_pending: '资格退款凭证待确认',
    qualification_refund_final_failed: '资格退款最终未完成', qualification_revoked: '推广资格已撤销',
    qualification_revoked_after_paid: '资格变化后已分账', settlement_outcome_unknown: '分账结果待核验',
    split_deadline_within_24h: '分账时限临近', buyer_refund_after_paid: '退款后已分账',
    payment_receiver_receipt_limit: '收款方收据额度受限', receiver_account_abnormal: '收款账户异常',
    identity_lineage_conflict: '身份归属存在冲突', no_valid_purchase: '未找到有效购买凭证',
    payment_confirmation_missing: '支付确认缺失', payment_evidence_unavailable: '支付凭证暂不可读取',
    refund_outcome_pending: '退款结果待确认',
  };
  if (labels[raw]) return labels[raw];
  if (!raw) return '未说明';
  // A reason is safe to display verbatim only when it is already staff-facing
  // Chinese text. Protocol codes and English diagnostics must not leak from a
  // read model into the administration UI.
  return /[\u3400-\u9fff]/.test(raw) ? raw : '原因待确认';
}

export function distributionCommissionStatusLabel(value: unknown): string {
  const labels: Record<string, string> = {
    pending: '待结算', held: '暂缓结算', settling: '分账处理中', paid: '已分账',
    cancelled: '已取消', exception: '异常待处理', zero_commission: '零佣金成交',
  };
  return labels[valueOf(value)] || '状态待确认';
}

export function distributionAdjustmentLabel(value: unknown): string {
  const labels: Record<string, string> = {
    buyer_refund: '买家退款调整', qualification_hold: '资格暂缓',
    qualification_revoke: '资格撤销', qualification_restore: '资格恢复',
    manual_recovery: '人工追回登记', merchant_liability: '商户承担登记',
  };
  return labels[valueOf(value)] || '调整待确认';
}

export function distributionSettlementStatusLabel(value: unknown): string {
  const labels: Record<string, string> = {
    planned: '待提交', accepted: '已受理', attempted: '处理中',
    outcome_unknown: '结果待核验', receiver_succeeded: '分账成功',
    cancelled: '已取消', exception: '异常待处理',
  };
  return labels[valueOf(value)] || '状态待确认';
}

export function distributionExceptionLabel(value: unknown): string {
  const labels: Record<string, string> = {
    settlement_unknown: '结算结果待核验', settlement_not_paid: '分账未完成',
    settlement_deadline: '结算时限异常', settlement_deadline_imminent: '结算时限提醒',
    receiver_unavailable: '收款准备未完成', qualification_revoked_after_paid: '资格变化后已分账',
    buyer_refund_after_paid: '退款后已分账', unfreeze_final_failed: '解冻失败',
    merchant_liability: '商户承担', recovery: '追回登记',
  };
  return labels[valueOf(value)] || '异常待确认';
}
