# 人群刷新计划与安静时段的上海时间语义

日期：2026-09-12

## 背景与问题

人群包详情页旧 controller 将“每日 2:00”提交为 `refresh_cron_utc: "0 2 * * *"`。Scheduler Owner 的既有合同不从该字段解释受控每日计划：`refresh_mode=daily_0200` 才推导 UTC `0 18 * * *`，即上海次日 02:00；所有非 `legacy_custom` 模式的 `refresh_cron_utc` 必须为空。因此旧写法会被服务端拒绝或造成错误语义。

只读生产核验显示：两个 active `daily_0200` 和四个 active `every_3m` 均没有 cron；三个 paused `legacy_custom` 保存 `0 1 * * *`，即上海 09:00。不存在需要迁移的错误 `0 2 * * *` 记录。另以 `quiet_hours` 聚合为空后复核 `automation_policy_versions` 总数为 0：当前生产没有已存安静时段策略版本，因此本项不会声称已统一迁移其他时区的历史策略。

## 决策

- OneID：不涉及。本项不读取、解析或变更客户身份。
- Persistence：已有 Segment / Automation 配置版本的本地事务。沿用现有配置版本、CAS、审计和 Idempotency-Key，不新增表或迁移。
- External Effects：不新增 Provider 写、Outbox、队列、Worker、重试或对账。新建策略仍走既有 Automation 合同；已有策略与已排队效果不重写、不重排。

## 用户可见行为

1. 新建或明确选择“每日 2:00”的人群配置写入 `refresh_mode: daily_0200` 与空 `refresh_cron_utc`。Scheduler 继续按其 Owner 合同在 UTC 18:00、上海 02:00 运行。
2. 保存筛选条件等其他变更时，未改动的既有人群计划完整保留原 `refresh_mode` 和 `refresh_cron_utc`；不把 `legacy_custom` 静默改为手动或每日计划。
3. `legacy_custom` 对可精确解释的固定日 cron 显示实际业务时刻，例如 `0 1 * * *` 显示“每日 09:00（历史自定义计划）”。不能把它称作“每日 2:00”。无法无歧义转成固定每日时刻的 cron 仍明确标为历史自定义规则，不伪造时间。两个受控计划选择都显示关闭时，页面同时说明原规则仍被保留，并提供“改为手动刷新”的明确动作；只有执行该动作后再保存，才写入 `manual` 和空 cron。
4. 新建策略固定以 `Asia/Shanghai` 语义提交安静时段，但普通界面不显示时区字段或后缀。已存策略、历史版本和已排队效果保持原值。

## 范围和非目标

仅修改人群详情的 source-owned Host / 模板和相应浏览器合同测试。不会修改 Scheduler SQL、生产数据、迁移、API 枚举、客户身份、Provider 行为或已存策略。

## 参考与取舍

- [PR #66](https://github.com/qianlan33333-png/AI-CRM-v3/pull/66) 已确认无时区来源必须显式按北京墙上时间解释；本项同样不依赖浏览器时区。
- [PR #258](https://github.com/qianlan33333-png/AI-CRM-v3/pull/258) 建立同源的上海时间 bridge 与 source-owned Audience Template Host；本项复用其 `refresh_mode` 写入，而不复制或改写冻结 controller。
- `internal/segment/store/schedule.go` 是当前 Scheduler Owner：它明确将 `daily_0200` 映射为 UTC `0 18 * * *`，因此前端不维护第二套 cron 映射。

## 验收

- `daily_0200`、`every_3m` 和组合模式各写其既有 `refresh_mode` 且 cron 为空。
- 保存 legacy custom 的其他字段不改变原 `refresh_mode` 或 cron；`0 1 * * *` 在页面显示上海 09:00。明确选择改为手动刷新并保存才写入 `manual` 与空 cron。
- 新建策略请求强制 `quiet_hours.timezone=Asia/Shanghai`；输入的时段保留为上海墙上时间。
- 真实 renderer/Chromium 验证详情页显示和提交，不以仅静态字符串断言替代。
