# 开发开始前

1. 只使用当前 AI-CRM-v4 仓库和准确 `main`；创建新的 `codex/<work-item>` worktree/分支和 PR。
2. 先写业务判断逻辑，查成熟产品和 GitHub 参考，评估仓库复用点，形成并冻结 PRD。
3. 明确 OneID、Persistence、External Effects 分类、数据 Owner、事务边界、真实验收旅程和回滚点。
4. 查看活跃 handoff、PR、共享文件和发布队列。Composition、迁移、公共组件、Provider、External Effects、CI、部署脚本串行；普通领域可并行开发。
5. 保存开始时 `main`、HEAD、tree 和干净状态。缺少准确基线、凭据或安全边界时失败关闭。

仓库公开不等于候选可信。发布候选必须由 GitHub 当前 `main` 和准确 PR head 生成，并由短时签名 freshness attestation 绑定 source bundle、tree 和（晋级阶段的）package 摘要。
