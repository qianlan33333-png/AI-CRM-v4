# ADR-0007：内容雷达 UnionID、OneID 与公开访问边界

- 状态：Proposed
- 日期：2026-09-04
- 决策范围：AI-CRM-v3 内容雷达

## 背景

AI-CRM-v2 已包含内容雷达管理页面、链接生命周期、分享、事件和统计 API，但冻结基线中的访问事件没有真实外部身份，授权用户统计仍是占位。AI-CRM-v3 又要求 `customers.id` 是唯一渠道中立业务主键，外部身份只能由 Identity 领域拥有和解析。

本次迁移还要遵守 donor 前端字节冻结：不能通过修改 v2 业务文件来绕过 v3 架构。

## 两轴分类

```text
OneID: resolves identity and may explicitly provision customer
Persistence: local transaction + Provider read
External Effects: not involved
```

微信 OAuth 换取用户信息属于 Provider read。内容雷达不发送企微消息、不修改 Provider 数据，因此不进入 Outbound/External Effects。

## 决策

### 1. 身份入口只接受严格 UnionID

**雷达用户信息获取的最终可信值是经 Provider 验证且带 `wechat-open-platform:<platform-id>` scope 的 UnionID，UnionID 再由 Identity 领域与其他 ID 关联。**

- 不得在 UnionID 缺失时 fallback 到 OpenID。
- 不得把 HTTP 请求体中的 `unionid` 或 `verified=true` 当成可信事实。
- Provider Adapter 负责把 Provider 响应转换成只能由内部构造的 verified fact。
- OpenID、external_userid、手机号等关联只由 Identity 领域处理；Radar 不实现匹配优先级、不查 Identity 表、不自动合并客户根。

### 2. “返回 UnionID”只存在于服务端信任边界内

Provider Adapter 可以向身份用例返回 UnionID verified fact，但：

- 浏览器只得到用途受限的 session/cookie；
- Radar 表只保存 opaque `identity_id`、规范 `customer_id` 快照、状态和证据摘要；
- 管理 API 和 CSV 默认只返回客户安全投影，最多返回不可逆掩码；
- 原始 UnionID 由 Identity 领域独占，不进入 Radar 日志、事件或前端状态。

### 3. Resolve 与 Provision 明确分开

授权回调调用 Identity Port：

1. `Resolve(verified scoped UnionID)`；
2. 若 resolved，取得 `customers.id`；
3. 若 not found，显式调用 `ProvisionCustomerFromVerifiedIdentity`；
4. 若 pending/conflict，失败关闭，不猜测客户，不自动合并。

如果未来 UnionID 和其他 ID 指向不同 customer roots，Identity 生成可审计 merge candidate；Radar 保持原事件证据，不重写身份历史。

### 4. Provider 网络与 PostgreSQL 事务分离

OAuth code exchange 在事务外执行。Provider 成功后，消费 OAuth state、Identity Resolve/Provision、Radar session、事件、幂等收据、审计和 Outbox 必须在可验证的同一 UoW 内完成。

若现有 Identity Port 不能参与调用方事务，先扩展稳定 Port/UoW 契约，不能用两个独立事务补偿。

### 5. v3 自有 Radar 领域，供体只提供行为

- 新建 `internal/radar`，由 v3 拥有代码和表。
- 不 import、submodule 或运行时调用 v2。
- 复用 v2 的页面行为、API 叶子协议和 characterization tests，不复制其身份缺口。
- 供体业务文件继续 hash-frozen；新建 v3 Radar host Adapter 负责 API、路由、真实统计和错误映射。

### 6. 公开访问统一经过 `/r/{code}`

- 匿名内容创建匿名 view session。
- 授权内容先完成严格 UnionID/OneID 流程。
- 链接由服务端记录后重定向。
- 图片/PDF 由 v3 公共查看器和受控媒体读取提供，不泄露私有存储 URL。
- 客户端事件只能使用服务端签发的短期 token 上报白名单阶段，不能自报身份。

