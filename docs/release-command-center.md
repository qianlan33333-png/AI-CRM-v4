# CRM v4 发布入口

当前日常路径见 [国内发布](operations/domestic-main-release.md)：相关检查、国内构建一次、预发验证、内网同包晋级、生产读回和源码对齐。GitHub 仅人工择机归档，不是部署门禁。真实业务验收单独记录，不占技术通道。

旧 `release_control.py`、`release_events.py`、GitHub 轮询、merge-preview、handoff 和观察占位流程仅用于历史查询。当前发布者负责国内候选串行发布，不修改候选源码或解决冲突。历史账本不可删除或重建；旧入口不得与当前发布者同时写生产。
