# 开发开始前

1. 只使用当前 AI-CRM-v4 仓库和准确 `main`；创建新的 `codex/<work-item>` worktree/分支和 PR。
2. 先写业务判断逻辑，查成熟产品和 GitHub 参考，评估仓库复用点，形成并冻结 PRD。
3. 明确 OneID、Persistence、External Effects 分类、数据 Owner、事务边界、真实验收旅程和回滚点。
4. 查看活跃 PR、共享文件与生产技术发布游标。Composition、迁移、公共组件、Provider、External Effects、CI 和部署脚本需协调同一文件；不同板块可并行开发、各提一个 PR。
5. 保存开始时 `main`、HEAD、tree 和干净状态。缺少准确基线、凭据或安全边界时失败关闭。

仓库公开不等于候选可信。受保护 `main` 与准确提交的 `check` 决定能否进入国内构建队列。合并后预备机按第一父链处理提交、生成完整文件清单和摘要，基础验收后通过内网晋级同一文件树。业务真实验收在生产技术安装后独立记录。见 `docs/operations/domestic-release.md`。
