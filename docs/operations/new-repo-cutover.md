# AI-CRM-v4 基线切换记录

AI-CRM-v4 的首个提交由生产源码的准确 Git tree 创建，未复制旧仓 Git 历史。基线 commit/tree 和生产 receipt 位于仓库外的 provenance sidecar；业务源码不因加入治理文件而回写生产基线提交。

新开发只从 AI-CRM-v4 当前 `main` 开始。旧仓库、旧 receipt、旧包、旧 head、旧数据库和旧运行时不能作为供体、构建或验收来源。

仓库已公开并启用 `main` 保护。公开可读只解决源对象获取；发布仍必须核对准确 head/tree、required check、签名的新鲜度证明、预发布 receipt、同包晋级和生产读回。
