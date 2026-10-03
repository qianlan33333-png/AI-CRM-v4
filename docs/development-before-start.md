# 开发开始前

1. 只用 V4，从国内 main 或唯一工作台给出的累计源码 HEAD 建立独立 codex worktree/分支；准确 base/head/tree 随候选交付。
2. 一项能力做一次业务流程、参考和简短父 PRD，记录 OneID、持久化和外部效果分类；已批准 brief 的相关修复直接继承授权。
3. 前端能力编码前用 Product Design，复用当前组件。新增限制应用核心 skill 的必要性判断。
4. 本地 AI 看准确 diff 和直接调用/字段消费者，在 `docs/release-checks.json` 列出相关包与具名用例。小包整包测，大包选用例；取消依赖闭包、未知自动全量和强制全仓 fast/compile。相关编译由测试与最终应用构建完成。
5. 执行清单；空匹配、skip、缺环境和超时不算通过。身份/权限/支付/数据变动加强相关断言，普通远处回归允许漏测风险。失败修复只重跑受影响检查。
6. 推送国内候选后重读 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json`，向其工作台投递原任务 ID、base/head/tree/ref、简短五项影响、清单、结果、未验证项及已有授权范围。开发任务不安装共享预发/生产。

五项影响是对外合同、业务机制、直接关联模块、页面、验证证据，短说明即可。下一批次成员从累计源码 HEAD 出发，无须等前一成员安装。最终统一构建、预发、相关旅程，已有当前范围生产授权直接同包晋级和读回，详见 [唯一发布文档](operations/domestic-main-release.md)。文档/测试/无载荷工具只推进源码。普通发布不备份数据库；真实 Provider 与技术结果分开。
