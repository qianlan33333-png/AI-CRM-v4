# PRD：企微侧边栏与精简 Customer 360

状态：Approved for production delivery（2026-09-04）

冻结供体：`AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`

目标仓库：AI-CRM-v3（Go 模块化单体）

## 1. 产品目标

把供体的企微客户侧边栏视觉壳迁入 v3，并只接入本期批准的真实能力；同时把现有数字 `customer_id` 详情页扩展为精简 Customer 360。模块以 OneID canonical `customers.id` 为唯一客户根，不建设第二套身份或客户归属逻辑。

侧边栏只保留六个一级页签：

1. 核心画像：基础资料、declared 手机号声明和安全时间线；
2. 问卷：immutable submission/version snapshot；
3. 商品：已发布普通商品和周期商品；
4. 订单：普通订单、周期权益及周期备注；
5. 优惠券：Customer 归属的本地券领取投影；
6. 素材：已启用图片素材和雷达链接。

Customer 360 只保留安全身份摘要、基础档案、订单统计、问卷统计、风险和最近触点。

## 2. 明确排除

本模块不建设、不读取、不显示且不新增同义 Port：

- 聊天记录、其他客服聊天或消息统计；
- 运营摘要、运营状态、自动化摘要或自动化状态；
- 客服与客户的跟进关系、负责人或 owners；
- CRM 标签、`CustomerTagReader`、标签 API、标签缓存或数据库投影。

企微标签继续由企微客户端原生展示。已有全局企微 callback、关系表以及被其他模块使用的旧路由不删除；本模块仅解除依赖。

## 3. 身份、授权与隐私

侧边栏上下文只使用三元组：系统配置和既有企微会话中的 `corp_id`、现有 OAuth Session 中的 `employee_id`、当前企微 JSSDK `getCurExternalContact` 返回的 `external_userid`。

服务端从 Session/配置取得企业和员工，HTTP 请求体只能提交当前 `external_userid`。Identity 使用 `wecom-corp:<corp_id>` scope 调用 OneID `Resolve`；只解析既有 canonical Customer，不隐式建客、不自动合并、不升级 assurance，也不查询客服—客户跟进关系。

Context Token 只绑定 `corp_id + employee_id + customer_id + exp`。Token 建立后页面及业务 API 不再携带 raw `external_userid`。手机号仅作为 declared identity claim；页面只展示脱敏值。

## 4. 用户旅程

### 4.1 企微侧边栏

1. 已认证员工在企微客户会话打开供体视觉壳；
2. Host Adapter 复用现有 JSSDK 配置并读取当前 `external_userid`；
3. 服务端根据三元组和 OneID 签发短期 Context Token；
4. 前端读取 workbench 和六个页签的真实本地投影；
5. 写画像、手机号声明和周期备注时使用 CAS、幂等键和原子 receipt/audit/outbox；
6. 发送商品、图片或雷达链接时，服务端先冻结内容并接受 Outbound External Effect，再由浏览器凭一次性 grant 调用现有 `sendChatMessage`，最后回写 client outcome；
7. 图片必须来自 Media 已持久化且未过期的真实企微 `media_id` 准备凭据；不存在时返回 `503 capability_not_ready`，不制造假 ID 或后台受保护 URL。

### 4.2 Customer 360

1. 从 `/admin/customers` 以数字 Customer ID 打开 `/admin/customers/{customer_id}`；
2. 页面请求 `/api/admin/customers/{customer_id}/360`；
3. 服务端先解析 canonical Customer，再分区读取本地身份、档案、订单、问卷、风险和触点；
4. 单个分区失败只把该分区标记 degraded，不遮挡其余数据；
5. 页面读取不触发企微或其他 Provider 调用。

## 5. HTTP 契约

侧边栏：

- `POST /api/sidebar/context-token`
- `GET /api/sidebar/v2/workbench`
- `GET|PUT /api/sidebar/v2/profile`
- `POST /api/sidebar/v2/phone-binding`
- `GET /api/sidebar/v2/questionnaires`
- `GET /api/sidebar/v2/timeline`
- `GET /api/sidebar/v2/products`
- `GET /api/sidebar/v2/orders`
- `GET /api/sidebar/v2/periodic-orders`
- `PUT /api/sidebar/v2/periodic-orders/{entitlement_id}/remark`
- `GET /api/sidebar/v2/coupons`
- `GET /api/sidebar/v2/materials`
- `GET /api/sidebar/v2/radar-links`
- `POST /api/sidebar/v2/send-intents`
- `POST /api/sidebar/v2/send-intents/{intent_id}/outcome`

