# 开发与发布优化

本任务复用用户已批准的方案：优化 skill 和原工具，工作台自动衔接无冲突旧基线，批次内通知有开发授权的原任务。随后将完整 main 上传 GitHub，校验全新 clone，再清理本机旧分支与过程目录。

## 业务判断

```mermaid
flowchart TD
 A[已授权行为与准确 diff] --> B[机械准备与直接消费者检查]
 B --> C{基线是否当前累计源码}
 C -->|是| D[原队列接收]
 C -->|否 无冲突| E[保留原候选 创建衔接候选]
 E --> B
 C -->|冲突或范围变化| F[原任务修复 保留顺序]
 D --> G[本次构件构建 安装预发 相关合同]
 G --> H{技术结果明确通过}
 H -->|有当前范围生产授权| I[同包生产 独立读回]
 H -->|未授权| J[留在预发]
 H -->|失败或不明| K[保留 attempt 只读核对]
```

OneID：不涉及客户归属修改。持久化：复用发布队列、原锁、attempt 与收据；不新增业务队列。外部效果：GitHub 推送和部署是已有授权的运维动作；不触发支付、退款、消息发送或订单履约。UI：本次不改用户页面。新增限制只用于准确源码、无秘密的配置校验和可信执行结果；不新增审批、时间窗或固定测试数量。

## 复用与取舍

- [Go test2json](https://go.dev/cmd/test2json/) 的具名终态替代 PASS 文本、测试数量和退出码猜测。
- [GitHub immutable artifact](https://github.com/actions/upload-artifact) 的摘要绑定思路用于同包晋级；GitHub CI 不成为新门禁。
- [Bazel query](https://bazel.build/query/quickstart) 的消费者选择思路用于准确 diff；不引入 Bazel 或递归依赖闭包。
- 保留原发布控制器、国内裸仓库、串行锁、合成 PG 与 2 核 2GB 预备机。
- 有效编译和下载缓存保留；关闭的临时源码、重复构建副本和过期无引用包回收。

## 交付

1. 开发 skill 一个核心正文，frontdoor 薄入口，UI 条件读取；新增可发现的发布 skill。
2. `dev_preflight prepare` 同步当前 V4 源码索引、消费视图和 CLI 目录，检查发布程序声明。机械修改显式展示；不会改业务代码或放松断言。
3. 原控制器 `stage-check` 固定合同注册表、隔离环境、具名 JSON 终态和真实安装包身份。空匹配、skip、超时、假 PASS 和摘要篡改拒绝。
4. 新批次按最后一次通过的预发源码增量选择合同。旧批次和收据维持原语义；历史通过记录保留原构件身份。
5. 无冲突旧基线在原锁下合并生成新候选，保留原 ref，重做准备及相关检查；冲突、ref 漂移和未知执行状态停止。
6. `aicrm --check-runtime-config` 与启动同一验证链；安装切换前校验，无网络/数据库/Provider 操作，不修改受保护环境。
7. 清理当前＋最近完整成功包与活跃引用；迁移备份及必要收据保留。迁移失败不盲目恢复数据库或冒称代码回退成功。

## 验证和接受

按本次直接消费者运行 Python 发布合同、Node 源码绑定合同、具名 Go 配置测试和 Linux 实际安装二进制合同；不跑全仓全量。定点对抗覆盖无匹配/skip/伪 PASS、漂移与冲突、篡改与路径逃逸、增量保留、断线/重复操作、配置泄密与副作用、迁移失败、硬链和活跃文件保留。技术安装与真实 Provider 业务结果分别记录。

GitHub main 更新用普通快进，不覆盖远端独有提交；fresh clone 的 SHA/tree 与推送读回一致后才替换本机 main。独有未提交文件和未并入提交先制作可恢复副本；不删除凭据、业务数据、原发布锁/队列或恢复所需收据。后续开发继续从实时国内 main/工作台累计 HEAD 开始。

## 已授权硬链接与自动清理修复

用户要求：修复现有旧包硬链接校验，完成本次可清理包回收，后续每次发布自动完成，无正常路径待处理项。继承上述分类和范围：无 OneID、UI、业务数据迁移或 Provider 写入；沿用原发布/安装锁及原成功收据。

```mermaid
flowchart LR
 A[原安装锁与当前健康收据] --> B[完整包和全部公开别名校验]
 B --> C[必要 observer 文件原子独立复制]
 C --> D[复用已有真实成功观察器]
 D --> E[当前 最近完整包 兼容回滚 活跃引用保留]
 E --> F[原 inventory 与 apply_plan 回收无引用包]
 F --> G[再次 inventory 零删除候选并记完成]
```

参考：[rsync 官方 link-dest 说明](https://rsync.samba.org/ftp/rsync/rsync.1)指出属性修改会作用于共享 inode；[RsyncProject/rsync atomic-rsync](https://github.com/RsyncProject/rsync/blob/master/support/atomic-rsync)采用新对象再切换。复用此原则处理需要独立 inode 的 observer 三个文件，不 chmod/chown 共享 inode。

根因：514 组公开共享文件是 root 管理的 0644 构件。root 本就可 chmod 或写入普通未共享包，对这些已验证公开文件额外拒绝 owner-write 没有减少 root 能力；实际边界是所有别名在托管包内、父链受 root 管理、无组/其他用户写入、内容与清单一致。私密文件、外部别名、可被非 root 写入的文件继续拒绝。

固定国内安装器在原锁中原子复制仍共享的三份 observer 代码（相同内容与权限，不动运行中的 bin），复用真实 current/API/worker/schema 观察器，准确成功事实已存在则复用。最近 native 前驱从已验证安装收据的 previous_sha 读取，并用完整包验证保留；不伪造旧 legacy success fact。之后使用原清理 owner 执行并再次 inventory，成功后才记录 completed。

检查只选 cleanup、domestic GC、success observer 及清理衔接合同，覆盖 root owner-write/私密与外部别名/可写父链、FD9 原锁、单独 inode 与副本中断、schema 拒绝零删除、重复运行、当前与前驱保留。真实预发安装同包验证补实际 observer/隔离 PG 与清理 dry-run；生产发布沿原控制器自动触发并独立读回无剩余可删除项。

对抗审查补充：observer 副本暂存在 releases 的固定私有生成目录（包目录外、同一设备），原锁内回收已结束的分类副本；错误文件/符号链接/挂载不当继续拒绝。新增同FS、半途失败续跑和跨SHA共享文件回收合同。

固定 `.domestic-observer-*` 前缀归本安装器独占，root 私有目录只容纳三份副本，不存业务文件。中断副本不按内容完整性作为回收门禁，按原排他锁、固定名称、所属与设备分类；未知内容拒绝清理。

自动收尾同时覆盖预发与生产；预发用真实 native 安装收据和当前包校验，不访问生产源码 cursor。两台都回报 completed 且零候选才记收尾完成，失败或状态不明停止后续清理写入。已启用的 Excel 辅助服务随应用切换重启并进入 readiness，释放其旧工作目录；不启用原本停用的服务。本次存量进程经代码一致性核实后定点重启。
