# 管理端经营总览 API

`GET /api/admin/overview` 是管理员在授权的全局范围内读取经营汇总的只读接口。它不写入汇总表，不调用 Provider，也不创建队列或后台任务。

支付区块只统计 `payments.paid_confirmed_at` 有可信确认时间的款项，并按业务 `order_id` 去重；无法证明确认时间的历史款项以缺少证据计数保留。退款使用所有者审计追加顺序中最新已提交的 `payment.refund_settled` 记录的 `occurred_at`，历史退款仅采用导入收据的可信发生时间。客户新增通过 Identity 的来源证据桥接到 canonical customer，历史和未知来源分别排除或标记待核实。分销和待办均由 Distribution 所有者读取。

每个所有者区块独立返回 `status`、`as_of` 和 `scope`。`zero`、缺少证据、读取失败和无权限不会混同；一个区块失败不影响其他区块。日期窗口按 `Asia/Shanghai` 解释，使用半开区间。

这项能力涉及 OneID 的只读 canonical customer 桥接；不涉及持久化、内部任务或外部效果。
