# CRM v4 发布入口

当前发布路径见 [国内构建与内网自动发布](operations/domestic-release.md)。GitHub 保留为唯一 PR 和受保护 `main`；合并后预备机按第一父链逐个构建和技术发布。真实业务验收单独记录，不占用技术通道。

旧 `release_control.py`、`release_events.py`、merge-preview、候选 handoff 及观察占位流程仅用于查询历史账本。新 PR 不再创建候选事件，也不由指挥台合并或部署。历史状态文件不可删除或重建；原入口在切换后不得写生产。
