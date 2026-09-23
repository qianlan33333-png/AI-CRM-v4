# 历史退款状态契约

OneID：复用既有迁移的 canonical payer/beneficiary，保持完整身份范围检查，不新增匹配规则。
Persistence：Payment owner 本地历史导入事务；原批次摘要/幂等收据/审计保持不变。不发送 Provider，不新建任务或退款效果。

历史 RefundRow 增加可选 status，缺省字段仍表示旧版成功事实，omitempty 保持旧 JSON 摘要。显式成功 SUCCESS/success/completed 映射 completed；failed、closed/CLOSED、PROCESSING/processing、requested 分别映射 history_failed、history_closed、history_processing、history_requested。不支持的状态失败关闭，必须核实后扩展，不能当成功。

仅 completed 计入订单 refunded_minor 和成功退款金额。历史处理中/已申请仍占退款风险敞口；历史失败/关闭不占。历史状态禁止挂接 external_effect_id，不能经过活跃退款 BindEffect/Complete 状态转换，不具备重试授权。Provider 回调迁移与旧订单状态增量是单独工作；本改动不接管旧回调、不覆盖既有订单摘要。

必须先应用 0127，再在隔离备份库演练完整订单+退款 Manifest。原 783 条已导入订单若源摘要变化仍严格冲突，不能用新 source_key 重复导入。禁止 order-only 伪装完整订单退款切流。
