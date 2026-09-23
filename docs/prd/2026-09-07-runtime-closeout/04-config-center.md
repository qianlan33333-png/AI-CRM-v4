# 配置中心：旧版分类、表单与真实生效

## 实现前架构判断

OneID：不涉及。配置中心不读取、解析或写入客户或外部身份。

持久化：Config 领域在同一个 PostgreSQL Unit of Work 中写入草稿版本、校验结果、发布指针、审计、幂等回执和 outbox；运行进程启动后另行追加只读的应用事实。没有内部持久任务，也不在此命令中调用 Provider。

External Effects：不涉及。开启配置只改变受控本地运行配置；它不会发送消息、退款、支付或调用 Provider。Provider 是否接受/投递仍由其 Owner 的效果和对账状态决定。

## 供体与界面复用

* 只读供体：`AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`，`aicrm_next/platform/admin_config/{category_registry.py,api.py,application.py,application_support.py}`。
* 原样界面结构：`aicrm_next/app/admin_console/templates/admin_console/config_center.html`，blob `70a4f93c680ac0f3719194e7e9283ef5565c99a9`；详情表单：`config_category_detail.html`，blob `6c79959f0e81e047ca6e70488a11029087fccf6d`；样式：`static/admin_console/config_center.css`，blob `0881be6631204c46b258137fd12138b1a8bbb37f`。
* 供体 registry 固定十个普通类别。旧版的 `config_api_key.html` 与 `config_api_clients.html` 是另外两行，因此 V3 以十二项表格展示。V3 的唯一壳仍是 `internal/webshell/templates/admin_base.html`；Host/Adapter 负责接口适配，不挂第二侧栏，也不公开供体页面。
* 首页严格保留供体的四列与顺序：`类目`、`是否生效`、`生效开关`、`配置`。只有企业微信基础、微信支付、微信小店和公众号授权具有一个明确的 V3 主启用字段，才显示开关；点击开关只创建完整快照草稿并跳转校验/发布，绝不直接改环境或调用 Provider。没有单一主开关的类别显示 `—` 和配置入口；专用 Owner 或不支持类别保持正确的管理入口或禁用标记。首页不堆放进程、revision 或消费者说明，这些事实留在详情和发布记录。

## 字段、命令与消费者映射

