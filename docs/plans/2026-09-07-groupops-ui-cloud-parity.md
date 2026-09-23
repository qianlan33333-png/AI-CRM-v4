# 群运营标准版 UI 与企微目录读取修复

OneID: 不涉及客户解析或建客；群负责人复用 Access 员工 Port 中的唯一企微 userid 映射。
Persistence: Provider read + 本地 PostgreSQL 事务。所有 Provider 分页成功后才替换目录，目录、收据、审计沿用现有同一 UoW。
External Effects: 沿用既有群运营 Provider write 意图、`outbound` 与 External Effects 接纳/恢复合同；本轮不新增效果类型、队列、Worker 或重试内核。节点时刻和 disabled 状态只改变既有冻结意图的生成条件，幂等键与 unknown 原键恢复不变；目录读取使用独立开关，不能隐含启用派发。

## 已核验根因

生产 cc4bb8c，企微配置存在，群运营 Provider 开关关闭，读取和群发共用开关。真实 gettoken、follow_user_list、groupchat/list、groupchat/get 均 errcode=0；目标运营成员在云端授权列表中，本地映射唯一；本地群目录 0 条。

## 行为合同

以用户图一图二和冻结 AI-CRM 供体 `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f` 的 `group_ops.css`（SHA-256 `4231be50170c77c063d31552dc09aec1586d5cc94df86a3bed30643dc2037c10`）、`group_ops.js`（SHA-256 `3733af636a0f8c13b3a9dd42aff2eef804d18649e957aa894b2bfb296852beac`）和 `operation_member_picker.js`（SHA-256 `bd84ce78ccb834f170548dea76cb99f6434978bc21211a9ec843dd2bf7ebabea`）为视觉和交互来源；保留四个列表统计、七列计划表、四个详情统计及基础配置/绑定群/Webhook/标准编排四个切换维度。业务值来自 V3，不硬编码标准版截图中的历史数值。冻结供体不修改，使用 V3 Host。

读取开关默认关闭，可独立开启；错误分页不替换旧目录；用户点击刷新后必须报告真实同步结果；不以本地重读作为云端读取成功。

## 字段与运行语义映射

| 标准字段或动作 | V3 事实 / Host 适配 |
| --- | --- |
| 页面结构、样式、操作顺序 | 标准供体 `group_ops.{js,css}`（记录的 SHA-256）直接携带到 `web/v3/groupOpsStandard.{js,css}`；仅无来源数字的格式化改为 `—`。 |
| `plan_name`、`plan_type`、`status` | `group_ops_plans.name/plan_type/status`；V3 `paused` 投影为供体的 `disabled`。 |
| `owner_userid` | `group_ops_plan_members.staff_id` 的既有员工记录；不创建客户或员工。 |
| `bound_group_count` | `group_ops_plan_group_assets` 的精确行数。 |
| `external_member_count` | 仅所有绑定群都有 Provider 返回的 `external_member_count` 时聚合；任一未知显示 `—`。 |
| `today_estimated_reach` | V3 没有可信预测来源，固定投影为 `—`；不以绑定数或群成员数冒充。 |
| `day_index`、`scheduled_time`、`trigger_time_label` | `group_ops_plan_nodes` 持久字段和显式 `schedule_semantics`；新建节点为 `calendar`，运行以接受 run 的上海日期为 Day 1 锚点生成 `scheduled_for`。迁移前的每行明确标为 `relative_delay` 并保持原有 delay；不按 Day 1/20:00/空标题等业务字段猜来源。V3 未保存群入群事件，绝不猜测该锚点。 |
| `action_title`、`status` | `action_title`、`node_status`；`draft` 与 `disabled` 不生成执行草稿。 |
| `queue_count` | 只数现存 `accepted`、`provider_accepted`、`outcome_unknown` 执行事实，不等同实际送达。 |

## 部署与回滚顺序

0101 引入的日历字段只由本版本运行时解释；旧二进制不会读取 schedule_semantics，不能把直接回退旧二进制视为语义兼容。回滚前先在 Runtime Settings 关闭 groupops.dispatch_enabled，并停用本板块新运行受理；保持目录读取关闭或按故障范围单独关闭 groupops.directory_read_enabled。确认没有本版本新接受的待执行 Provider 写后，再切换二进制。已冻结的执行、效果、回执和 outcome_unknown 事实保留，不删除、不重建幂等键；恢复时只按原键查询、回调或既有对账流程继续。恢复到本版本后，先核对版本与两个开关，再按计划重开受理；本轮上线前两项 Provider 写均保持关闭。

供体 JS 仅有两处事实投影修正：没有可信今日触达来源时，列表总计也显示 `—`；成员统计只在总人数与外部联系人数均可信时派生内部人数。其余标准 DOM、样式和交互代码按记录的冻结来源携带。
