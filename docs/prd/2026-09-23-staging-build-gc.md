# 预发布构建目录回收 PRD

## 业务判断

预发布机磁盘压力会阻断新候选构建，但构建目录仍可能是当前安装、回滚、观察、已验收待晋级或交接中的唯一包来源。目录年龄和单一队列状态均不足以授权删除。默认仅盘点；只有同一候选在权威发布状态与预发布队列中均明确到达 `released` 或 `stale_candidate`，来源证明完整，且所有安装和待晋级引用均排除，才列入可回收清单。任何缺失、冲突、未知状态或未登记目录保留并说明原因。

## 公开方案与仓库复用

- [GitHub Actions artifact retention](https://github.com/actions/upload-artifact#retention-period) 提供按制品设置保存期限的参考，但时间只能作为候选排序，不能证明业务引用已结束。
- [Kubernetes image GC race 讨论](https://github.com/kubernetes/kubernetes/issues/123631) 展示盘点与删除之间可能出现新引用，因此本工具在共用构建锁下重新读取状态和重算计划。
- 复用本仓 `staging-build.lock`、`release-queue.json`、`release_events.py` 的权威状态结构、`candidate-manifest.json` 与 `staging-receipt.json`。不改 PR #17 正在修改的队列和分槽脚本，不自动安装定时器。

## 合同与边界

1. `inventory` 读取调用方提供的权威状态快照、预发布队列、`current`、已安装 releases、success receipts 和所有 builds，输出原因和计划摘要。快照必须来自指定权威状态文件，操作员负责安全同步到预发布机；工具不把队列文件冒充权威状态。
2. 构建目录名必须是 40 位 SHA；manifest、built receipt、Git HEAD、归档名称和 SHA-256 必须一致。只有终态候选且来源完整的目录可回收；未知目录只报告。
3. `apply --plan` 在独占 `staging-build.lock` 与队列锁下重新盘点，要求输入文件摘要及完整 eligible 清单与计划一致。先将 manifest、receipt、文件清单和引用快照写入独立审计收据，再逐目录回收。若中途失败，收据写明已完成目录与错误；不得宣称全部完成。
4. 清理前后记录磁盘空间和 `/readyz` 只读结果。健康检查失败或版本变化使 apply 失败并留下审计记录。审计目录必须在 builds 之外。
5. 工具不会删除 source-bundles、安装包、当前 release、回滚 release、控制面元数据，也不会连接生产或触发部署。初版不自动调度。

调用方先用 `python3 scripts/release_events.py --state /Users/qianlan/Downloads/新CRM/release-control/state.json show` 核对权威状态，并把同一状态的完整 JSON 以安全、原子方式同步成预发布机上的只读快照。每次生成 inventory 和执行 apply 前，指挥台都必须重新确认快照仍是当前状态；工具会核对快照内容摘要，但无法自行证明远端快照的新鲜度。示例：

```sh
python3 deploy/staging-build-gc.py inventory --state /opt/aicrm/release-control-state.snapshot.json > /opt/aicrm/build-gc-plan.json
python3 deploy/staging-build-gc.py apply --state /opt/aicrm/release-control-state.snapshot.json --plan /opt/aicrm/build-gc-plan.json
```

两个命令都使用已有的构建锁和队列锁；inventory 只取共享锁且不创建文件。指挥台在 apply 前必须逐项检查 eligible、protected 与源证明。审计收据保存在 `/opt/aicrm/build-gc-audit`，任何未确认的候选都不进入清单。

## 分类与验收

- OneID：不涉及；不读取客户身份。
- Persistence：本地文件；审计收据原子写入，不修改业务数据库。
- External Effects：`apply` 删除已证明无引用的预发布构建目录；`/readyz` 为只读请求。无 Provider 写入。
- 测试覆盖终态准入、各类引用保护、来源不明、计划过期、符号链接、审计留存和健康检查。`fast` 与专项测试绑定准确 HEAD；该治理工具不要求构建业务运行时包。
- 上线先由指挥台审阅 PR、在预发布机运行 inventory 并核对计划，再单次手动 apply。回滚方式是停止工具并从另存包重建候选；由于删除不可逆，任何证据缺口均不执行。