| 旧类别与逐字段来源 | V3 等价及消费者 | 保存、发布和实际应用 |
| --- | --- | --- |
| 企业微信基础：`WECOM_CORP_ID`、`WECOM_AGENT_ID`、`WECOM_SECRET`、`WECOM_CONTACT_SECRET`、`WECOM_API_BASE`、`WECOM_DEFAULT_OWNER_USERID`、`WECOM_CALLBACK_TOKEN`、`WECOM_CALLBACK_AES_KEY`、`WECOM_ARCHIVE_SECRET`、`WECOM_PRIVATE_KEY_PATH`、`WECOM_SDK_LIB_PATH`、`WECOM_ARCHIVE_TIMEOUT`、`WECOM_CORP_TAG_LIMIT` | `wecom.{enabled,corp_id,agent_id,callback_enabled,customer_sync_enabled}` 给 `wecomadapter.Client`、callback handler、customer sync；`message_archive.{enabled,page_limit,page_budget}` 给 `wecomadapter.MessageArchiveReader`、`messagearchive.Service`、archive callback dispatcher。密钥只保留 `environment://...` 安全引用。旧 API base/default owner/tag limit/SDK path/timeout 在 V3 无安全等价，逐项显示不支持，不能写 env。 | 非秘密键进入 immutable runtime release；secret-reference 进入同一版本但不会存秘密。API/worker/effects-worker 在下一次受控启动读 active revision 并记录 application；没有应用事实就是“待受控重启”。类别检查验证 CorpID/AgentID、引用存在和 archive 依赖，绝不调用企微。 |
| 后台访问：`ADMIN_AUTH_MODE`、`ADMIN_LOGIN_REDIRECT_URI`、`ADMIN_WECHAT_TRUSTED_DOMAIN` | Access 是唯一 Owner：后台成员、密码和会话都经 `internal/access`。没有配置中心复制密码表或 HTTP 环境写入。 | 表格提供真实的 Access 管理入口及当前安全状态；此类别不制造本地“已生效”开关。旧 redirect/domain 没有 V3 Owner，明确不支持。 |
| 侧边栏与身份：`SIDEBAR_PRODUCT_CONTEXT_TOKEN_TTL_SECONDS`、`SIDEBAR_CONTEXT_TOKEN_TTL_SECONDS`、`AICRM_SIDEBAR_JSSDK_ADAPTER_MODE`、`AICRM_SIDEBAR_JSSDK_REAL_ENABLED`、`AICRM_SIDEBAR_JSSDK_SECRET`、`AICRM_SIDEBAR_JSSDK_TIMEOUT_SECONDS`、`AICRM_SIDEBAR_IMAGE_QUICK_KEYWORDS` | `sidebar.context_token_ttl_seconds` 给 `wecom.ContextTokenService`；JSSDK 仍由 `wecom` Provider/签名 Owner 控制，secret 是安全引用。产品 context、关键词、adapter mode/timeout无 V3等价。 | TTL 是发布版本的受控启动项；JSSDK 依赖检查只验证配置与 Provider 开关，显示 provider 未配置/待应用，不能承诺 wx.config 成功。 |
| AI 与自动化：`DEEPSEEK_*`、`AICRM_AUTH_*`、各 machine client ID/secret ref | `automation.operations.{provider_mode,max_recipients_per_run}` 给 Segment/Automation、outbound message provider；`ai_assistant.ui_enabled` 与 `ai_assistant.dispatch_enabled` 给 AI Assistant handler/intent；`intake_enabled` 只控制旧 signed intake，V1 caller/OAuth2 创建待审计划不受它控制。机器令牌和 OAuth client 仍由 Access/Open Platform Owner 管理。DeepSeek/旧统一授权字段无 V3 Provider，不伪造。 | automation max 的 API/worker usage 与冻结 run 版本回读；其他启动项通过 application fact 回读。mode 受枚举、既有固定发送授权、受保护凭据及 effects+WeCom 依赖校验，开关不等同 Provider 成功。 |
| CRM 开放 API Key：旧 `config_api_key.html` / `direct_api_key` | V3 仅 `internal/openplatform` 的 V1 caller 管理与 Access machine credential/OAuth2。Direct API Key 已退休。 | 打开实际 caller 管理；不在 Config 保存 token、不恢复 56 条机器接口。 |
| API 接入与 Token：旧 `config_api_clients.html`、`config_api_client_detail.html` | 同一 Open Platform V1 caller/OAuth2 入口，调用方、scope、状态由 Access Owner 管理。 | 打开实际管理页；token 只在 Owner 受控签发/轮换，Config 不显示或保存。 |
| Webhook 与外推：`OPENCLAW_*`、`QUESTIONNAIRE_*`、`AICRM_EXTERNAL_EFFECT_*`、`OUTBOUND_WEBHOOK_RETRY_*` | `effects.provider_enabled`、`survey.completion_provider_enabled`、`commerce.push.provider_enabled` 给 External Effects/Survey/Commerce 已有消费者。URL、HMAC、allowlist、retry和未注册 adapter 均无安全的 V3 Config 等价。 | 仅发布本地启动配置；检查报告 effect/provider 依赖，且明确 accepted/queued 不是外部成功。不得由保存触发外推。 |
| 稳定性：`HTTP_DEFAULT_TIMEOUT`、`HTTP_RETRY_MAX`、`HTTP_RETRY_BACKOFF_BASE`、`CIRCUIT_FAILURE_THRESHOLD`、`CIRCUIT_RECOVERY_SECONDS`、`RQ_DEFAULT_TIMEOUT`、`OUTBOX_MAX_ATTEMPTS`、`OUTBOX_BACKOFF_BASE_SECONDS`、`REDIS_URL` | `stability.worker_limit` 给 Composition 的 Inbox 单次 claim 批量处理；它不是 Worker 并发数；旧 HTTP/RQ/Redis/retry 状态机与 V3 River/External Effects 的 Owner 不等价。 | Inbox 单次 claim 处理条数为受控启动项；其余字段显示 V3 不支持，不能自建 Redis、ticker 或第二重试内核。 |
| 微信支付：`WECHAT_PAY_ENABLED`、AppID/MchID/cert serial/API v3 key/private/public key path/notify URL/API base/timeout/product catalog | `wechat_pay.{provider_enabled,app_id,app_scope,h5_oauth_enabled,h5_app_id,h5_app_scope,merchant_id,merchant_serial,private_key_path,platform_cert_path}` 给 `paymentprovider.NewWeChatPay`、callback verifier。密钥均安全引用。旧 notify/API base/timeout/catalog 没有独立 V3配置 Owner。 | 非秘密启动项可发布，安全引用只作 presence 回读，下一次受控启动由 payment provider 实际构造并记应用；检查失败则未生效。保存从不创建支付/退款。 |
| 支付宝支付：全部 `ALIPAY_*` | V3 没有支付宝 Provider、路由或支付 Owner。 | 类别和字段保留为禁用说明；不能保存、发布或显示已生效。 |
| 微信小店：`WECHAT_SHOP_ENABLED`、AppID/AppSecret/API base/callback token/timeout | `wechat_shop.{provider_enabled,app_id}` 及 app secret/callback token/AES 安全引用给 `paymentprovider.NewShopCallbackVerifier`。API base/timeout尚无 V3 Owner。 | 受控启动发布并以 provider 构造/application 回读；不触发店铺写入。 |
| 公众号授权：`WECHAT_MP_APP_ID`、`WECHAT_MP_APP_SECRET`、`WECHAT_MP_OAUTH_SCOPE` | `survey.oauth.{enabled,app_id,open_platform_id,scope}` 给 Survey/Radar OAuth provider；secret 是安全引用。 | 发布后受控启动应用；已绑定 Open Platform scope 拒绝普通发布修改，检查只验证 OAuth 配置形状和回调依赖，不替用户在公众号后台改域名。 |


