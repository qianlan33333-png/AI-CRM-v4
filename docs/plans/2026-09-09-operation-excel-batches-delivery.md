# 运营闭环 Excel 批次：本地交付记录

本轮范围是 PRD、子代理开发与本地验收。未推送、合并、部署、执行生产迁移、恢复生产组件或真实发送。代码位于 `codex/operation-batch-consolidation` 分支的工作区。

## 文档

- [详细 PRD](2026-09-09-operation-excel-batches-prd.md)
- [接口契约](2026-09-09-operation-excel-batches-api.md)

## 分工与复用

主代理负责需求、接口协调和交付审查，业务代码由子代理完成：后端 terra xhigh；前端 terra high；Python 组件 luna max；事务与权限专项复核使用 sol medium。

供体 `qianlan33333-png/AI-CRM` 核对版本为 `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`。复用其长期计划列表、详情左右分区及审核交互行为。它的 Python 业务后端没有整包搬入 V3，也没有成为运行依赖。

直接复用 V3 已有长期计划、审核、Identity Port、Outbound、External Effects、持久任务和观察组件；新增必要的计划与批次关联、不可变内容版本、草稿替换、Excel 可选分层及同页操作适配。没有另建发送系统或身份匹配系统。

## 实现范围

- 运营闭环计划列表进入详情；详情仅内容准备与发送、发送效果与复盘两个维度，支持新批次和历史切换。
- 页面/API 指定长期计划上传；五列必需、分层可选，标题为空保留执行失败语义；批次封面必需。
- 草稿替换、内容修改、排除和封面修改参与版本及重新审核；提交后冻结，旧版本可查。
- Excel 审核与批准沿用既有事务、权限及幂等逻辑；发送身份延后解析；回执状态区别于任务创建。
- 总体与可选分层的 12/24/48 小时报告；缺失数据不按零处理；前端完整读取分页。
- 已关联 Excel 不在 AI 助手重复展示；其他来源审核保留；旧未关联批次必须显式关联。
- `0124` 保留 AI Assistant-owned、只处理非关联 legacy import 元数据的兼容触发器。它让发布失败后的旧二进制三列导入写入得到创建时间、版本、初始封面和行 revision；旧二进制随后切换当前内容（包括 A→B→A 封面回退）时只追加不可变封面事件。全部审批、Outbox、External Effects 与 Provider 状态均不在该触发器范围内。待所有保留回滚 release 都升级到 0124 且旧导入端点退休后删除。

## 验证状态

以下为子代理在最终源码上执行并回报的结果：

| 检查 | 结果 |
| --- | --- |
| 组件目录 `python3 -m unittest test_batches.py` | 12/12 通过，含组件 HTTP 协议 |
| `node scripts/excel-batches-dom-test.mjs` | 通过，含完整分页、异步写锁、旧批次晚到结果隔离 |
| `npx tsc -p web/tsconfig.json --noEmit` | 通过 |
| `go test ./internal/aiassistant/... ./internal/operationcycle/...` | 通过 |
| 隔离 PostgreSQL 下的两个 Host journey（下述命令） | 通过，10.106 秒；强制 Chromium，无缺失浏览器跳过 |
| `python3 scripts/dev_preflight.py fast` | 通过 |
| `python3 scripts/dev_preflight.py compile` | 通过，全仓 `go test -p 1 -run '^$' ./...` 编译检查 |
| `git diff --check` | 通过 |

Host 专项使用新建的本地隔离数据库、真实 HTTPS、管理员会话、CSRF、PostgreSQL、最终构建的前端及 Chromium：

```sh
AICRM_DATABASE_URL='<本地隔离测试数据库连接>' \
AICRM_REQUIRE_CHROMIUM_JOURNEY=1 \
go test ./cmd/aicrm \
  -run 'TestPostgreSQLExcelHTTPCompositionJourney|TestPostgreSQLExcelBatchesChromiumJourney' \
  -count=1
```

浏览器覆盖计划→详情→新批次→封面→编辑→排除→提交→刷新，并检查数据库中的一个待执行意图及一个排除行。HTTP 分支另覆盖同键重试、请求漂移、重复文件与明确新批次、历史/替换、提交冻结、通用写入口阻断、话术改回历史内容及重复封面。测试数据库用后已删除。

联调修复包括：纯排除/分层重复内容约束冲突、历史内容回退、操作未完成时误提交、旧批次响应覆盖新批次、历史封面记录和分页版本一致性。权限专项同时检查了旧通用审核入口不能绕过本场景版本及冻结规则。

## 未验证边界

Host journey 使用确定性的本地组件协议 fixture，没有在同一行程启动真实 Python 服务；组件自身的 12 项测试另行通过。全仓 compile 是编译检查，不是全仓业务测试。

未调用真实 WeCom/Provider；本轮验证发送意图及回执投影，复用既有 External Effects 的重启恢复和未知结果不换键逻辑，未新增一次真实 Provider 故障演练。以上不证明真实企微执行、生产回执权限或生产打开日志覆盖。真实发送、生产数据源完整性和线上端到端验收仍需后续明确安排。

## 生产交付补录（2026-09-10）

- PR #223 合并为 `3ddf7f54cf8208a557ca2181bdc7f166c30b2a9f`；PR 的 PostgreSQL race、前端、Chromium、Python 组件、归档 SDK 与汇总检查全部通过。
- 正式安装器完成迁移、服务切换及精确 `/readyz` 校验；本地 SSH 回传同一份清单校验通过的正式内容后，以运行序号 `1239` 复核，安装器按幂等保护拒绝重复切换。
- `aicrm.service`、`aicrm-effects-worker.service`、`aicrm-excel-batches.service` 正常运行，相关 timer 已启用；上一版 release 目录仍保留。安装器没有创建持久 `rollback` 符号链接，仅在安装失败期间使用已读取的 previous 路径回切。
- 通过受保护管理员会话、CSRF 与正式 API 创建独立验收计划和一个带封面的待审核合成批次。Headless Chromium 只读验收确认批次预览、两个维度、12/24/48 小时窗口和逐人回执均可呈现；没有批准、提交或真实发送。
- 线上验收发现详情标题退化为内部 `strategy_key`。后续最小修复从页面已加载的长期计划条目读取标题，并保留批次接口返回字段的覆盖能力；专项浏览器回归要求详情标题显示长期计划名称。
