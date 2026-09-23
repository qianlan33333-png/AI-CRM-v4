# 侧边栏客户 360 业务动态修复

## 业务判断与边界
OneID：读取已授权的 canonical customers.id，沿用侧边栏员工/客户上下文校验；不新增身份匹配、建客或合并。
Persistence：只读已有持久业务事实，领域 Owner 通过稳定 Port 提供有界分页；不新增表、后台作业或历史重写。
External Effects：不涉及 Provider 读写，不恢复已暂停的备注全量补打。

## 已确认问题
侧边栏 composition 将 timeline 绑定到 customer_timeline_projection，只存在资料同步与泛化问卷事件。问卷名称未展示；订单、雷达、渠道没有接入，资料同步占满分页。这不是业务事实不存在的证据。

## 参考
已检索 GitHub frappe/crm，阅读 frontend/src/composables/useTimelinePreferences.js（https://github.com/frappe/crm/blob/e4bd5d99b4851b7b80992524f52cea77cbaf7f5c/frontend/src/composables/useTimelinePreferences.js）：统一时间展示与最新优先排序。实现优先复用本仓 open_platform_activity.go 的领域分页读取与汇总模式，不引入外部依赖。

## 需求与行为合同
- 显示已提交的具体问卷名称及提交时间。
- 显示已归属当前客户的渠道进入事实及渠道名称；企微回调仅证明通过渠道添加/进入，不虚构每次扫码；历史导入以历史渠道进入摘要展示。
- 显示具体商品名称与真实订单状态，受益人与付款人关系不混淆。订单活动采用既有创建时间，文字明确为创建订单及当前状态，不冒充付款发生时间。
- 显示已可信归属当前客户的雷达访问/阅读阶段及雷达名称；匿名、pending/conflict不关联客户。
- 资料同步记录不进入业务动态；原审计/投影数据保留。
- 读取现有业务事实，历史数据立即可见，无需重放提交、支付、扫码或企微写入。
- 四个来源倒序合并，固定水位及确定性跨来源ID排序；分页不重复、不遗漏，同时间同原始ID也可区分；升级后的游标与旧投影游标隔离。
- 任一来源失败返回明确不可用，不伪装为空记录。名称缺失显示可理解的对象类型与内部业务编号；文本作为纯文本渲染。

## 实现与验收
复用 Survey.CustomerHistoryWindow、Order.CustomerActivities、Radar.CustomerActivities；在 Owner 内补充商品/雷达名称，Channel增加有界客户活动Port（含原生与已归属历史事实）。Composition聚合，沿用侧边栏现有timeline DTO及列表展示。
验证：四类命名内容、噪声排除、身份隔离、跨源同时间分页、来源故障、匿名排除、历史渠道去重/人工归属、待支付不显示已购买、现有权限游标及真实PostgreSQL旅程。按 fast -> compile -> 专项 -> 完整CI验证，不将本地测试称为已上线。

## CI 总时限修正
run 35170713435 的 backend 在 `cmd/aicrm` 包累计 600.071 秒触发 Go 默认 10 分钟包级时限。当时 `TestTagCatalogDispatchPostgreSQLRecoveryAndProviderOutsideTransaction` 仅运行 1 秒，栈位于正常建库迁移；其独立 PostgreSQL 16 race 复跑通过，无断言失败或竞态报告。为容纳该包约 295 个用例的累计成本，canonical backend 命令显式设置每包 `-timeout=15m`，保留串行、race、count=1、全包集合及工作流 25 分钟上限。此为无状态验证配置，不涉及 OneID、持久化或 Provider 效果。已检查并行任务，未见其他任务修改此命令。修复后复跑完整后端阶段与当前 HEAD 的完整 CI。
