# 问卷外推生产接通收口

状态：已实现并完成本地验证；未部署、未启用 Provider、未产生真实外推。

## 分类和边界

- OneID：外推只经既有 `ExternalIdentityValueReader` 读取配置的 `kind` 与 `scope` 的已验证外部标识，并在提交事务中冻结。没有该标识时保持可解释的失败；不得由手机号、`customer_id` 或未带 scope 的值推断 `user_id`。
- 持久化和外部效果：Survey 的提交/合成测试快照、业务收据、审计、Outbox 与 External Effects 接纳均在既有 PostgreSQL Unit of Work 中完成；HTTP 只由 `outbound` 在事务外执行。网络错误在调用开始后为 `outcome_unknown`，复用原幂等键，不能重发。
- 本轮不新增迁移、队列、旧接口或历史发送；不改 0101/0102 的群/配置工作。

## 字段和协议依据

| 项目 | 证据 | 收口要求 |
| --- | --- | --- |
| 管理绑定 | `survey_operation_configurations` 保存 `external_push_enabled`、不透明 `external_push_configuration_ref`、metadata 与 version；`/operations/external-push` 有 CSRF 和 CAS，冻结页面的两次保存会保留 metadata。当前 V3 生产没有该表的业务绑定行。 | 管理页只列出 Composition 中已白名单的 opaque 引用，并拒绝启用未知引用；数据库不保存 URL 或密钥。不迁移旧七份问卷业务数据。 |
| 载荷 | 旧 `questionnaire/external_push.py` 发送 `user_id`、`questionnaire_title`、`submitted_at`、`phone_number`、`answers`，并按配置追加 `day`、`frequency`、`expires_at_ts`、`type`、`remark`、自定义参数和评估快照。 | `day`、`frequency`、`expires_at_ts` 使用精确 snake_case JSON tag；回归测试必须证明三项进入冻结 policy 和最终 body。 |
| 签名 | 旧 `WebhookHmacSigner` 对 `timestamp + "\\n" + event_id + "\\n" + 原始 body` 做 HMAC-SHA256，使用 `X-AICRM-*` headers。 | 保持已有冻结签名向量和不跟随重定向的 HTTP 客户端。 |
| 密钥 | 旧运行时从 runtime setting 取得 `secretref:file:*`，再由受限文件秘密库解析；不能把 runtime environment 字符串当作 HMAC key。 | 总控以旧服务的 runtime setting + secretref 解析实际密钥后，安全转入 V3 既有 Composition-only `targetsJSON` 的 base64 `signing_key`；真实 ref、URL 和密钥不写入数据库、日志、测试输出或 PR。V3 不依赖旧库或旧秘密目录。 |
| 保存前测试 | 旧操作使用 `user_id=questionnaire_test`、空答案、`phone_number=NULL`、`is_test=true` 及独立 test run。 | 已有合成快照/External Effects 接纳链只对隔离接收端测试；禁用或未配置时零网络。HTTP 2xx 只表示接收方运输层接受，不等同于其业务生效。 |

## 验收

1. Composition 对 target JSON 的 `day`、`frequency`、`expires_at_ts` 做精确解析，保留既有受保护 `signing_key` 白名单解析。
2. 真实 PG16 覆盖一次合成测试从管理员保存、不可变快照、接纳到效果状态回读；接纳失败时 Survey 收据/快照/效果均回滚，超时保持 `outcome_unknown`。
3. 隔离 HTTP 接收端验证原始 body 与 HMAC 向量、headers、失败和跳转处理；不调用生产端。
4. 生产接通步骤由总控执行：先部署并验证配置引用/密钥引用/Provider readiness，再对单个问卷执行合成测试；以外推的固定 event id 在接收方核对业务处理和去重收据。V3 的 `executed` 仅为 HTTP 2xx 运输层接受，不能据此声明业务生效；接收方未知/未确认时保持对账，不新建幂等键重发。最后才考虑启用其余绑定。
