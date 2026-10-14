---
name: aicrm-v4-release
description: "Operate the single AI-CRM-v4 domestic release workbench: integrate candidates in order, run selected installed contracts, promote the same artifact with existing authorization, reconcile failures and reclaim closed release residue."
---

# V4 发布

开始、恢复和每次发布写操作前读当前 V4 `AGENTS.md`、[唯一发布文档](../../docs/operations/domestic-main-release.md)，重新读取 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json`。thread_id 不指向本任务就停止发布写入。沿用原队列、锁、attempt、收据；不创建另一个通道、定时器或 heartbeat，不根据旧文字推断服务状态。

## 正常路径

1. 核对国内 main/累计源码、准确 base/head/tree/ref 和已有授权范围。无冲突旧基线由原控制器创建衔接候选：保留原 ref，重做准备和直接消费者检查。冲突、范围变化或失败回原任务，保留队首顺序。
2. 读 AI 短清单和真实结果，不要求 GitHub PR/check、依赖闭包或全仓检查。文档/skill/测试/无载荷工具推进源码即可。
3. 已完成的成员先接收源码、按需要安装预发；每个新构件构建/安装各一次。新批次基于最后通过的预发源码检查新增合同与直接消费者，不重复整个累计历史。历史证据保持原源码/构件身份。
4. 用原控制器 `stage-check` 注册合同执行实际安装二进制、隔离 PG/浏览器。JSON 具名终态、摘要和安装收据共同证明；无匹配、skip、超时、伪 PASS 均未通过。
5. 用户已命令当前范围部署到生产时，内部绑定准确成员、head/tree、构件与预发收据后直接同包晋级，独立读回版本、完整摘要、服务与相关入口。已授权的相关修复可内部重绑定新 head，换 SHA 不自动要求再确认；旧审批摘要不证明新构件，无关新增另列；要求集中上线时所有成员完成前保留在预发。

## 异常和清理

业务失败只修失败及影响范围；准备失败修机械绑定；环境/fixture/汇总失败保留原日志，在安装载荷没变时不重装。安装结果不明或断线只读原 attempt、进程、收据和版本，证明原操作完成后才能推进；不换 attempt 重装。

安装前用候选 `aicrm --check-runtime-config` 复用启动验证链，错误码不含秘密、不改受保护 env。普通发布不备份数据库，生产迁移前才备份；迁移后失败不能盲目回旧代码或自动恢复 DB。

完成后回收关闭的临时源码、测试数据库、重复构建副本和过期无引用包。原控制器成功收尾同时清理预发与生产，两台均 completed 且零候选才记收尾完成；异常停止后续清理并只读对账。保留当前＋最近完整成功包、活跃引用、必要失败日志/收据/迁移备份。有效编译/依赖缓存保留，损坏缓存定点清除。关闭进程后的普通目录清理失败可警告；安装不明仍停止写操作。公开不可变包与私有凭据按各自用途核对权限。

技术结果与真实扫码、支付、回调、Provider accepted/delivered 分别记录；后者未验收不冒称业务成功，也不阻塞下一次技术发布。需要代理时依 AGENTS 使用一个 gpt-6-luna max agent，不按阶段拆代理。

## GitHub 和本机重置

用户要求上传 main 时普通快进并读取远端准确 SHA；无该请求不新增 GitHub 部署门禁。清理本机旧 checkout 前先下载并校验全新 main，保全独有未推送修改；不删除凭据、业务数据、原控制状态或恢复所需证据。fresh clone 保留固定源码路径和 skill 路由，后续开发核对实时国内基线。
