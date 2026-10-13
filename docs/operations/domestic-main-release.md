# CRM V4 极简国内发布

依据 [已批准的优化方案](../prd/2026-10-14-development-release-simplification.md)。普通发布由本地 AI 选择改动包及直接业务消费者，不递归证明整个依赖闭包，不因未知、共享文件或规则改动自动全量。

## 四步

1. AI 读实际 diff、直接调用点和字段使用者，给出短清单。
2. 运行选中检查；纯单元使用原生缓存，相关环境检查只执行一次。
3. 完成的成员可先安装预发；每个新构件构建、安装各一次，完成新增影响的相关合同。
4. 已有当前范围的部署授权，内部绑定候选/构件/收据，直接同包生产并独立读回。

```mermaid
flowchart LR
 A[AI 短清单] --> B[相关检查与源码累计]
 B --> C[最终 Linux 构建一次]
 C --> D[预发相关冒烟]
 D --> E[已有授权 同包生产]
 E --> F[版本 服务 相关认证读取]
```

## AI 清单

候选在 `docs/release-checks.json` 中保存清单；发布器从准确候选 Git tree 读取，与该源码一起交付。`baseline_sha` 是当前累计源码 base；每次候选更新清单。

```json
{
  "baseline_sha": "<准确base SHA>",
  "checks": [
    {"lane": "backend", "package": "./internal/product/app"},
    {"lane": "backend", "path": "internal/payment/app/service_test.go", "test": "TestExample"},
    {"lane": "frontend", "path": "web/v3/productAdapter.periodic_contact_collection.test.mjs"}
  ],
  "installed_checks": ["periodic-contact"]
}
```

示例路径与用例以当前候选真实文件为准。小包整包测，大包用 `test` 选择具名用例。`installed_checks` 是 `docs/operations/release-contracts.json` 的注册合同名称。新批次只选自上次预发后新增成员的合同及其直接消费者；旧批次保持原收据语义。相关并发检查显式加 `race: true`，PostgreSQL 用例显式加 `postgres: true`。需要额外维护程序时用 `build_commands: ["./cmd/<已有发布程序>"]`。

清单只预检路径、语法和用例存在，拒绝空匹配、skip 和失败。无清单时自动找改动包附近合同；找不到就由 AI 补清单，禁止转全仓。全量只通过明确的维护命令 `dev_preflight.py full` 执行。

```sh
python3 scripts/dev_preflight.py affected --base <base> --head <head> --dry-run
python3 scripts/dev_preflight.py affected --base <base> --head <head> --report-dir <仓外结果目录>
```

## 批次先累计源码

国内裸仓库 `main` 是权威源码，GitHub 人工归档。沿用原队列、锁、attempt 和安装器；预备机保持 2 核 2GB。`release` 检查候选后将运行时成员纳入现有批次，状态为 `prepared`，返回累计 `source_head_sha`；此时应用尚未更新。下一成员从该源码 HEAD 出发。

成员完成就可运行 `batch-stage`，无需等尚未完成的开发；新构件一次构建、一次安装，停在 `stage_validation_pending`。原控制器 `stage-check` 执行注册合同，隔离 PG/Chromium、记录具名 JSON 终态和实际应用/Host 摘要。`poll` 接受结果；先前合同保持原源码/构件身份，下次只检查新增成员及 AI 明确选择的直接消费者。用户要求集中上线时保留预发直到预期成员齐全；已有范围授权用 `promote-authorized` 同包生产，不重新构建或询问。

```sh
sudo /usr/local/libexec/aicrm/domestic_main_release.py release --config /etc/aicrm/domestic-main-release.json --ref refs/heads/codex/<work-item> --head <head> --base <base>
sudo /usr/local/libexec/aicrm/domestic_main_release.py batch-stage --config /etc/aicrm/domestic-main-release.json
sudo /usr/local/libexec/aicrm/domestic_main_release.py stage-check --config /etc/aicrm/domestic-main-release.json
sudo /usr/local/libexec/aicrm/domestic_main_release.py poll --config /etc/aicrm/domestic-main-release.json
sudo /usr/local/libexec/aicrm/domestic_main_release.py promote-authorized --config /etc/aicrm/domestic-main-release.json --head <最终head> --authorization-reference <用户原命令引用>
```

