# AI-CRM v4 部署约定

## 运行环境

应用只监听 `127.0.0.1:8080`，Caddy 对外终止 HTTPS 并反代。PostgreSQL 16 是运行时数据依赖。每版放在不可变 SHA 目录，`/opt/aicrm/current` 原子指向当前版本；环境与密钥在 `/etc/aicrm/aicrm.env`，不入 Git、不打印到日志。

## 发布

国内主仓激活前，GitHub `main` 和准确 PR `check` 仍是旧发布门禁，见[旧发布操作](../docs/operations/domestic-release.md)。激活后，权威 `main` 位于预备机裸仓库，独立开发分支登记准确候选，经预备机检查、合成数据验收、生产源码 bundle 备份和同包内网晋级；生产读回后才推进国内 `main`，见[国内主仓发布](../docs/operations/domestic-main-release.md)。GitHub 只由用户人工择机归档，不自动推送。

- 预备机角色：root 管理 `/etc/aicrm/domestic-release-role`，内容精确为 `staging`；数据库仅含可重建合成数据，不备份。迁移失败会停止候选；当前没有仓库内的数据库重置工具，必须按[国内发布操作](../docs/operations/domestic-release.md)核验主机和合成库身份后人工引导恢复。
- 生产机角色：同一路径内容精确为 `production`；数据是真实业务数据，只有数据库迁移前备份并核对归档。普通页面/程序发布跳过数据库备份与迁移。
- 角色文件缺失、符号链接、所有者/权限或内容不符时停止。角色来自主机本地受保护配置，不接受 PR 或发布参数覆盖。
- 生产安装校验完整文件清单和摘要，原子切换版本，重启服务并读回 SHA、服务与 `/readyz`。健康失败切回上一技术版本；状态不明时只读对账。
- 支付、退款、企微 Provider 默认按环境开关关闭；部署器不写商户平台配置。生产技术健康与 Provider/真实业务验收分别记录。

## 工具升级与首次切换

发布器、构建器、安装器或 systemd 单元变更需要在启用前完成准确代码的安装核验。国内主仓切换时先完成 #39 及三端准确版本核对，停旧 timer 和生产写入口，验证预备机真实服务用户/目录/PATH/systemd 主机合同与失败恢复演练，再建立受限裸仓和激活收据。新旧发布器不得同时操作生产。详细门槛见[国内主仓发布](../docs/operations/domestic-main-release.md)。

## 历史材料

旧 GitHub Actions 部署、merge-preview、candidate handoff、人工提交发布包和一次性恢复命令已退出正常发布路径。相关历史收据保留供审计，不能据此重新发布或认定当前状态。
