# 渠道码中心 V1 语义审计

行为基线：`AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`。本审计只冻结可观察语义，不复制 Python 运行时、数据库访问、Provider 调用或身份匹配代码。

| V1 事实 | v3 归属与实现 |
|---|---|
| `automation_channel_contact` 每渠道唯一联系人及 `enter_count` | Channel 聚合历史与实时事实；canonical customer 去重，unresolved source contact 仍计数 |
| active QR 按渠道读取，历史 scene alias 继续识别 | Channel legacy asset 投影；Provider readback 后才成为可下载、可归因资产；State 只保存 corp-scoped HMAC 摘要 |
| owner 或 1–5 名 assignee、ratio/cap-switch | Channel 不可变配置版本；WeCom UserID 映射本地 active `admin_users`，读取可降级、保存和发布 fail closed |
| welcome text、自动通过、图片、小程序、附件、群邀请 | Channel 保存稳定 Media ID；Media owner 导入并校验实际引用的内容，不复用临时 provider media ID |
| entry tag 与名称快照 | Tag owner 的 provider binding 是启用条件；删除或未核验标签只保留历史名称并阻塞 |
| 扫码回调后分配、欢迎与标签 | OneID Resolve/显式 Provision 边界保持不变；Channel 接受意图，Outbound 独占企微写，External Effects 记录结果 |

## 兼容规则

- 列表、抽屉和编辑页读取同一聚合及资产投影，不能各自伪造 `0`、空 URL 或不同状态。
- 旧码验证失败不冒充成功；新码执行成功前不退役旧码。
- `accepted/queued/attempted` 不是二维码生成成功。前端有限退避轮询，只有 `executed/reconciled/legacy_verified_active` 暴露 same-origin 下载。
- 历史导入不创建 Provider 效果、不调用 Provider、不创建或合并客户；后续 Provider readback 和灰度写是独立阶段。
