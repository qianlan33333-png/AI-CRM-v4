# Behavior Contract：客户档案

## 冻结输入

- 供体 SHA：`dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`
- 供体客户档案：服务端基础档案 + 前端并行标签、问卷、消息。
- 供体用户运营详情：客户摘要 + 最近 20 条时间线。

## v3 必须保留

1. 列表进入独立详情页。
2. 基础档案先可见，扩展分区并行且互不阻塞。
3. 分区有 loading、empty、not_ready、error、retry 状态。
4. 问卷显示题目、选择项文本和安全结果摘要。
5. 时间线默认 20 条并支持继续加载。
6. 一级页必须保留供体“客户查找 + 客户列表”双卡片、横向筛选条和紧凑数据表格。
7. 二级页必须保留供体 module banner、五列 profile grid、左主栏/右侧栏及消息气泡列表，不得降级为简易单列详情。

## v3 必须改变

1. 路由与接口只使用数字 customer_id。
2. identity 先解析 canonical root，绝不按手机号或外部 ID 兜底。
3. 默认手机号脱敏，查询为 CSRF + RBAC + audit + no-store 的临时动作。
4. 标签是“最近同步”，详情读取零 Provider 调用。
5. 聊天只允许活动元数据，不允许正文或参与者 Provider ID。
6. 未接入返回 `capability_not_ready`，运行故障返回 `section_unavailable`。

## Characterization/Journey 断言

- `/admin/customers/42` 页面不包含 external_userid、UnionID、`+86` 示例、assurance 文案或聊天正文容器。
- 页面脚本只对 `/api/admin/customers/42/{section}` 发起同源 no-store GET。
- owners/tags/surveys/timeline 任一请求失败不改变基础档案和其他分区。
- 成员无映射时只显示数量；标签无目录名称时只显示“标签名称待同步”。
- 明文手机号不写 URL、storage、日志或持久 DOM 状态，30 秒后清空。
- Chat disabled 时响应 503，页面文案为“能力待接入”，而不是“暂无聊天”。
