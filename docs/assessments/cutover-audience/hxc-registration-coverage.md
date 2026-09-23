# 包28：黄小璨注册三态规则

OneID：Identity Owner 内沿用 Normalize、HXC Inspector 判定受信快照与 canonical customer；不建客、不合并、不复制 mobile hash 匹配器。持久化：HXC generation 原发布 UoW 原子保存三态覆盖表（0138）和原审计/运行状态；Segment只读Owner Port，复用已有刷新任务，无发送。

## 负面证据门禁

- HXC MySQL Provider 在 repeatable-read 全量分页和提交成功后才设置 Snapshot.Complete。
- Identity 正向 Inspector 唯一匹配为 registered。
- 完整来源中的合法NULL/空字段不产生比较键，不代表抓取缺失。源 phone 索引要求每个非空值可正常标准化且无重复；UnionID索引要求每个非空值具有同一可信开放平台scope且verified、无重复。任何源conflict/invalid阻止负面判断。
- 客户只允许当前 active、verified 比较键。存在 declared、重复同类键、不认识的scope、任一现有比较键对应源索引不完整或命中时，不能判 unregistered。
- 只有至少一个可信比较键，且所有现有phone/UnionID比较键的完整索引都明确无匹配，才为 unregistered。
- 缺客户、缺证据、旧代次无覆盖记录均为 unknown，不纳入未注册人群。

## 刷新与02:00计划

新模板 `hxc_registration`：要求 active 企微联系人，保留owner范围，registration_status为registered/unregistered。包28使用unregistered。负责人经现有Access reference解析，不回填源员工ID。

部署0138和代码后，需要通过正常 `POST /api/admin/hxc-dashboard/refreshes`（管理员/CSRF）发起一次完整HXC刷新并等待其原运行回执成功；原timer亦走同一刷新路径。只显示任务accepted不代表证据已发布。

规则读取 `projection_as_of <= reference_time` 的最新历史代次，防止02:00任务稍晚执行时被新代次覆盖。两小时freshness适配每小时HXC刷新。过期/无代次会使刷新失败而保留原人群，不把失败当空集；unknown排除。跨批出现不同代次也失败关闭。

启用只开启人群计算，未恢复源发送或任何Provider效果。生产应用由主线程统一进行。

## 验证

真实PostgreSQL：完整双索引negative、非完整snapshot、单索引缺失、declared键、重复源索引；HXC代次发布原子回滚及缺行unknown。

Segment回归：unknown排除、active联系人、stale generation拒绝。Identity/HXC/Segment领域测试和架构检查通过。
