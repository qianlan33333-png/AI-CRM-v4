# HXC 看板：源系统用户 ID 查询标签澄清

**状态：** 已批准，实施中
**开发基线：** `60431e9dc90a749e93a09c38dd5a0113c90b47d9`（fresh `origin/main` worktree）
**用户入口：** `/admin/hxc-dashboard` → 漏斗 / 数据看板工具栏

## 业务判断与源码证据

当前输入框占位符为“精确 HXC 用户 ID”，而同一页面表格把 `HXC-` 前缀的安全用户引用展示为可复制文本。两者看似同一标识，实际不是同一合同：

- 页面把输入原样（仅 `trim`）传为 `exact_hxc_user_id`。
- 查询 handler 对这个值执行 `domain.Subject(h.Key, rawHXCUserID)`，再以得到的 `subject_digest` 精确查询投影。
- `Subject` 以服务端 HMAC key 和原始源系统 HXC 用户 ID 派生摘要，仅把摘要前 6 字节的十六进制展示为 `HXC-` 安全引用。这个引用是不可逆展示标识，不是可交回查询端的原始 ID。

因此，复制列表的 `HXC-…` 粘到当前输入框会令 handler 对字面量再次计算 HMAC，通常得到零行。这是标签语义造成的可预期结果；静态源码链解释了现象，不等同于线上复现、身份缺失或查询服务故障。

本页是 V3 动态看板，沿用单一管理端 Shell 和现有工具栏输入样式。`scripts/check-pr01-donor-manifest.sh` 的 V3-owned overrides 清单明确列出 `web/src/admin/sections/funnelGrid.ts`，所以本 PR 可修改这个 V3-owned 文件；不触碰 `web/donor-sources`。Product Design `audit` 路由与前端组件索引结论是复用这一输入控件、只修正文字和辅助说明，不新建页面、组件或身份展示模式。GitHub 代码搜索没有发现仓库内可复用的 HXC 原始 ID / 安全引用前端说明实现；本 PR 以当前 HXC handler 和页面为行为依据。

## 架构分类

| 判断 | 结论 |
| --- | --- |
| OneID / 外部身份 | 涉及现有 HXC 外部标识的**展示与输入语义**，但不解析、匹配、建客、合并或改变 OneID 归属。继续由现有 HMAC subject 和投影合同处理。 |
| 持久化 | 不涉及。仅改变静态标签和帮助文字。 |
| 内部持久任务 / Provider | 不涉及。不会触发刷新、Provider 读取或写入、队列或 worker。 |
| 权限 / PII | 保持现有 Admin 页面授权和原始输入查询合同；不在列表新增原始 ID、不记录输入值、不把安全引用提升为身份凭证。 |

## 最小范围

1. 在现有 HXC 工具栏输入框把标签语义改为“源系统 HXC 用户 ID”。
2. 在输入框旁加入可见、可访问的短说明：“请填源系统 HXC 用户 ID；列表安全用户引用（HXC-…）仅用于展示，不能用于查询。”输入附带对应的 `aria-label` 与 `aria-describedby`。
3. 保持 `exact_hxc_user_id` 请求字段、`trim`、handler HMAC 派生、`subject_digest` 查询、分页、排序与全部其它筛选完全不变。
4. 列表第一列仍展示现有安全用户引用；不显示、回填或由安全引用推导原始源系统 ID。

## 明确排除

- 不接受 `HXC-…` 安全引用作为新的查询协议，不做反向 HMAC、二次 hash 兼容、身份映射或模糊检索。
- 不改 HXC domain、HTTP handler、PostgreSQL 查询、OpenAPI、OneID Port、投影、任务/刷新、权限或审计结构。
- 不将原始 HXC 用户 ID 添加到表格、URL、客户端日志或错误文本。
- 不把零结果包装为身份不存在、身份冲突或后端能力不可用。

## 验收

在现有 `web/v3/hxcPresentation.test.mjs` 的真实页面挂载用例中断言：

| 场景 | 预期 |
| --- | --- |
| 初始工具栏 | 输入明确标记为“源系统 HXC 用户 ID”，有上述安全引用不可查询的说明与可访问关联。 |
| 录入 `raw-source-hxc-user-id` 并应用筛选 | 发往现有 `/api/admin/hxc-dashboard/query` 的 `exact_hxc_user_id` 等于该原始字符串；浏览器层不 hash、不替换为 `HXC-…`。 |
| 列表展示 | 现有 `HXC-` 安全用户引用仍出现；说明不把它描述为查询值。 |
| 其它筛选、分页和加载 | 沿用现有请求形状与页面行为。 |

HXC presentation 用例注册到 canonical `run-donor-view-consumers.sh check` 的已物化 frontend consumer 段，因此 required frontend lane 会执行它；相关 domain/handler 合同测试继续单独运行。浏览器线上回读须在已认证环境单独记录，不能由 JSDOM 或源码推断替代。
