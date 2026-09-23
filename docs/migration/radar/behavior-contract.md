# 内容雷达迁移 Behavior Contract

> Donor：`qianlan33333-png/AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`
>
> 性质：只读供体行为冻结。本文不授权 v2 运行时依赖，也不把供体占位能力认定为 v3 完成。

## 两轴结论

```text
OneID: involved only for authorized public visits; use scoped verified UnionID through Identity Port
Persistence: local PostgreSQL transaction + Provider read outside transaction
External Effects: not involved; no Provider write
```

## 不可变身份规则

雷达授权用户信息的最终可信值是经 Provider 验证、带 `wechat-open-platform:<platform-id>` scope 的 UnionID；UnionID 再由 Identity 领域与其他 ID 关联并解析为 `customers.id`。

- 缺 UnionID 时禁止 fallback 到 OpenID。
- HTTP 请求体不能自报 UnionID 或 verified。
- Radar 不实现 OpenID、external_userid、手机号的匹配或客户合并。
- 原始 UnionID 不进入 Radar 表、日志、管理 API、CSV 或浏览器状态。

## 管理端行为

### 列表

- 页面 key：`radar`；现有输出名/旧链接为 `radar.html`。
- 支持标题搜索、内容类型筛选、启用状态筛选。
- 行操作：详情、编辑、启用/停用、复制分享地址、显示/下载二维码。
- v2 真实能力：配置列表、启停、分享路径与 QR payload。
- v2 缺口：列表 DTO 把 PV、授权用户、查看次数固定为 0，`last_viewed_at` 使用更新时间代替。

### 新建/编辑

- 页面 key：`radarForm`；旧链接为 `radarForm.html?id={id}`。
- 内容类型：链接、图片、PDF。
- 字段：标题、目标地址、媒体引用、启用开关、授权开关。
- 图片/PDF 通过现有 Media 管理能力选择或上传。
- v3 保留 UI 行为，服务端补充显式 `content_type`、`auth_required`、版本/CAS 和校验。

### 详情、事件和导出

- 页面 key：`radarDetail`；旧链接为 `radarDetail.html?id={id}`。
- 展示访问量、授权用户、查看次数、授权转化率。
- 支持事件筛选和 CSV 下载。
- v2 真实能力：本地事件分页、阶段/时间筛选、统计和 50,000 行安全上限。
- v2 占位：事件不包含真实身份；CSV 头为 `unionid,external_userid,created_at`，前两列固定为空。
- v3 必须用真实事件口径和客户安全投影替换占位，不导出原始身份。

## API 叶子协议

| 能力 | v2 路径/方法 | v3 disposition |
|---|---|---|
| 列表/创建 | `GET/POST /api/admin/radar-links` | 保留兼容，canonical 合同由 v3 OpenAPI 拥有 |
| 新建选项 | `GET /api/admin/radar-links/new/options` | 保留 UI 所需投影 |
| 详情/编辑 | `GET/PATCH /api/admin/radar-links/{id}` | 增加版本/CAS 和真实内容类型 |
| 启停 | `POST .../{id}/enable|disable` | 保留行为，写命令必须幂等且原子 |
| 分享 | `GET .../{id}/share` | 保留 path/QR payload，不携带身份 |
| 统计 | `GET .../{id}/stats` | 用 v3 可证明事件重建 |
| 事件 | `GET .../{id}/events` | 增加归因状态和客户安全投影 |
| CSV | `GET .../{id}/events/export` | 兼容别名映射到 v3 安全导出 |
| 公开入口 | `GET /r/{code}` | link/image/pdf 完整 viewer 与授权分支 |
| 公共事件 | `POST /api/h5/radar-contents/{code}/events` | 兼容别名；只接受服务端 event token |

## 供体事件行为

v2 公开重定向在一个 UoW 中写 `landing` 和 `redirect`；公共事件接受以下八个历史阶段：

- `viewer_open`
- `image_loaded`
- `pdf_opened`
- `pdf_manifest_loaded`
- `pdf_page_loaded`
- `pdf_page_error`
- `image_manifest_loaded`
- `image_variant_loaded`

幂等键只保存 digest；重复相同 payload 返回原 receipt，不同 payload 冲突。事件表刻意不含 IP、UA、Referer、query、OpenID、UnionID、external_userid 或 customer_id。

v3 可以把历史阶段映射到新的规范阶段，但必须保持“同一 session + 版本 + 阶段不重复计数”和 PII 最小化；客户端不得自报身份。

## 状态与错误

| 条件 | 冻结/目标行为 |
|---|---|
| 未认证管理请求 | 使用 v3 统一登录/权限，不绕过 |
| 缺写权限或 CSRF | 403；不写业务状态 |
| 非法/非 canonical ID | 400 |
| 内容不存在 | 404 |
| 内容停用 | 公开访问 410（v3 收紧） |
| 幂等 payload 冲突 | 409 |
| 并发版本冲突 | 409（v3 新增明确 CAS） |
| Provider 缺 UnionID | 明确失败；不降级 OpenID |
| Identity pending/conflict | 不建客、不归属、不自动合并 |
| 媒体不可读 | 不计真实查看，显示可理解错误 |

## 统计定义

- PV：有效 `landing` 数。
- 授权用户：取得 scoped verified UnionID 后 distinct `identity_id` 数。
- 真实查看：link=`redirected`、image=`image_loaded`、pdf=`pdf_opened`。
- 转化率：授权用户/PV，PV=0 时为 0。
- v2 历史事件只进入 legacy 投影，不与 v3 实时指标相加。

## 前端冻结边界

以下 Radar 闭包由 `scripts/check-radar-donor-manifest.sh` 字节校验：

- `web/src/admin/sections/radar.ts`
- `web/src/admin/sections/qr.ts`
- `web/src/api/admin.ts`
- `web/src/api/generated/p4-radar/p4-radar.ts`
- 对应 controller、registry、nav、transport 和 shared API 类型/客户端。

新能力放在 v3-owned host Adapter、Radar 后端和公共 viewer，不直接修改以上供体业务文件。

## 完成证据

- 不能用菜单、空壳、HTTP 200、固定零值、Mock、PR 合并或任务排队证明完成。
- 必须同时证明管理旅程、三种公开内容、严格 UnionID、OneID 解析、真实指标、CSV、PG16 原子性、PII 无泄漏和 donor hash 未变。
