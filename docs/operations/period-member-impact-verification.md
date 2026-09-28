# 周期商品会员数影响检查：首个校准样本

样本：国内 `d0566b40a0330b12abb9cba390dc6f73319cd924` → `c510a95b6ca52801c9561a3463c3e68c4bbc1cf9`，head tree `a4cf8bf4fd57eebe2e264317974d2a8f09ce5481`。任何新候选都重新计算 base/head 图谱与补丁签名，不继承此处包数。

## 图谱和既有全量收据对照

- 两端 `go list -mod=readonly -deps -test -json ./...` 的图谱 fingerprint：`787a6ff73c94eed3f20a128dcbf35a3974b620b877ce04ae79e2a2f432ebbef7`；所选 **35 个包**，包含 Order、Product、Sidebar 及其测试导入反向闭包。无未归属 Go 文件。
- 已完成的准确 Linux 全量发布检查在主机 `/opt/aicrm/domestic/control/state.json` 中状态 `passed`，五 lane 均成功。backend 原始 JSON：`/opt/aicrm/domestic/build-worker/domestic-main-checks/c510a95b6ca52801c9561a3463c3e68c4bbc1cf9-attempt-1/backend/backend-go-test.jsonl`，SHA-256 `7b6cff1f627ce00ad9a8775ff495f98eb15a1fd541cd651bb95ed131ba242649`。
- 35 包中 32 包有成功终态，3 包是无测试包的 package skip；无失败。`cmd/aicrm` 自动发现 345 项顶层测试：310 项 backend 在上述日志中恰好运行并通过一次，34 项 Chromium 在 backend 正常跳过、在独立 browser lane 恰好运行并通过一次。browser JSON SHA-256：`fa4ea08db4cd9c92100280ce899901d42ab0e463617ada4a6c5fd4358b95f923`。剩余 1 项 `TestDomesticReleaseInstalledAlipayCheckout` 是安装器持私有输入执行的预发合同，不在普通 backend 环境执行。
- 自动发现器将 310 项 backend 测试按编译后的源文件映射为 12 个串行小组，每组至多 30 项（单文件超过限制时允许该文件独占一组）；清单覆盖检查要求每项恰好分配一次。小组运行后的 JSON 审计拒绝漏跑、重复、任意 skip、失败和意外测试。新小组执行的实际耗时及通过收据仍需在准确候选 Linux 检查时记录，不能拿旧单包运行冒充新分组执行。

## 规则边界

`period-member-read-v1` 接受本次审阅过的源和权威记录差异的规范化补丁摘要；后续 Order 的 `CountServicePeriodMembers` 只读函数及其测试变化可获得影子缩范围建议；函数外源码须逐字一致，静态拒绝 SQL 写入和外部效果调用，但新补丁在取得自己的全量对照前仍强制全量。它仍从候选准确 base/head 计算 Go 包全集；同时核对 OpenAPI、前端 DTO 与页面，并选择周期商品列表、会员数据及权益相关 Chromium 旅程。未知 diff、图谱缺失/错误、共享代码、迁移、权限或检查器变动全量。该规则的代码提交本身属于可执行检查策略变更，必须先通过全量检查；发布器只有在独立核准的 planner fingerprint 与环境配置匹配时才允许强制采用该缩范围规则。

## 发布后仍需独立读回

原业务包的预发/生产安装与服务健康收据已经完成；生产数据库只读聚合曾核得 `ces=93`（有效 87、过期 6）、`lianmeng=7`。发布后的**认证页面**列表数字、列名及权限尚无完整读回收据，不能由数据库聚合、静态页面测试或安装健康代替。下一次同类运行必须分别记录预发和生产的真实列表数字、会员数据总数、列名、无权限响应、服务及 `/readyz`，再作技术发布结论。
