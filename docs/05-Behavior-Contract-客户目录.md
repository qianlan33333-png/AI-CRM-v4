# Behavior Contract：客户激活与目录

本契约冻结 `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e` 中本板块可复用的对外行为，不承诺兼容其内部包结构、React 页面、migration 或历史数据模型。

## 冻结行为

- 列表默认 50，最大 200。
- 排序固定为 `updated_at DESC, customer_id DESC`。
- 首页生成固定 watermark，后续 cursor 必须绑定同一 watermark、排序键和筛选指纹。
- cursor 不可解析、版本错误、字段越界、筛选指纹不一致均返回 400，不降级到 offset。
- 页面与 HTTP 只接受不带 `+86` 的 11 位中国大陆手机号；Identity 使用 `phone:cn11` HMAC 摘要精确解析 Customer ID，Customer 查询不接触 raw phone。
- 精确总数上限 10,000；超过时 `total_is_estimate=true`。
- 企微按成员和 Provider cursor 分页；必须持久化当页全部业务事实后才前进 cursor。
- 同一 scoped external_userid 重放不重复建客。

## v3 有意差异

- UI 使用 Go Template + 原生 JS/CSS，不携带 v2 React 构建链。
- 同步进度与收据是 v3 新的强契约；队列或 HTTP 200 不是完成。
- 全量 stale 仅在整轮成功且投影对账后生效。
- 手机号只能作为 declared identity 附着到已有根，不参与建客和自动合并。
- 列表和详情只把手机号展示为普通本地号码，不展示 `+86` 或 assurance；头像和内部激活状态不属于页面字段。

## Characterization / Journey 清单

1. 首页和第二页之间插入新数据，已建 watermark 下不重复、不跳行。
2. 改变任一筛选条件后复用旧 cursor，返回 400。
3. 超过 10,000 条时返回估算标记。
4. Provider 页拉取成功但数据库事务失败，cursor 不前进。
5. 在中间成员/中间 cursor 崩溃，恢复后从已提交位置继续。
6. 重复 external_userid 只保留一个 active identity 和 Customer。
7. 部分失败轮次不标记 stale。
8. Viewer 能看脱敏值但不能查询完整号码；Admin/SuperAdmin 缺 CSRF 不能查询，成功查询写入固定 purpose 的不可变审计。
9. declared phone 已被其他 Customer 占用时得到 conflict，不转移归属。
10. 导入结果桶之和与输入数不等时 reconcile 失败。