## PR #190 最终验收矩阵

下表是页面十二个类目的最终交付口径。`启动读取确认` 指 active snapshot 在该角色成功完成 Composition、Adapter 构造与配置校验后写入的 `(revision, source, role, release_sha, snapshot_checksum)` 事实；它不是 Provider 成功、支付完成或消息送达。除“单次运行最大人数”外，发布后的运行时字段都要求 API、worker、effects-worker 重新启动并读取同一 checksum。页面只有收齐本类目字段所需角色的同一版本事实，才显示“已发布并已读取”或“已关闭”；关闭版本尚未读取时显示“关闭已发布，待读取”。

| 类目 | 本轮可审阅字段与入口 | 发布后消费者及动作 | 缺少前置条件、受控字段或禁用项 |
| --- | --- | --- | --- |
| 企业微信基础 | `wecom.enabled`、`wecom.agent_id`、`wecom.callback_enabled`、`wecom.customer_sync_enabled`、`message_archive.{enabled,page_limit,page_budget}`；`wecom.corp_id` 仅可首次补齐或保存原值；五个固定 secret-reference 只回读 presence。 | API callback、客户同步/存档 worker、effects-worker 均在受控重启后读取；回调、通讯录、存档 Adapter 全部构造成功后才记录应用事实。 | 启用企业微信须已有企业微信 Secret 与上下文签名部署契约；会话存档还须存档密钥、Runner、SDK 库和私钥路径。缺任一引用或 Adapter 失败不写应用事实。API base、旧负责人、SDK/超时和标签上限逐项保持部署管理或退休状态。 |
| 后台访问 | `/admin/admin-access` 的成员、会话与访问管理入口；Config 不写任何 Access 字段。 | Access Owner 自己管理；没有 runtime release 或 Config 应用事实。 | 旧登录模式、重定向和可信域是 Access 受保护部署项；不在 Config 创建密码、副本凭据或伪开关。 |
| 侧边栏与身份 | `sidebar.context_token_ttl_seconds`；JSSDK Secret 只显示闭集引用 presence。 | 所有角色的启动快照一致；实际 Context Token 服务由 API 使用，发布后重启并回读 checksum。 | JSSDK adapter mode、超时、产品旧 token、图片关键词无可发布等价；JSSDK 密钥缺失只显示未配置，不把侧边栏说成已接通。 |
| AI 与自动化 | `automation.operations.{provider_mode,max_recipients_per_run}`、`ai_assistant.{ui_enabled,dispatch_enabled}`；旧 signed intake 和两个安全引用只读。 | mode、UI、dispatch 在三角色受控重启后读取；`max_recipients_per_run` 由 API/worker 对新预览、确认和执行写 usage 快照，它是单次运行人数上限。 | mode 非 `disabled` 须已有固定发送授权、通讯录 Secret、企业微信和受控外推依赖；dispatch 须已有私信授权。旧 signed intake 保持部署禁用，不能以此判断六个 V1 Open Platform 操作是否可用。 |
| CRM 开放 API Key | `/admin/api-docs` 的实际 Open Platform caller/OAuth2 管理入口。 | Access/Open Platform Owner 签发或轮换 caller 凭据；没有 Config release。 | Direct API Key 与旧 56 条机器接口已退休；Config 不保存 token，也不新增平行认证。 |
| API 接入与 Token | 同一 `/admin/api-docs` caller、scope 与令牌管理入口。 | Access/Open Platform Owner 管理；没有 Config release。 | 只保留 V1 caller/OAuth2，配置中心不能编辑 machine client、scope 或 token。 |
| Webhook 与外推 | `effects.provider_enabled`、`survey.completion_provider_enabled`、`commerce.push.provider_enabled`、`groupops.directory_read_enabled`、`groupops.dispatch_enabled`。 | 三角色重启读取。群目录只在 `directory_read_enabled || dispatch_enabled` 时可读；发送意图只在 `dispatch_enabled` 时接受。 | 问卷/商品外推须受控外推执行；群发送还须企业微信与受控外推，目录读取本身不授予发送。URL、allowlist、重试、webhook key 和 Provider 写权限由受保护部署/各 Owner 管理；发布不产生外部调用。 |
| 稳定性 | `stability.worker_limit`。 | 三角色重启读取；Inbox worker 用它限制一次 claim 处理条数。 | 该值不是 Worker 并发数。HTTP 重试、熔断、RQ/Redis、Outbox 重试均由既有平台 Owner 管理或已退休，不能由 Config 另建可靠性内核。 |
| 微信支付 | `wechat_pay.{provider_enabled,app_id,h5_oauth_enabled,h5_app_id,merchant_id,merchant_serial}`；三个 secret-reference presence；两个 App Scope 只读受控。 | API/worker/effects-worker 重启读取，支付 Provider 与验签 Adapter 成功构造后写应用事实。 | 支付开启须已有 AppSecret、私钥、平台证书与 API v3 Key；H5 开启须已有 H5 Secret 与订单联系人数据键。非空 AppID、H5 AppID、商户号不可普通发布改绑，merchant serial 可轮换；回调 URL、服务地址、超时和商品目录不是 Config 字段。 |
| 支付宝支付 | 保留旧类别和逐字段禁用说明。 | 无消费者、无发布动作。 | V3 没有支付宝 Provider 或路由；不能保存、发布或显示已生效。 |
| 微信小店 | `wechat_shop.{provider_enabled,app_id}`；AppSecret、Callback Token、Callback EncodingAESKey 仅显示 presence。 | 三角色重启读取；小店回调验签 Adapter 成功构造后写应用事实。 | 开启须三个小店专属受保护引用同时存在。Callback AES Key 缺失时保持未启用，不能借用企业微信或公众号密钥；服务地址和超时仍是部署管理。非空 AppID 只能走小店接入迁移。 |
| 公众号授权 | `survey.oauth.{enabled,app_id,open_platform_id}`；OAuth scope 与 AppSecret 是受控只读。 | API/worker/effects-worker 重启读取，Survey/Radar OAuth Provider 成功构造后写应用事实。 | 开启须 OAuth Secret，且 AppID、Open Platform ID、scope 已形成可验证组合。非空 AppID/Open Platform ID 或 scope 不允许普通 Config 改绑；Config 不替管理员修改公众号后台域名。 |

