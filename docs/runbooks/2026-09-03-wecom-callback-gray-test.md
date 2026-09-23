# 企微外部联系人回调灰度验证与回滚手册

## 1. 当前状态与边界

- v3 回调入口：`https://id-dev.youcangogogo.com/wecom/external-contact/callback`
- 正式企微回调地址在本次开发上线后仍保持旧地址，不自动切换。
- 本手册只验证外部联系人回调、OneID、跟进关系和已有 State 绑定的渠道归因。
- 不做企微全量同步、外部联系人主动拉取、手机号补齐、历史客户导入或客户列表功能验收。
- Token、EncodingAESKey、State 原值、external_userid、WelcomeCode 和原始 XML 不进入测试记录。

企微管理后台的外部联系人回调地址是企业级配置，无法天然按用户百分比灰度。因此本期的“灰度”是：先完成离线和合成验证，再在约定维护窗口短时切换，使用专用员工、专用外部联系人和专用渠道码验证，满足门禁后再扩大观察。

## 2. 切换前准备

负责人需要准备并记录以下非敏感信息：

- 维护窗口、操作人和回滚操作人。
- 旧回调 URL 和 v3 回调 URL；不在文档中记录 Token/AESKey。
- 一个已绑定 CRM 员工账号的企微测试员工。
- 第二个企微测试员工，用于验证同一 OneID 的多跟进关系。
- 两个全新的外部联系人测试号：一个标准新增，一个半联系人新增。
- 一个由渠道码专项能力创建并已在 v3 登记 State 摘要的专用渠道码。
- 切换前 30 分钟旧入口的回调量、错误量和积压基线。
- v3 的 release SHA、`/healthz`、`/readyz`、worker timer 和数据库 migration 状态。

切换门禁：任一准备项不满足，不切换正式回调。

## 3. 切换前离线门禁

必须全部通过：

1. CI 的 `make check`、竞态测试和 PostgreSQL 集成测试通过。
2. v3 生产边界完成 synthetic 回调：验签、解密、Inbox、worker、OneID、receipt 全链路成功。
3. synthetic 回调被明确标记，不与真实业务统计混淆。
4. 错签名、过期时间、CorpID 不匹配、畸形 XML、超大请求和重复 query 参数都被拒绝且不落 Inbox。
5. 查询接口不返回 external_userid、State 原值、原始 XML、Token、AESKey 或操作理由。
6. 禁用 callback 开关不会影响员工企微 OAuth 和侧边栏基础能力。

## 4. 短时切换步骤

1. 在企微管理后台把外部联系人回调 URL 改为 v3 URL，Token 和 EncodingAESKey 使用已保管的同一组生产值。
2. 完成企微 GET URL 验证；验证失败立即恢复旧 URL，不继续测试。
3. 用标准测试号扫描专用渠道码并添加第一名测试员工。
4. 在 60 秒内确认 v3 收到回调并返回成功 ACK；运行/等待 oneshot worker。
5. 只通过安全运维接口和数据库安全投影确认：一个 Inbox、一个处理收据、一个 Customer、一个 active 企微身份、一条 active 跟进关系、一个渠道 entrant receipt。
6. 用同一测试号重复触发等价事件，确认不新增 Customer/identity，也不重复产生业务结果。
7. 用同一测试号添加第二名员工，确认仍是同一 OneID，并新增第二条 active 跟进关系。
8. 触发资料变更，确认复用同一 OneID；若事先漏掉新增事件，可信 edit 能自修复建客。
9. 删除第一名员工的跟进关系，确认只有对应关系 inactive，第二名员工关系保持 active。
10. 分别由每个员工删除该外部联系人，确认每个带对应 `UserID` 的 `del_external_contact` 只停用该员工关系；全部员工都完成后，关系才全部 inactive，而 Customer 和身份历史保留。
11. 用第二个测试号走半联系人新增，确认 OneID 和关系行为与规格一致。

## 5. 渠道归因专项用例

| 用例 | 预期 OneID | 预期渠道结果 |
|---|---|---|
| 已登记且唯一的 State | 创建或复用 | `channel_attributed` |
| 没有 State | 创建或复用 | `channel_unmatched` |
| 未登记 State | 创建或复用 | `channel_unmatched` |
| 同一摘要存在两个有效绑定 | 创建或复用 | `channel_ambiguous`，不猜渠道 |
| OneID 强身份冲突 | 不错误归属 | `identity_conflict`，不写渠道客户归属，渠道 reconcile 拒绝绕过 OneID |

渠道归因失败不得阻断可信 OneID 创建。原始 State 不得出现在日志、API 或测试截图中。

## 6. 观察与放量

专用用例通过后先保持 30 分钟观察，再决定是否继续让正式入口指向 v3。观察项：

- Inbox `received/processing/retryable/failed` 数量和最老等待时间。
- 回调 HTTP 4xx/5xx 比例与加密 ACK 成功率。
- `failed_terminal`、`identity_conflict`、`channel_ambiguous` 和 `channel_unmatched` 数量。
- worker timer 最近成功时间、每批处理数量和租约接管情况。
- 相同回调是否只对应一个 Inbox 和一次业务结果。
- 新 Customer 与 active `wecom_external_userid` 是否一一对应。
- 日志是否出现敏感值；发现即回滚并按安全事件处理。

建议门禁：持续 30 分钟无 5xx、无积压增长、无错误归属、无敏感信息泄漏、专用全链路用例全部通过，才视为本次切换成功。

## 7. 立即回滚条件

出现任一情况立即把企微回调 URL 恢复为旧 URL：

- GET 验证失败或 POST 无法稳定返回加密 ACK。
- 回调持续 5xx、Inbox 积压增长或 worker 无法处理。
- 同一外部联系人创建多个 OneID，或出现错误客户归属。
- 删除事件错误创建 Customer，或旧事件覆盖新关系状态。
- State 被错误匹配，或歧义时仍选择了某个渠道。
- 日志/API 暴露 Token、AESKey、原始 XML、State、external_userid、手机号等敏感信息。
- v3 影响员工登录、OAuth、侧边栏或其他已上线能力。

回滚只切回入口，不删除已创建的 Customer、identity、Inbox 或 receipt。已被 v3 ACK 的事件不会由企微保证重投，应先保留证据并让已入库任务安全处理；如果处理逻辑本身会继续造成错误归属，则先关闭 v3 callback worker，保留 Inbox，修复后再受控重试。

## 8. 验收证据模板

每个用例只记录：

- 时间、环境、release SHA、用例编号。
- callback receipt ID、Inbox ID、Customer ID、结果码和处理耗时。
- 是否重放、是否创建 OneID、关系变化、渠道结果。
- 截图或查询结果中的敏感字段必须遮盖。
- 结论：PASS / FAIL / NOT_EXECUTED。

在正式切换前，以下项目保持 `NOT_EXECUTED`：真实企微 GET 验证、真实新增/半联系人新增、真实删除/编辑、真实渠道码 State 归因、旧入口切回演练。
