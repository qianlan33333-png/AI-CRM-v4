# 问卷单次提交与完成页

## 业务决策

- 同一份问卷，同一个已通过 Survey OAuth 与 OneID 解析的 canonical `customers.id`，从本能力上线后只允许成功提交一次。
- 刷新、换浏览器、换会话或换 `submission_key` 都不能获得第二次提交机会；相同 key 的既有幂等重放与 payload conflict 语义保持不变。
- 完成动作优先级为：已启用且可安全解析的导航目标、已配置且可读取的渠道二维码、默认结束页。解析失败一律降级默认结束页，不回到答题页，也不使用请求参数形成跳转。
- 历史答卷不回填、不删除、不合并；一次提交资格只从本迁移上线后的新提交开始记录。

参考行为来自 `qianlan33333-png/AI-CRM` 当前 main 的已提交状态、入口重定向和事务内重复提交防护；v3 只复用行为合同，不复制旧仓身份表、UnionID 主键或 Python 实现。

## 架构分类

```text
OneID: reads canonical customer
Persistence: local transaction; an already-configured completion push remains a Provider write/external effect
```

- Survey 是提交资格、答卷和完成配置的唯一 Owner。HTTP 只接受 Survey OAuth 会话已经解析出的 canonical Customer，不解析、创建、关联或合并身份。
- 新提交资格、答卷、答案、结果 token、审计、Outbox 和既有 completion effect 接受必须在同一个 PostgreSQL Unit of Work 中提交或回滚。
- 完成页导航使用独立的 composition-owned 安全目标解析器；不得把 outbound completion Provider endpoint、签名信息或任意 URL 暴露给浏览器。
- 导航白名单只由 `AICRM_SURVEY_COMPLETION_NAVIGATION_TARGETS_JSON` 配置；它与 External Effects 的 `AICRM_SURVEY_COMPLETION_TARGETS_JSON` 完全分离。
- 渠道二维码只经 `internal/channel/port.PublicLeadQRCodeReader` 读取。Survey 不访问 Channel 表，也不触发二维码创建、刷新或 Provider 调用。
- 不新增队列、Worker、身份匹配器、Provider writer 或重试/对账状态机。

## HTTP 与页面合同

公共完成动作固定为：

```json
{"type":"default"}
{"type":"redirect","redirect_url":"https://approved.example/path"}
{"type":"lead_qr","lead_qr":{"url":"https://approved.example/qr.png"}}
```

- `/q/{slug}` 在可信会话解析后先读取新提交资格；已提交时直接 `303` 到安全跳转或 `/h5/done.html?slug=...`，未提交才进入 `all` / `one`。
- `/api/h5/surveys/session` 返回 `submitted` 和 `completion_action`，不返回 Customer ID、submission ID、result token 或外部身份。
- 已提交用户直接读取公开定义或再次 POST 时返回稳定的 `409 already_submitted` 与同一 `completion_action`。
- 首次 POST 保持 `201` 和原结果凭据兼容，同时返回 `completion_action`。前端对首次与重复状态均使用 `location.replace`。
- 默认结束页唯一可见业务文案为“收到你的问卷”；不显示导航、标题、时间、编号、版本、结果入口、返回按钮或内部效果说明。
- `lead_qr` 完成页只增加已配置二维码及必要扫码说明；普通完成流程不再链接结果页，既有结果查询 API 保持兼容。

## 前端复用与 Product Design

| 参考页面 | 复用组件 | 公共扩展 | 受影响调用 | Product Design |
| --- | --- | --- | --- | --- |
| 公共问卷 H5 | `surveyPublicHost.ts`、`surveyPublic.css`、共享 visual tokens | 将既有 `done.html` carrier 升级为真实完成态 | `/q/{slug}`、auth/all/one/done | 本会话 catalog 无 `product-design:*`，未执行或冒充插件；沿用现有浅灰画布、白卡与蓝色 token |

冻结 donor 不修改；Host 继续只负责呈现和安全导航，提交资格与完成动作事实均由 Survey Owner 返回。

## 验收

- 串行与并发的同 Customer/同问卷/不同 key 只有一份新答卷、结果 token、审计、Outbox 和 completion effect；不同 Customer 可各提交一次。
- 同 key 同 payload 重放原回执；同 key payload 漂移冲突；事务失败后资格不残留。
- `/q`、session、公开定义和提交接口覆盖默认、跳转、二维码、身份缺失/冲突和 `already_submitted`。
- 失效导航目标或二维码读取失败安全降级默认结束页，不产生开放跳转或跨领域查询。
- Chromium 在 375/390/430 覆盖首次提交、刷新、重新打开、后退、直接访问 all/one、默认结束页、二维码和跳转。
- 发布证据分别记录 commit/tree、定向测试、full/CI、部署、OAuth 与真实提交 readback；局部编译或截图不等于上线完成。