开放接口继续只保留六个 V1 caller 能力：`GET /open/v1/capabilities`、`POST /open/v1/customers:resolve`、`GET /open/v1/customers/{customer_id}`、`GET /open/v1/customers/{customer_id}/activities`、`POST /open/v1/ai/review-plans`、`GET /open/v1/operations/{operation_id}`。它们由现有 Open Platform caller 与 OAuth2 scope 授权；AI 的旧 signed intake 始终不是这些调用的旁路或开关。

## 受控应用协议

1. 管理员在一个类别编辑字段；Host 用当前 effective snapshot 组成完整草稿并要求确认。
2. Config 在一个 UoW 中创建不可变 release、校验、CAS 发布/回滚、审计、幂等收据及 outbox。保存草稿不是发布，发布也不是 Provider 成功。
3. Composition 在构造领域 Provider 前读取 active release，把闭集非秘密值投影到 `platform/config.Runtime`；环境只做尚无发布版本时的兼容默认。Secret reference 只允许该字段的固定受保护环境槽位，运行时读取值不进入 Config、HTTP 响应、审计或日志。
4. 每个启动角色追加 `config_runtime_applications` 事实。页面同时显示 published revision、每角色应用 revision、automation 的真实 usage；角色缺失或 revision 落后时状态为“待受控应用”，不会显示“已生效”。
5. 部署 Owner 只需在受保护部署材料中更新固定 secret 槽位、发布审核通过的准确二进制并重启对应角色；应用记录、`/readyz` 的 SHA 与页面回读是核验依据。没有这一链路，配置仍是待应用。


