# C0 渠道码中心 donor 契约审计

## 冻结基线

- v3 基线：`origin/main@bd401bcc337a70f95f2affa172af36e5a6fa303d`。
- donor：`qianlan33333-png/AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`，仅作只读行为、测试和叶子协议供体。
- v3 不依赖 donor 的 Go module、数据库、migration、运行时服务或远程 API。
- 本契约只证明供体页面和行为已冻结；不代表 Channel PostgreSQL、Provider 执行或生产历史导入已经完成。

## 开发前最高优先级分类

```text
OneID: 涉及。渠道归因只接收回调边界计算的 State 摘要；外部身份只经 internal/identity/port 解析，只有 provider-verified not-found 才能显式建客。
Persistence: 涉及本地事务、内部持久任务、Provider read 和 Provider write。Channel 状态/收据/审计/Outbox/effect acceptance 必须同一 PostgreSQL UoW；持久任务复用 River/jobqueue。
External Effects: 涉及。所有企微业务写由 outbound 持有，Channel 只冻结配置并保存 opaque effect_id；Provider 默认 disabled。
```

Channel 是渠道定义、不可变配置版本、客服分配、资产业务引用和归因收据的 Owner。Identity 是外部身份与 canonical Customer 归属的 Owner；WeCom 是 callback inbox、relationship 与 Welcome grant 的 Owner；Outbound 是企微业务写和 effect envelope 的 Owner；External Effects 是效果状态机的 Owner；Media、Tag 分别拥有素材和标签目录。

## 前端冻结结论

- `channels.html` 和 `channelForm.html` 是活跃页面，必须与 donor 逐字节一致。
- 渠道页面没有独立 CSS 文件；渠道视觉规则以内联样式存在于两张模板中，因此模板字节门禁同时冻结渠道专属 CSS。`tokens.css` 与 `labs.css` 仅作为共享样式证据记录，不增加第二份渠道样式 Owner。
- `controller.ts`、`admin.ts`、`wecomAcquisitionLinks.ts`、导航、注册表及类型是交互/Adapter 参考证据。它们为共享文件，不由本模块额外锁死；后续只允许通过 v3 Adapter 接真实 API，不得把 donor 后端带入运行时。
- 页面必须继续区分 `accepted`、`queued`、`attempted`、`executed`、`outcome_unknown`、`final_failed`、`reconciled`。只有具备受控结果的 `executed` 或 applied reconciliation 可打开、复制或下载。

## 后端分类

| donor 能力 | v3 处理 |
|---|---|
| 渠道列表、详情、编辑、客服分配 | 保留用户行为，`internal/channel` 重写 |
| 二维码、获客链接、欢迎语、入渠标签 | 保留合同，经 Channel Port、Outbound、External Effects 重写 |
| State 与扫码归因 | 复用 v3 已有 HMAC 摘要和 OneID；不复制匹配器 |
| 历史渠道、客服、联系人和效果事实 | 一次性只读快照导入；不可执行 |
| donor migration、store、queue、worker、Provider client | 丢弃 |
| donor customer key、raw external identity、自动合并 | 丢弃 |

## 完成边界

C0 的可验证完成证据仅包括固定 donor SHA、选定源文件 SHA-256、两张活跃模板逐字节一致、行为 Journey 和禁止 v2 runtime import 的自动检查。渠道 CRUD、资产发布、扫码归因和历史导入在后续交付完成前均不得标记为生产可用。
