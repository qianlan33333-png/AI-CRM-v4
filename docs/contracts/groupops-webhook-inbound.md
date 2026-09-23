# 群运营动态 Webhook 接口

> **状态：待合并、待部署。** 本文描述的是当前分支的接口契约，给出的域名 URL 仅是上线后的调用格式示例；现有生产环境尚未具备这项动态请求能力，不能据此直接投递或发送消息。

此接口向 `plan_type=webhook` 的已启用群运营计划提交一次动态群发意图。它不读取计划节点：请求本身提供话术和目标群的子集。`正式群运营计划测试` 是计划名；目标只能是该计划已绑定群的稳定 `asset_reference`，不能传群名称。

## 先取得可用的目标引用

管理员会话下读取计划 `12` 的绑定群：

```http
GET /api/admin/automation-conversion/group-ops/plans/12/groups?limit=100&offset=0
Cookie: aicrm_admin_session=...
```

响应 `items[].asset_reference` 才能填入 `target_chat_references`。调用方应保存这些不透明引用；不要用显示名称、企微群名或 URL 猜测匹配。

## 图片和附件：先上传，再引用本地数字 ID

上传使用**管理员 Cookie 会话和 `X-CSRF-Token`**，与下文 Webhook 的 HMAC 鉴权不同。不要在 Webhook 内传文件字节、外部 URL、文件名代替 ID，或伪造 Bearer 鉴权。

```bash
# 图片：响应 item_id（也可从 item.id 读取）作为 image_id
curl --fail-with-body -X POST 'https://www.youcangogogo.com/api/admin/image-library/upload' \
  -H 'X-CSRF-Token: YOUR_ADMIN_CSRF_TOKEN' \
  -H 'Idempotency-Key: image-upload-20260913-001' \
  -b 'aicrm_admin_session=YOUR_ADMIN_SESSION' \
  -F 'image=@cover.png' \
  -F 'name=课程封面'

# 附件：响应顶层 id（也可从 item.id 读取）作为 attachment_id
curl --fail-with-body -X POST 'https://www.youcangogogo.com/api/admin/attachment-library/upload' \
  -H 'X-CSRF-Token: YOUR_ADMIN_CSRF_TOKEN' \
  -H 'Idempotency-Key: attachment-upload-20260913-001' \
  -b 'aicrm_admin_session=YOUR_ADMIN_SESSION' \
  -F 'attachment=@guide.pdf' \
  -F 'name=课程资料'
```

大附件走 `/api/admin/attachment-library/uploads` 分片流程时，以 complete 响应的 `attachment_id` 为准。

## Webhook 请求 JSON

接口：

```text
POST https://www.youcangogogo.com/api/automation/group-ops/webhooks/groupops-46d055d8-0276-449c-a2b5-170af39c3c92
```

分支部署后，下面的文字、图片、附件组合可以投递。把 `REPLACE_WITH_BOUND_ASSET_REFERENCE`、`123` 和 `456` 换成已读取/上传的真实值：

```json
{
  "webhook_reference": "groupops-46d055d8-0276-449c-a2b5-170af39c3c92",
  "target_chat_references": [
    "REPLACE_WITH_BOUND_ASSET_REFERENCE"
  ],
  "messages": [
    {"type": "text", "text": "今晚 8 点直播，资料已整理好。"},
    {"type": "image", "image_id": 123},
    {"type": "file", "attachment_id": 456}
  ]
}
```

规则如下：

- `webhook_reference` 必填，且必须严格等于 URL 最后一个片段；它也在被签名的 JSON 内。
- `target_chat_references` 至少一个，不允许重复；每个值都必须属于该计划的绑定群。
- `messages` 至少一个、最多十个。`text` 最多一个且只能放第一个；其余最多九个附件按数组顺序发送。文字不能含 NUL；普通换行和 Tab 可以保留。
- `image_id`、`attachment_id` 是 JSON **数字**，不是字符串。
- 未支持 `url`、`media_id`、原始图片/附件、任意远程下载地址，也不支持把文字放在图片或文件之后。

小程序支持两种互斥的安全引用形态。

日课课卡自动解析形态示例：

~~~json
{
  "webhook_reference": "groupops-46d055d8-0276-449c-a2b5-170af39c3c92",
  "target_chat_references": ["REPLACE_WITH_BOUND_ASSET_REFERENCE"],
  "messages": [
    {"type": "text", "text": "点击查看今日课程"},
    {
      "type": "miniprogram",
      "appid": "wx0ca836834b18e989",
      "path": "pages/article/article?lesson_id=REPLACE_WITH_LESSON_UUID&from=learn",
      "title": "今日课程"
    }
  ]
}
~~~

title 必填，最多 64 个 UTF-8 字节，不能含控制字符或首尾空白。自动封面只支持此公开 AppID 和严格日课
path：lesson_id 必须为小写规范 UUID，from=learn 必须唯一，不能有额外参数、host、
scheme 或 fragment。Media 才能以固定课卡来源读取并验证 PNG，再在接受事务中
创建本地图片和小程序素材。请求不能给封面 URL。title 中控制字符、首尾空白或超长会在 JSON 形状校验时返回 400 invalid_request；不匹配该 AppID/path 的输入返回
400 miniprogram_cover_unsupported；匹配后封面暂时无法取得或验证返回
503 miniprogram_cover_unavailable。两者都不会选择默认图或创建发送意图。

无已验证自动封面规则的页面先以管理员 Cookie 加 X-CSRF-Token 上传 PNG/JPEG，
再调用 POST /api/admin/miniprogram-library 创建已启用本地素材，例如：

