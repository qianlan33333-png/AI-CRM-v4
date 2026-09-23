# Group Ops Webhook 日课课卡封面 PRD 增量

**状态：设计已审核，待本分支实现、评审和部署。当前生产不能按此接口投递。**

## 决策

OneID：不涉及。请求只包含计划已绑定的群稳定引用和 Media 素材引用，不包含客户或外部身份。

持久化和外部效果：日课封面下载是 Media 的 Provider 读取，在任何 PostgreSQL 事务外完成。Media 的本地图片和小程序素材、Group Ops 的 run、intent、execution、审计、Outbox 与既有 External Effects acceptance 必须在同一个 PostgreSQL Unit of Work 一起提交或回滚。没有新的队列、Worker、重试内核或企微写 Adapter；最终仍由 Outbound 经既有 External Effects 和 add_msg_template 任务路径执行。

## 受限自动封面规则

旧服只读行为审计确认的是一个日课课卡叶子协议，不是任意小程序 AppID/path 的取图能力：

1. 调用方自动解析形态只接受公开 AppID wx0ca836834b18e989，以及严格 path pages/article/article?lesson_id=UUID&from=learn。
2. UUID 必须规范；不允许额外或重复 query 参数、scheme、host、fragment 或其他 path。
3. 只有 Media Adapter 可以从固定来源 https://ip.lhbl.com.cn/api/share/lesson-card/UUID.png 构造读取地址。请求 JSON、计划配置和管理员输入都不能提供来源 URL。
4. HTTP client 固定 HTTPS host、禁止 redirect、有界 timeout，并以 LimitReader(MaxImageBytes+1) 读取。只接受 image/png；随后以 image.DecodeConfig 和完整 PNG decode 复用现有 Media 的 10 MiB、单边 10,000、40,000,000 像素限制。
5. title 的控制字符、首尾空白或超长在 HTTP 严格 JSON 形状校验中返回 400 invalid_request；其他不匹配 AppID/path 的输入在任何取图前返回 400 miniprogram_cover_unsupported。匹配来源后的读取失败、非 PNG、截断、超限或解码失败返回 503 miniprogram_cover_unavailable。它们都不会创建 run、Media 素材、EER、River job 或 Provider 调用。
6. Webhook text 同样在严格入口拒绝 NUL，以免 PostgreSQL JSONB 在接受事务中才拒绝；正常换行和 Tab 仍可用于话术。

来源依据是旧服 release 41f80a11835445c034fdd39f69a6b6712722bb98 的 aicrm_next/automation/automation_engine/group_ops/broadcast.py 第 40 至 99 行及 aicrm_next/channels/integration_gateway/lesson_card_cover_client.py 第 9 至 48 行。旧服仅是只读叶子协议供体，不是 V3 运行时依赖。

Go 实现复用标准库 https://pkg.go.dev/net/http 的取消和 body 关闭语义，以及 https://pkg.go.dev/image#DecodeConfig 的配置解码边界。

## Media Port 和事务边界

Media 的稳定 Port 定义 PreparedWebhookMiniProgram 和两个操作：

1. PrepareWebhookMiniProgram 在 Group Ops 验证活动计划、Webhook descriptor 和目标群子集之后执行。它只做受限 GET 和 PNG 验证，返回未持久化的 Media-owned prepared value。
2. MaterializeWebhookMiniProgramWithin 在最终锁定计划、确认不是既有 replay 之后执行。它从 prepared value 写入本地 enabled 图片及小程序素材，返回 local miniprogram reference。该调用使用已有 Group Ops UoW 的 transaction context，禁止再开事务。

Group Ops 只持有并传递 Media Port 的 prepared value，绝不 import Media app/store。重放存在 run 时跳过 GET 和 materialize；并发同事件只允许一个 run/effect 集合与一套 Media materialization 胜出。锁内会再次检查目标绑定，防止并发解绑后继续接受。自动创建素材的 Media 审计 actor 取计划 UpdatedBy，仅表示受该计划最后编辑者名义创建；Webhook 调用来源仍由 client ID、event ID 和冻结请求摘要审计，不把该管理员描述为本次请求的直接调用者。

## 非日课页面和人工封面

没有经过上述限定协议的页面（包括旧 observation path）不能自动取图。管理员先走现有 image-library multipart 上传取得 local image_id，再走现有 miniprogram-library 创建 enabled 的本地小程序素材，其 thumb_image_id 使用该图片。动态 Webhook 以唯一形态 {"type":"miniprogram","miniprogram_id":789} 引用该本地素材。

自动形态 {"type":"miniprogram","appid":"wx0ca836834b18e989","path":"...","title":"..."} 与 miniprogram_id 互斥，且都不接受 cover URL、provider media_id 或原始图片字节。现有 image_id 和 attachment_id 引用形态保持不变。

## 验收

- HTTP parser 覆盖固定 AppID/path、UUID、重复/额外 query、redirect、非 PNG、大小、截断、timeout 和 PNG 尺寸限制。
- Runtime 在无效计划、暂停计划、无效目标及已有 replay 时不调用 GET。
- 真实 PostgreSQL journey 在 Media 图片、小程序、Media audit/outbox、Group Ops run/intent/execution、EER 或 River 写入任一失败时确认整个 UoW 回滚。
- 真实 Provider 预检仅验证冻结本地素材进入既有 Outbound shape；不发送真实群消息。202 代表本地接受，企微成员实际执行和送达回执仍是独立状态。
