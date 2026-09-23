# v2 内容雷达生产源盘点

生产迁移只读取冻结 v2 提交 `6bfbe5816bb89913c70adaca87d6a486260e016e` 对应的两张表：

- `radar_links`：仅 ID、公开码、名称、标题、HTTPS 目标、Media 引用、状态、版本、操作者 ID 和时间。
- `radar_link_events`：仅 ID、雷达 ID、阶段、页码和时间。

捕获脚本使用 `REPEATABLE READ READ ONLY`、固定字段 JSON 投影和 pinned SSH host。它不读取 receipt 原值、key/payload digest、来源 key、UnionID、OpenID、external_userid、手机号、IP、UA、referrer、OAuth code 或 token。

每次执行以生成的 snapshot digest 和 dry-run 数量为准，本文不固化生产数量。历史 link/image/PDF 定义导入后均为 `disabled + unionid_required`；媒体引用必须在 v3 Media 中已存在且启用，否则进入 quarantine。旧事件只写 `radar_legacy_events`，`identity_attributed=false`、`replayable=false`，不会创建或猜配 OneID。