~~~json
{
  "name": "观察期内容卡片",
  "appid": "THE_APPROVED_APPID",
  "pagepath": "pages/observation-issue/observation-issue?id=REPLACE_WITH_ID",
  "title": "内容详情",
  "thumb_image_id": 123,
  "enabled": true
}
~~~

以该响应的本地数字 item.id 作为 Webhook 素材引用：

~~~json
{
  "webhook_reference": "groupops-46d055d8-0276-449c-a2b5-170af39c3c92",
  "target_chat_references": ["REPLACE_WITH_BOUND_ASSET_REFERENCE"],
  "messages": [
    {"type": "text", "text": "查看内容详情"},
    {"type": "miniprogram", "miniprogram_id": 789}
  ]
}
~~~

miniprogram_id 是 JSON 数字，且与 appid、path、title、图片和文件字段互斥。
管理员图片/小程序素材创建的 Cookie/CSRF 鉴权与 Webhook HMAC 鉴权不同。

## 签名和 cURL

每个逻辑事件使用一个新 `X-AICRM-Event-Id`，长度为 16–256 个 ASCII 字符，范围 `0x21`–`0x7e`（不含空格）。它在客户端 ID `aicrm-webhook-group-ops` 下对**所有**群运营 Webhook URL 全局唯一。相同事件只能用完全相同的 JSON 重放；同事件但内容或 URL 不同返回冲突。

签名原文是以下精确 UTF-8 字节，最后的 JSON 必须保持原始字节，不要重新格式化：

```text
X-AICRM-Timestamp + "\n" + X-AICRM-Event-Id + "\n" + request body bytes
```

`X-AICRM-Signature` 是该原文的 HMAC-SHA256 小写十六进制，可选前缀 `sha256=`。不要把密钥写入代码、日志或本文档。

```bash
BODY_FILE=groupops-request.json
TIMESTAMP="$(date +%s)"
EVENT_ID='groupops-20260913-000001'

# 由调用方的密钥管理系统计算；GROUP_OPS_WEBHOOK_SECRET 仅作为本机示例变量。
SIGNATURE_HEX="$(python3 - "$BODY_FILE" "$TIMESTAMP" "$EVENT_ID" <<'PY'
import hashlib
import hmac
import os
import sys

secret = os.environ.get("GROUP_OPS_WEBHOOK_SECRET")
if not secret:
    raise SystemExit("GROUP_OPS_WEBHOOK_SECRET is required")
body = open(sys.argv[1], "rb").read()
signed = (sys.argv[2] + "\n" + sys.argv[3] + "\n").encode("utf-8") + body
print(hmac.new(secret.encode("utf-8"), signed, hashlib.sha256).hexdigest())
PY
)"

curl --fail-with-body -X POST \
  'https://www.youcangogogo.com/api/automation/group-ops/webhooks/groupops-46d055d8-0276-449c-a2b5-170af39c3c92' \
  -H 'Content-Type: application/json' \
  -H 'X-AICRM-Client-Id: aicrm-webhook-group-ops' \
  -H "X-AICRM-Timestamp: $TIMESTAMP" \
  -H "X-AICRM-Event-Id: $EVENT_ID" \
  -H "X-AICRM-Signature: sha256=$SIGNATURE_HEX" \
  --data-binary "@$BODY_FILE"
```

时间戳允许过去五分钟、未来一分钟的窗口，JSON 上限为 64 KiB。

## 返回和重试

非 202 的 Webhook 响应使用专用的安全错误信封：

```json
{
  "ok": false,
  "error": {"code": "provider_disabled"},
  "provider_execution_eligible": false,
  "real_external_call_executed": false
}
```

| HTTP | 代码 | 调用方动作 |
| --- | --- | --- |
| 202 | 成功 run 摘要 | 保存 `run.run_id`；动态执行的 `node_id` 会显示为字符串 `"0"`，表示数据库中无预设节点。|
| 400 | `invalid_request` | 修正 JSON、引用、目标或顺序；使用新 event ID。|
| 401 | `protocol_authentication_failed` | 修正时间戳、客户端 ID、事件 ID 或签名；不要泄露密钥。|
| 404 | `plan_not_found` | URL 的 Webhook reference 未解析到计划；核对计划描述符，不能用群名称代替 reference。|
| 409 | `idempotency_conflict` / `operations_conflict` | 前者表示同事件不一致；后者表示计划未启用、配置不完整或目标不属于该计划。不要自动改 event ID 重发。|
| 400 | `miniprogram_cover_unsupported` | AppID/path 不属于日课自动封面契约。上传封面并创建本地小程序素材后，用新的 event ID 和 miniprogram_id 重试。|
| 503 | `miniprogram_cover_unavailable` | 日课契约已匹配，但固定封面来源暂时不可用或未通过 PNG 校验。用同一事件和完全相同 JSON 重放；不会创建 run 或发送意图。|
| 503 | `provider_disabled` | 运行时尚未允许接受新的 EER 意图；本次生产排查时此开关处于关闭状态。不会调用小程序解析，也不能将它当成已接受或已群发。恢复后只能用原事件和完全相同 JSON 重试。|
| 503 | `protocol_auth_unavailable` / `group_ops_unavailable` | 签名验收依赖或 Group Ops 运行时不可用；不要把它当成已接受或已群发。恢复后只能用原事件和完全相同 JSON 重试。|

202 只证明本地 run/EER 意图已原子接受。企微素材准备、实际 Provider 调用和群内送达是后续独立状态，不能由 202 推断为已群发。