测试、文档、无应用载荷的工具变动只推进源码，不重建重装应用。固定控制器变动使用原 `maintenance-check`：从准确候选控制器执行选中合同，通过后在原锁内安装准确工具字节并保留回退，再释放同一源码候选。维护结果用于同候选继续，不重复执行相同检查。

已安装验收直接使用安装二进制和页面资源；常驻 Go/npm 缓存，依赖按锁文件变化准备。Go 与 Chromium 在小机器上错开执行。业务断言全部完成且进程停止后的临时目录删除失败记警告；身份、数据库归属、进程状态和业务结果不明仍停止。

## 每次发布自动清理

持原串行锁，在相关检查开始前清理已结束 attempt 的过程工作区、preparation snapshots 和旧续跑 checkpoint；检查收尾清理当前临时生成物。源码检查不恢复不明的过程 checkpoint，准确候选完整通过的维护收据继续复用；安装合同的独立具名终态可保留原安装收据/二进制/测试源码绑定，失败只重跑失败合同及已改变的 fixture。生产完成或原对账完成后，回收与健康安装收据及完整构件 inventory 一致的重复 build 副本。

保留有效 Go/npm 编译及依赖缓存、配置、业务数据库、必要诊断/收据/迁移备份、当前包和最近一份完整成功包及活跃引用。缺两份兼容回滚不阻止删除其他已验证无引用包；不同 schema 的前驱包是恢复材料，不是自动回滚许可。未知目录/进程/数据库身份先停止核对；已知临时文件删除失败记 warning，不改写业务验收或安装结果、不自动重装。清理直接嵌入原发布生命周期，无新增 timer 或发布通道。具体合同见 [自动清理PRD](../prd/2026-10-10-release-process-cleanup.md)。

## 失败、恢复与读回

检查失败停止后续发布写操作，修复后只重跑失败用例及修复影响范围。仅准确候选完整通过的检查收据可复用；不明的过程续跑缓存不用作通过证据。固定安装 runner 的逐合同通过日志原样保留；没有整体通过收据的阶段仍未完成。修复候选重新按更新后的 AI 短清单执行，不重新证明整个仓库。安装失败或状态不明，只读原 attempt、版本与服务对账，禁止盲目重装。保留上一完整包供兼容条件下回退。

生产独立读取应用 SHA/摘要、API ready、worker 状态及相关认证接口或页面。普通发布不备份数据库，生产迁移前才备份并验证相关数据。真实支付、扫码、Provider 和接收方结果单列，不占技术发布通道、不冒称已成功。GitHub PR/check、merge-preview、ACK、观察等待不设日常门禁。

## 工作台交接和当前基线