后台：`GET /api/admin/customers/{customer_id}/360`。

本模块不实现或不消费 chat、other-staff-messages、owners、tags、运营摘要和自动化摘要接口。未准备好的真实能力返回 `503 capability_not_ready`；运行故障返回 `503 section_unavailable`；二者不得混淆。

## 6. 领域与持久化

```text
OneID: resolves scoped WeCom external identity to canonical customers.id; no follow-relationship coordination
Persistence: local transactions + existing Provider-read infrastructure + outbound-owned Provider writes
```

Sidebar 只依赖 Customer、Identity、Survey、Product、Order、Coupon、Media、Radar、Outbound 的稳定 Port。业务状态、幂等收据、审计、Outbox 与 External Effect 接受在同一个 PostgreSQL UoW 提交。Provider 网络调用不得持有事务；只有 Outbound 可以发起企微业务写。浏览器效果区分 accepted、queued、client_executed、outcome_unknown、final_failed 和 reconciled，过期 grant 由 River 持久任务关闭。

风险摘要仅使用本项目保留领域的本地事实，禁止间接重新引入聊天、标签、运营、自动化或跟进关系。

## 7. 历史数据迁移

历史周期权益和优惠券从供体 PostgreSQL 的 repeatable-read/read-only 快照读取。每行 UnionID 以明确开放平台 scope 通过 OneID `Resolve`；not_found、conflict 或定义未映射写入只含 digest 的 quarantine，不隐式建客。

周期权益由 Order Owner 持久化，优惠券领取由 Coupon Owner 持久化，`source_system + source_key + source_digest` 保证重放安全。生产执行依次完成 inspect-stream、dry-run、目标库只读 preflight、v3 PostgreSQL 备份、apply、replay 和 reconcile。preflight 必须证明全部来源行已有唯一 OneID 和定义映射；任何阻塞行都停止写入并报告分类计数。最终输入数必须等于 imported + replayed，映射数必须等于输入数且 quarantine 为零。

## 8. 供体冻结与批准差异

供体 HTML/CSS/JS/素材由 `docs/migration/sidebar-customer360/donor-manifest.yaml` 固定 SHA，禁止修改供体文件。活动 Host Adapter 保留供体布局、视觉语言和交互壳，只做以下批准差异：删除聊天、标签、跟进、运营和自动化入口；只显示六个页签；以 v3 OAuth/JSSDK/Context Token 和稳定 Port 替换供体旧网络控制器。

生产只能有一个侧边栏页面 owner 和一个活动控制器，不部署 Webshell/TypeScript 双控制器。

## 9. 验收标准

- 无 `wecom_follow_relationships` 行时合法三元组仍能签发和使用 Context Token；删除或停用关系不使已签 token 失效；
- 无效 Session、错误企业、Identity not_found/conflict、token 篡改或过期必须拒绝；
- DOM 只出现六个一级页签，无聊天、标签、跟进、运营或自动化入口；
- Customer 360 schema/DOM 无 `message_summary`、`tags`、`owners`、`user_ops_status`、`automation_status`；
- 普通/周期商品只返回已发布可分享版本；订单、问卷、优惠券均按 canonical Customer 隔离；
- 图片发送没有真实未过期 `media_id` 时失败关闭；任何 send outcome 都有 intent/effect/audit/outbox/任务或 client receipt；
- 全局 callback/relationship 回归继续通过，证明其他模块能力未删除；
- `make check`、race、OpenAPI、前端构建、供体冻结检查和 PostgreSQL 16 migration 通过；
- 生产 release SHA 等于 main SHA，`/readyz` 为 200，历史迁移守恒对账完成；
- 真实企微 smoke 必须分别验证上下文、六页签读取、一次 JSSDK 发送调用及 outcome receipt。未取得 Provider 证明时只能标记 unverified，不宣称送达。
