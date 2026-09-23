## 变更

- 用户可观察的变化：
- 明确未改变的范围：

## 开发前置与并行发布快照

- [ ] 新功能已完成市场/GitHub 调研、复用评估和已确认 PRD；Bug 已完成复现、根因判断和回归测试
- [ ] 假设与 PRD 已确认；开始后将持续推进到完整上线验收
- 工作项/板块：
- 负责人：
- 分支/worktree：
- 状态：开发中｜调试中｜待测试｜待合并｜排队上线｜已上线
- 修改范围与共享入口：
- 依赖的其他工作项：
- 预计上线窗口：
- 当前 main HEAD/tree：
- 活跃分支与 PR：
- 重叠文件/模块/迁移/API：
- 其他已合并但未部署版本：
- 当前排队上线版本：
- 正在执行的部署或观察窗口：
- 本次合并需要重新运行的测试：
- GitHub 合并提交后部署目标：`124.220.53.183`
- [ ] 部署使用仓外私钥，未进入仓库、PR、日志或命令输出
- [ ] 部署前已完成 known_hosts/远端 Host Key 校验
- [ ] 部署后认证读回与观察窗口已完成（未完成不得标记已上线）

## CI 证据留存时间线

- PR 状态阶段：本地验证｜CI 执行｜CI 失败｜CI 通过｜已合并｜已部署｜读回通过
- 首轮 CI 证据：run_id / attempt / head_sha / 结果 / 分类 / artifact URL
- 当前 head 首次 attempt：run_id / attempt / head_sha / 结果
- 当前 head 最终 required check：run_id / attempt / head_sha / 结果 / artifact URL
- 失败原因分类（首轮失败或阻塞）：assertion_or_verification_failure｜environment_setup_failure｜cancelled｜pending_or_incomplete｜unknown_failure
- 修复提交与复跑阶段：
- 合并 SHA：
- 部署 SHA：
- 线上认证读回：
- 观察窗口：
- 未验证项与阻塞原因：
- 预发布环境：`49.232.57.128`
- 预发布验收 SHA/tree：
- 预发布 receipt/artifact：
- capability：
- affected_modules：
- required_routes：
- required_services：
- required_provider_dependencies：
- business_readback：
- 合并后 main tree 与预发布 tree 一致性：
Staging-Head: <预发布时的 PR head SHA>
Staging-Tree: <预发布 tree SHA>
Staging-Receipt: <预发布 receipt 或 artifact 链接>
- [ ] 首轮证据未被后续成功覆盖
- [ ] 当前 head 的最终结果未使用旧 head 绿灯替代
- [ ] PR 正文只保留摘要和 artifact 链接，未粘贴长日志
- [ ] 本地完整验证和预发布验收已完成；GitHub 仅执行一致性与治理门禁

## 验证

- [ ] `make check`
- [ ] 已检查数据 Owner、幂等/重放和外部效果（如适用）
- [ ] 无 Secret、PII 或生产数据进入代码、日志和测试固定值

## 能力影响与风险

- OneID 分类及理由：
- 持久化 / 内部任务 / Provider 读取 / 外部效果分类及理由：
- 影响报告：`governance` artifact（能力 → 共享依赖 → 消费者 → 既有测试/旅程）

高风险变更必须填写下列三行。Head 填 PR 分支当前完整 SHA；追加提交后更新并重跑失败的 governance job。这是作者影响声明，不是独立人工批准。独立审核依保护分支和实际 review 记录确认。

Governance-Head: <当前 PR head 的 40 位 SHA>
Governance-Preservation: <原能力保持不变的业务断言和受影响消费者，至少 12 字符>
Governance-Validation: <实际验证命令、结果及尚未验证项，至少 12 字符>

## Merge preview candidate
- Candidate ID:
- Base main SHA:
- PR head SHA:
- Merge preview SHA / tree SHA:
- Affected modules:
- Shared dependencies:
- Queue position / candidate status:
- Staging accepted receipt and package SHA:
- Bundle mode: incremental / full fallback
- Source mirror status: ready / mirror_stale (non-blocking)
- Direct staging→production promotion receipt:
- Staging fixture manifest/readback (客户同步/教研板块):