唯一工作台每次从 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json` 的 `thread_id` 读取。工作台开始、恢复、发布写操作前核对路由；不指向自己即停止写操作。更换工作台继续沿用原队列、锁、in_flight、批次和收据，不初始化第二路径、不自动启用 timer/heartbeat；timer 状态以主机实测为准。

开发任务实际推送国内准确候选后，重新读路由，通过 `send_message_to_thread` 投递原任务 ID、base/head/tree/ref、简短五项影响、AI 清单、相关测试结果、未验证项及授权范围；不自行安装共享预发或生产。五项影响只说明对外合同、业务机制、关联模块、页面影响、验证证据，不要求完整依赖图证明。

用户“部署到生产机”即已说明范围授权，相关修复继承授权，无关新范围另列。新批次的无冲突旧基线由原锁内 merge-tree 创建衔接候选，保留原 ref/SHA，机械更新当前 V4 绑定与清单基线，重跑相关检查；已说明业务范围的授权继续有效，但旧审批摘要不能证明新构件；按原授权范围内部绑定新 head/构件/收据，无须因换 SHA 再问一次。业务冲突、ref 漂移、范围变化和失败回原任务并保留队首。批次内通知限于用户已授权开发并实际交付候选的原任务，其他消息仍需明确授权。源码与应用 SHA 因源码-only 改动合法不同，不视为失败。无状态变化用控制器 `status` 轻量读取。

新批次使用 stage_policy_version=2，最后通过预发源码为检查基线；历史 stage_history 每条均保留原安装摘要。旧已安装/密封批次保持原收据及审批摘要，不重算为新策略。`batch-reopen`、`batch-tool-repair` 继续只用于原状态的具体修复，不作为普通发布新增环节。

若源码累计批次在安装前构建失败，原成员检查已通过且预发/生产包和生产游标都未变化，可用同一 `maintenance-check --sha <新修复 SHA>` 检查从原累计 HEAD 继续的登记/控制器修复。不要先 submit 或 supersedes 原失败成员：保留其 ref、成员与检查收据。此分支仅接受新增标准 Go 程序登记及固定控制器、相关测试/文档，不接受业务代码或其他脚本行为变更。检查结果存原批次 `build_repair_maintenance`；按原锁安装已检查的准确固定工具字节（保留回退），再执行 `batch-tool-repair --failed-candidate <原 HEAD> --sha <修复 SHA> --ref <新 ref>`，它原子保留原收据并追加检查过的成员，最后 `batch-stage` 使用原检查和修复检查重试最终包。未知安装、已发生安装或成员/收据变化均不走此恢复分支。


## 准备、配置和 GitHub

开发者提交业务改动后运行 `python3 scripts/dev_preflight.py prepare --base <原base> --authorization-reference <已有范围授权引用>`，查看 source-index/source-lock、CLI 目录和构建声明的机械差异，提交后在干净 checkout 运行 `prepare --verify`。活跃 V4 合同可同步，冻结 donor 不改；dirty canonical 不能冒称准确 commit。

用 `dev_preflight.py handoff --base <base> --ref refs/heads/codex/<work-item> --task-id <原任务> --authorization-reference <已有授权> --impact <五项影响json> --receipt <仓外summary.json>` 生成交接。它确认国内 ref 已推送，head/tree 干净，具名执行及日志存在；结果需实际通过 send_message_to_thread 发给实时路由。机器生成不等于替你获得新增权限。

新构件声明 runtime_config_check_supported，安装器在受保护 env 不变、候选已密封时对 api/worker 执行 `aicrm --check-runtime-config`，在备份、迁移、切换前拒绝坏配置。该命令复用启动 Load，无服务、网络或 DB 操作，返回固定无秘密错误码。新批次缺声明即拒绝；旧元数据缺字段时保留旧阶段合同，并明确没有执行这个新检查，不冒称通过。迁移进行/完成后失败写明 DB 状态并停止应用，不能盲目切旧包或自动恢复 DB。

GitHub 仍不是日常部署门禁。用户要求上传完整 main 时普通快进、读取远端准确 SHA/tree；如上传的是已完成预发而未晋生产的累计源码，分别记录 GitHub main、国内累计 HEAD 和生产应用，不把上传称生产部署。全新 clone 校验后再替换本机 main，保全独有工作，清理旧分支/工作区和可再生成的过程文件。后续从实时工作台累计 HEAD 或国内 main 开发。


状态不明时先运行只读入口，不运行 release/poll/promote 试探：

```sh
sudo /usr/local/libexec/aicrm/domestic_main_release.py status --config /etc/aicrm/domestic-main-release.json
sudo /usr/local/libexec/aicrm/domestic-promote.py --read-domestic-main
```

依据原 attempt、原进程与安装收据明确结果后，才按已授权原恢复入口推进。不可把一次 SSH 断开当作安装失败或重新安装许可。