### 7. 指标按可证明事件计算

- PV：`landing`。
- 授权用户：distinct `identity_id`，且来源为 scoped verified UnionID。
- 查看次数：类型对应的终态事件。
- 历史 v2 点击进入独立 legacy 投影，不混入实时指标，也不推断 OneID。

## 架构关系

```mermaid
flowchart LR
    B[微信浏览器] --> R[/r/code]
    R --> RA[Radar App]
    RA --> WP[WeChat Provider Read Adapter]
    WP -->|verified scoped UnionID fact| IP[Identity Port]
    IP -->|identity_id + customers.id| RA
    RA --> RS[(Radar tables)]
    RA --> MP[Media Port]
    MP --> V[Image/PDF Viewer]
    RA --> D[Link Redirect]
    A[Admin frozen UI] --> HA[v3 Radar Host Adapter]
    HA --> RA
    IP -. owns raw identities .-> ID[(Identity tables)]
```

## 所有权与依赖

| 数据/能力 | Owner | Radar 访问方式 |
|---|---|---|
| 雷达配置、版本、session、事件 | Radar | 自有 Store |
| UnionID 与其他 ID 绑定 | Identity | `internal/identity/port` |
| 客户主数据 | Customer | Customer stable Port |
| 图片/PDF | Media | Media stable Port |
| 微信用户信息 | WeChat connector | Provider read Adapter |
| 企微写入 | Outbound | 本范围不使用 |

## 幂等与安全决策

- 管理写命令使用 `Idempotency-Key + command digest`。
- OAuth state 一次性、短 TTL、绑定 code/版本；只存摘要。
- 事件键由 `view_session + link_version + stage` 确定。
- Provider 超时、缺 UnionID、scope 不合法、Identity conflict 都不会形成“授权成功”。
- 原始外部身份、OAuth code、token、手机号、IP、完整 UA/referrer 不进入结构化日志。

## 备选方案

### 整体复制 v2

拒绝。它把无身份归因和占位统计一起复制，并造成旧仓运行时依赖风险。

### Radar 自己保存 UnionID 并做 JOIN

拒绝。它建立第二套身份系统，跨领域拥有 PII，并使 scope、冲突和合并规则分叉。

### UnionID 缺失时退回 OpenID

拒绝。OpenID 需要 App scope，且不能满足用户明确要求的跨 ID 关联入口；静默降级会造成错误归属。

### 浏览器回传 UnionID

拒绝。HTTP 客户端不能提供 verified 事实，且扩大 PII 泄露面。

## 结果与代价

正向结果：

- 内容访问可以稳定归因到 OneID，并由 Identity 统一关联其他渠道身份。
- Radar 不保存原始外部身份，减少隐私和跨域耦合。
- 供体 UI 保持可比较，v3 后端拥有长期演进空间。
- 指标从占位数字变为可追溯事实。

代价：

- 需要补齐公共 viewer、OAuth Adapter、Identity UoW 契约和前端 host Adapter。
- 如果 Provider 不返回 UnionID，访问将明确失败而不是降级继续。
- 历史 v2 点击无法自动转成已归因 OneID，需要保持 unattributed 或另行核验。

## 守护测试

- Provider 缺 UnionID、错 scope、伪造 HTTP unionid、state 重放均失败。
- 现有 UnionID Resolve 与新 UnionID Provision 路径分别有 PG16 集成测试。
- conflict/pending 不创建客户、不写错误归因。
- DB、日志、CSV、浏览器响应不含原始 UnionID。
- donor hash gate 证明冻结业务文件未变。
- Radar Store 的 SQL 只访问 Radar 表。

## 复审触发条件

- 微信 Provider 身份合同发生变化。
- Identity Port 的 scope 或 UoW 语义变化。
- 业务要求导出原始外部身份或触发自动营销动作。
- 引入需要 Provider 写入的 Radar 后续能力。

上述任一变化必须新建 ADR/PR，不能在本决策下隐式扩展。
