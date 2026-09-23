# PRD：HXC 漏斗 OneID 双键匹配与身份持久化

> 状态：Approved for implementation
> 日期：2026-09-05
> 目标页面：`/admin/hxc-dashboard`、`/admin/oneid`

## 1. 背景

当前 HXC 看板只使用带开放平台 scope 的 UnionID 查询 OneID。HXC MySQL 虽然含有手机号，但刷新快照没有把手机号带入 OneID；未匹配身份也不会持久化，因此生产投影出现全量未匹配时，系统无法利用手机号补充匹配，也无法在未来身份补齐后自动重试。

OneID 的渠道中立主键是 `customers.id`。UnionID、手机号、企微外部联系人 ID 等均是挂在 Customer 根下的多行身份，不在 `customers` 上增加宽字段。

## 2. 产品目标

1. HXC UnionID 与手机号并行精确匹配，任一身份唯一命中即可归属 OneID。
2. 两个身份指向不同 Customer、身份本身不唯一或 HXC 来源重复时进入冲突，禁止自动选边。
3. 对现有 HXC 用户执行一次全量 inspect 和全量 apply；未来刷新继续处理新增或变化用户。
4. HXC UnionID 与手机号都有 Identity-owned 持久化落点；未匹配不再丢弃。
5. 冲突在 OneID 管理页形成可查看、可忽略、可人工确认归并的闭环。

## 3. 明确不做

- 双键都未命中时不创建 Customer，即使 UnionID 的来源和 scope 已验证。
- 手机号不提升为 verified，不用手机号单独建客。
- 不自动合并两个 Customer 根。
- 不改变 HXC 会员、使用行为和三段漏斗分类。
- 不把原始手机号、UnionID、HXC 用户 ID 暴露到浏览器、日志、Job 参数或 HXC 投影。
- 不产生任何 Provider 写入或 External Effect。

## 4. 身份模型

```text
customers.id
  ├─ unionid               @ wechat-open-platform:<id>
  ├─ phone                 @ phone:cn11
  ├─ wecom_external_userid @ wecom-corp:<id>
  └─ 其他 scoped identity
```

HXC 原始身份只在 Provider Adapter 与 Identity Port 的受控内存边界中出现。已匹配 UnionID 使用现有 `customer_identities`；手机号使用确定性 HMAC 查询摘要与 Phone Vault 密文。未匹配值写入 Identity 领域的加密来源观察，不写入 HXC 表。

## 5. 匹配规则

| UnionID | 手机号 | 结果 | 动作 |
|---|---|---|---|
| 唯一命中 A | 未命中/缺失 | matched by UnionID | 归属 A；手机号有效且未占用时以 declared 身份挂到 A |
| 未命中/缺失 | 唯一命中 A | matched by phone | 归属 A；verified UnionID 未占用时挂到 A |
| 唯一命中 A | 唯一命中 A | matched by both | 归属 A并刷新观察 |
| 唯一命中 A | 唯一命中 B | conflict | 创建来源冲突及人工 merge candidate，禁止自动选边 |
| 任一身份多根 | 任意 | conflict | 禁止使用另一身份降级选边 |
| 两者都未命中 | 任意 | unmatched/pending | 加密持久化观察，不建客 |
| 非法值或缺 scope | 任意 | unmatched/invalid | 保存安全结果码，不参与归属 |

补充规则：

- UnionID 必须使用准确的 `wechat-open-platform:<id>` scope。
- 手机号只接受 11 位中国大陆号码，规范化 scope 为 `phone:cn11`。
- 相同 UnionID/手机号出现在多个有效 HXC 用户上时，相关主体均进入来源冲突。
- 多个 HXC 主体落到同一 canonical Customer 时，保持当前一对一约束并进入冲突。
- Customer merge lineage 必须解析到最终非 merged 根后再比较。

## 6. 持久化与幂等

Identity 领域保存来源主体、加密身份观察、不可变解析收据和来源冲突。身份值变化时创建新观察并退休旧观察，不覆盖历史；连续两个完整成功快照均未出现的来源主体才标记 retired。

每个 HXC 主体使用以下幂等范围：

```text
hxc + subject_digest + rule_version + source_payload_digest
```

同键同载荷重放返回原结果；同键载荷漂移失败并进入冲突。身份绑定、观察、收据、冲突和审计在一个 PostgreSQL Unit of Work 内提交。

## 7. 全量与增量

- `AICRM_HXC_IDENTITY_WRITE_ENABLED=false`：完整读取 HXC 和 OneID，只生成聚合 inspect 结果，不改变身份。
- 开关开启：逐主体 apply，完成后发布 `hxc-current-v2` 不可变投影。
- 全量 apply 后以相同快照和规则重放，新增 Customer 必须为 0，新增身份/观察/冲突/收据必须为 0。
- 未来继续复用现有 HXC 定时器和 River `hxc-dashboard` 队列，不引入新 Worker 框架。

## 8. 看板与冲突处理

HXC 看板保留 matched/unmatched/conflict，总览增加 matched by UnionID、phone、both、pending observation 与 invalid identity。明细增加匹配来源、安全原因码及可空冲突 case ID。

OneID 管理页展示安全 HXC 引用、两侧 Customer、脱敏手机号、scope、证据摘要、候选 ID 与状态。普通管理员/员工只读；SuperAdmin 可幂等忽略冲突或确认现有 merge candidate 的 survivor。来源修正或人工合并后，下一次 HXC 刷新自动复核并关闭 case。

## 9. 验收

- 当前 HXC 全量用户 100% 进入且只进入一个结果桶。
- 任一唯一身份命中时匹配正确；双键异根和任一多根时 0 自动选边。
- 双键均未命中时新建 Customer 数为 0，两个身份均有加密观察。
- 全量 apply 可中断恢复，同快照重放零新增。
- API、日志、Job、CSV、浏览器存储和 HXC 投影中无原始手机号或 UnionID。
- 生产 release SHA、迁移、Timer、Worker、页面统计和下一次增量刷新全部验证通过。

## 10. 架构分类

```text
OneID：涉及；解析、挂接、冲突及人工归并，唯一根为 customers.id；不自动建客或合并。
Persistence：Identity 本地事务 + HXC 不可变投影 + River 内部持久任务。
Provider read：只读 HXC MySQL，在 PostgreSQL 事务外完成。
Provider write / External Effects：不涉及。
```
