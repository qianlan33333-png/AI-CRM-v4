# 临时媒资准备 API 与 Port 契约

## 架构分类

OneID：不涉及。素材凭据不识别、不创建也不归属客户。

Persistence：素材扫描是 Media 的只读 Port；刷新轮次是 Outbound 拥有的 River 内部持久任务；上传是 `outbound_media` lane 上独立的 External Effect；消息仍是另一条 External Effect。上传请求不持有数据库事务，上传的 attempted/unknown 不得投影成消息 attempted/unknown。

## 缓存与有效期

缓存键为上传企业与凭据作用域摘要、上传接口、素材类型、内容 SHA-256 和必要的文件名。`source_ref` 是 Media 内部的 `image:<id>` 或 `attachment:<id>`，用于精确读取与审计，不参与共享身份。不同 `source_ref` 的相同文件可复用凭据。

成功回执必须包含 Provider `created_at`，`expires_at = created_at + 72h`。`valid_through` 只判断发送时是否足够有效，不缩短或改写真实到期时间。刷新成功后才切换 current；失败或 `outcome_unknown` 保留仍有效的旧 current。未知结果不得按 TTL 猜测失败或自动重传。

消息发送前的 `valid_through` 至少保留 30 秒执行余量；仅剩很短有效期的旧 ID 会先准备新凭据，不直接发送。该余量只用于发送 preflight，每日 02:00 仍全量刷新所有启用素材。

素材上传超时由 `AICRM_WECOM_MATERIAL_UPLOAD_TIMEOUT_SECONDS` 单独配置，默认 120 秒，允许 1–240 秒。EER River job 超时为 270 秒，严格小于 300 秒 attempt lease；消息 HTTP 请求仍保持 8 秒。

## HTTP

所有路径位于既有管理员鉴权和 CSRF 边界 `/api/admin` 下。POST 必须携带 `Idempotency-Key`；同一管理员、路径、命令和 key 返回原结果，payload 漂移返回冲突。

`GET /api/admin/media-preparations?cursor=<opaque>&limit=100`

```json
{"ok":true,"items":[{"source_ref":"image:42","source_type":"image","content_digest":"sha256:<hex>","file_name":"cover.png","media_type":"image/png","size_bytes":1234,"snapshot_version":3,"state":"ready","effect_id":"eer_42","media_id":"provider-id","source_digest":"sha256:<hex>","failure_code":"","credential_state":"ready","credential_usable":true,"provider_created_at":"2026-09-10T02:00:01+08:00","last_succeeded_at":"2026-09-10T02:00:01+08:00","expires_at":"2026-09-13T02:00:01+08:00","next_refresh_at":"2026-09-11T02:00:00+08:00"}],"today_refresh_round":null,"next_refresh_at":"2026-09-11T02:00:00+08:00","next_cursor":"opaque","done":false}
```

`today_refresh_round` 在当天尚无每日轮次时必须为 JSON `null`，不能伪装成已完成的零项轮次。存在轮次时使用下述 refresh round DTO。

扫描时发现原始 bytes 已缺失或素材元数据不能形成冻结快照，仍会在顶层
`failures` 返回可定位条目，例如
`{"source_ref":"attachment:9","failure_code":"source_bytes_missing","state":"final_failed","credential_state":"missing","credential_usable":false}`。
该条目不含 `content_digest`、文件名、类型或 `media_id`，因为服务没有证据可以安全推断它们；管理界面应提示管理员补传原文件，而不是把它从素材状态列表静默移除。

`state` 是最近一次准备或刷新尝试的状态；它不能单独表示当前凭据是否可发送。`credential_state` 只取 `ready`、`expired`、`missing`，`credential_usable` 是对应布尔值。刷新失败但旧凭据仍有效时，示例为 `state=final_failed, credential_state=ready, credential_usable=true`；旧凭据过期时仍保留 `media_id` 与 `expires_at` 供审计，但返回 `credential_state=expired, credential_usable=false`。

手动全量刷新请求固定为 `{ "force": true }`；`false` 是无效请求。服务端只接受北京时间当天（handler 不接受客户端日期），同一管理员与 `Idempotency-Key` 重放同一轮次。

手动单素材 `POST /api/admin/media-preparations/{source_ref}/prepare` 同样固定要求 `{ "force": true }`。按需发送准备走内部 `ReadyForSend`，不借用管理员操作键。

`outbound_media` 的 `outcome_unknown` 只能由带明确结论的 EER 对账关闭。`POST /api/admin/external-effects/{effect_id}/reconcile` 的 `outcome` 必须为 `no_effect`，或为 `confirmed_effect` 并同时提供非空 `media_id` 与 RFC3339 `provider_created_at`。后者保存为 `outbound.material.upload.v1` 证据，凭据到期时间严格为该 Provider 时间加 72 小时。省略结论会拒绝且 EER 与素材准备均保持 `outcome_unknown`；相同幂等键重放原结论，换结论返回冲突。
所有尚不存在的时间字段也序列化为 JSON `null`，不得输出 Go 零值时间。

`POST /api/admin/media-preparations/{source_ref}/prepare`

```json
{"force":true}
```

返回 `202`，body 为 `{"ok":true,"material":<GET item>}`。`force=false` 返回 `400`；`force=true` 用管理员与 Idempotency-Key 区分手动轮次。同 key 重放不创建新上传。

`POST /api/admin/media-preparations/refresh-rounds`

```json
{"force":true}
```

服务端使用 Asia/Shanghai 当天日期。手动轮次由管理员与 Idempotency-Key 唯一；不同 key 可在同一天创建不同轮次。每日 02:00 轮次按本地日期唯一。

`GET /api/admin/media-preparations/refresh-rounds/{id}`

```json
{"ok":true,"refresh_round":{"id":7,"local_date":"2026-09-10","state":"running","cursor":"opaque","total":120,"queued":80,"succeeded":35,"failed":1,"unknown":0,"started_at":"2026-09-10T02:00:00+08:00","completed_at":null}}
```

## Go Port

跨域只使用 [`internal/outbound/port/material.go`](../../internal/outbound/port/material.go) 的 `MaterialSourceReader`、`MaterialStatusReader`、`MaterialPreparer` 和 `MaterialRefresher`。读取字节必须同时匹配 `source_ref`、`content_digest` 与 `snapshot_version`；发生漂移时不得上传。

消息 External Effect 的可选发送前检查实现 `externaleffects/port.ProviderPreflighter`。未就绪时返回同一 River job 的 Snooze；External Effects 在检查之后重新校验 queued generation/job，之后才创建真实 attempt。此等待不增加 `attempt_count`，不调用消息 Provider，也不触发消息完成 sink。

GroupOps 接受时保存 `GroupOpsMaterialIntentSnapshot` 和精确 source snapshot；image/file/miniprogram 的临时 `media_id` 不是审批内容，不写入 intent snapshot。已接受的 GroupOps effect 在同一 EER job 上等待素材准备，准备成功后自动继续，不要求管理员再次点击接受。

`outbound_excel` 与 `outbound_media` 对企微明确频控拒绝（已证明没有创建 Provider 效果）在原 River job/原 lane 内有界退避，最多 5 次；运输或响应不明的 `outcome_unknown` 不自动重试。并发 3/2 是单个 effects-worker 进程的 lane 容量；部署多个 worker 副本会按副本数叠加，本次未修改生产副本配置。