## 字段状态账本与身份边界

`internal/config/runtime_catalog.go` 的 `legacyCatalogFields` 为供体 registry 中每一个旧字段生成独立行，且每行恰有一种状态：V3 可发布字段、固定安全引用及其真实 presence、由明确 Owner 的受保护部署清单管理、或已退休/本轮不支持。它不合并多个字段为一个“unsupported”占位，也不会将路径、URL、allowlist、超时或重试写进 HTTP 配置。

运行时的密钥/授权读取来源是 V3 的闭集受保护部署引用。旧服务的一次性导出只可作为迁移输入，由部署 Owner 转入 V3 受保护配置；运行中的 V3 不读取旧数据库、旧文件或旧服务。页面只回读当前启动角色报告的 `configured` 布尔值，绝不返回值、散列或可枚举环境变量名。

`wecom.corp_id`、`survey.oauth_app_id`、`survey.oauth_open_platform_id`、`wechat_pay.app_id`、`wechat_pay.h5_app_id`、`wechat_pay.merchant_id` 与 `wechat_shop.app_id` 都是已绑定身份或接入标识：已有非空部署值时，普通 Config 发布只能保存相同值，任何改变都必须被拒绝并走显式身份/接入迁移。这样不能把新 AppID 或商户号与旧支付、小店回调或 OAuth scope 静默组合。未配置的部署值由受控部署契约补齐。`wechat_pay.merchant_serial` 是证书轮换值，仍可按正常发布流程更新。`wechat_pay.app_scope`、`wechat_pay.h5_app_scope` 与 `survey.oauth_scope` 是部署受控 scope，普通 Config 发布不得修改。自动化 mode 和 AI dispatch 允许按草稿/校验/发布流程开关，但只有既有固定授权、全局依赖和受保护凭据都存在时才会校验通过；关闭不要求这些前置条件。

## 旧二进制回退

0102 扩大了运行时目录；0102 之前的程序只认识 `automation.operations.max_recipients_per_run`。因此普通“用此版本回滚”只用于仍运行新版程序，不能当作旧二进制回退的前置条件。

在切换程序前，管理员必须在新版的发布记录页面执行“准备旧程序恢复配置”。它通过带操作凭证、幂等键和 active revision CAS 的 `POST /api/admin/config/runtime-releases/legacy-binary-recovery` 原子发布一个只含上述旧字段的恢复版本；人数沿用当前生效值。服务同时把当前快照逐项与受保护部署默认值比较：支付、企业身份、OAuth、自动化、AI、群运营或其他新字段只要有差异便返回冲突，绝不发布恢复版本。冲突时先由受控部署把所需值落实为受保护默认值，在新版完成启动校验和应用回读后再重试。成功后核对返回版本仅含旧字段，才可以按独立部署流程回退程序。该动作不写环境、不会发送业务请求，也不会把程序回退本身标记成已完成。

启动应用事实的身份是 `(revision, source, role, release_sha, snapshot_checksum)`。`snapshot_checksum` 覆盖闭集 effective settings，所以 revision `0` 的环境默认发生变化时，旧应用记录不会证明当前快照已经读取。Composition 只在全部配置相关 Adapter、路由和启动校验成功后写入该事实；失败启动不会留下“已应用”记录。
