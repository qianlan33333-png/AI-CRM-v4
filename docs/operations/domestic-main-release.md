# CRM V4 极简国内发布

依据 [已批准的方案](../prd/2026-10-03-extreme-release.md)。普通发布由本地 AI 选择改动包及直接业务消费者，不递归证明整个依赖闭包，不因未知、共享文件或规则改动自动全量。

## 四步

1. AI 读实际 diff、直接调用点和字段使用者，给出短清单。
2. 运行选中检查；纯单元使用原生缓存，相关环境检查只执行一次。
3. 最终累计源码构建一次、安装预发一次，完成清单中的相关冒烟。
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

示例路径与用例以当前候选真实文件为准。小包整包测，大包用 `test` 选择具名用例。`installed_checks` 是既有预发收据中的检查名称；批次合并去重后在最终安装包上覆盖。相关并发检查显式加 `race: true`，PostgreSQL 用例显式加 `postgres: true`。需要额外维护程序时用 `build_commands: ["./cmd/<已有发布程序>"]`。

清单只预检路径、语法和用例存在，拒绝空匹配、skip 和失败。无清单时自动找改动包附近合同；找不到就由 AI 补清单，禁止转全仓。全量只通过明确的维护命令 `dev_preflight.py full` 执行。

```sh
python3 scripts/dev_preflight.py affected --base <base> --head <head> --dry-run
python3 scripts/dev_preflight.py affected --base <base> --head <head> --report-dir <仓外结果目录>
```

## 批次先累计源码

国内裸仓库 `main` 是权威源码，GitHub 人工归档。沿用原队列、锁、attempt 和安装器；预备机保持 2 核 2GB。`release` 检查候选后将运行时成员纳入现有批次，状态为 `prepared`，返回累计 `source_head_sha`；此时应用尚未更新。下一成员从该源码 HEAD 出发。

所有预期成员纳入后运行 `batch-stage`，只构建并安装最终包一次，停在 `stage_validation_pending`。使用现有旅程脚本在该包上完成 AI 选定的业务检查，保存在既有 stage-evidence。`poll` 接受最终预发结果。已有范围授权即可 `promote-authorized`，内部封批和同包晋级；不再询问确认。

```sh
sudo /usr/local/libexec/aicrm/domestic_main_release.py release --config /etc/aicrm/domestic-main-release.json --ref refs/heads/codex/<work-item> --head <head> --base <base>
sudo /usr/local/libexec/aicrm/domestic_main_release.py batch-stage --config /etc/aicrm/domestic-main-release.json
# 在最终安装包完成短清单中的合成业务旅程，使用现有收据格式。
sudo /usr/local/libexec/aicrm/domestic_main_release.py poll --config /etc/aicrm/domestic-main-release.json
sudo /usr/local/libexec/aicrm/domestic_main_release.py promote-authorized --config /etc/aicrm/domestic-main-release.json --head <最终head> --authorization-reference <用户原命令引用>
```

测试、文档、无应用载荷的工具变动只推进源码，不重建重装应用。固定控制器变动使用原 `maintenance-check`：从准确候选控制器执行选中合同，通过后在原锁内安装准确工具字节并保留回退，再释放同一源码候选。维护结果用于同候选继续，不重复执行相同检查。

已安装验收直接使用安装二进制和页面资源；常驻 Go/npm 缓存，依赖按锁文件变化准备。Go 与 Chromium 在小机器上错开执行。业务断言全部完成且进程停止后的临时目录删除失败记警告；身份、数据库归属、进程状态和业务结果不明仍停止。

## 失败、恢复与读回

检查失败停止后续发布写操作，修复后只重跑失败用例及修复影响范围。检查输入未变的通过结果可复用；不重新证明整个仓库。安装失败或状态不明，只读原 attempt、版本与服务对账，禁止盲目重装。保留上一完整包供兼容条件下回退。

生产独立读取应用 SHA/摘要、API ready、worker 状态及相关认证接口或页面。普通发布不备份数据库，生产迁移前才备份并验证相关数据。真实支付、扫码、Provider 和接收方结果单列，不占技术发布通道、不冒称已成功。GitHub PR/check、merge-preview、ACK、观察等待不设日常门禁。

## 工作台交接和当前基线

唯一工作台每次从 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json` 的 `thread_id` 读取。工作台开始、恢复、发布写操作前核对路由；不指向自己即停止写操作。更换工作台继续沿用原队列、锁、in_flight、批次和收据，不初始化第二路径、不自动启用 timer/heartbeat；timer 状态以主机实测为准。

开发任务实际推送国内准确候选后，重新读路由，通过 `send_message_to_thread` 投递原任务 ID、base/head/tree/ref、简短五项影响、AI 清单、相关测试结果、未验证项及授权范围；不自行安装共享预发或生产。五项影响只说明对外合同、业务机制、关联模块、页面影响、验证证据，不要求完整依赖图证明。

用户“部署到生产机”即已说明范围授权，相关修复继承授权，无关新范围另列。原开发者处理代码或基线失败并保留队首顺序。源码与应用 SHA 因源码-only 改动合法不同，不视为失败。无状态变化用控制器 `status` 轻量读取。

旧已安装累计批次仍按原收据收尾，新批次采用源码累计。`batch-reopen`、`batch-tool-repair` 继续只用于原状态的具体修复，不作为普通发布新增环节。
