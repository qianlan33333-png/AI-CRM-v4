# 公共问卷 H5：答题与提交呈现

## 决策

本次会话的 Product Design catalog 未提供可读取路由，因此未执行该插件步骤；沿用已批准的浅灰、白卡与蓝色主操作视觉基线，并以实际 H5 页面和移动 Chromium 截图验证。

`/q/{slug}` 是已发布问卷的公开 H5 入口。它先由 `internal/survey/http.Handler.publicEntry` 验证 slug、读取已发布定义和既有 Survey OAuth 会话，再跳转到 `/h5/all.html` 或 `/h5/one.html`；答卷读取、提交和结果回读仍分别走既有 `PublicApplication` 的公开 API。

本次只让受访者在 375、390、430 宽度下清楚完成答题、提交和结果查看。冻结 H5 模板和 Owner 不改；在 `web/v3` 新增公开问卷 Host 与样式，并由 release HTML 在冻结 H5 runtime 之前装配。它只标注、辅助可访问性和样式既有状态，不发起业务请求、不替换提交逻辑。

OneID：不直接参与。公开入口继续使用 Survey Owner 已有的 OAuth 会话来确定提交身份；本次不解析、创建、关联或读取身份记录。

持久化：不直接参与。定义版本、答卷写入、提交 key 的幂等回执和结果 token 均保留 Survey Owner 的既有事务边界。V3 只将既有 submitting 状态可视化并禁用按钮，不能成为服务端幂等的替代。

外部效果与支付：不参与。本次不启用、调用或模拟完成回调、支付或任何 Provider；`h5/pay.html` 等同一 H5 产物仅保留原有装配，不作为问卷答题路径或本 PR 的能力。

## 受访者流程与状态

| 状态 | 用户看到什么 | 约束 |
| --- | --- | --- |
| 已授权、可答题 | 问卷标题、题数/逐题进度、题卡、必答标记和固定主按钮；底部留白保证最后一题不被按钮遮住。 | `all` 与 `one` 模式沿用 Owner 返回的版本和题目顺序。 |
| 必填或格式未通过 | 已有 Controller 的校验提示保持可见；已填写选项和文本不丢失。 | 不以中文文案推断服务端状态，也不改题目规则。 |
| 正在提交 | 主按钮禁用、带明确忙碌语义，重复点击不再触发 UI 行为。 | Controller 既有 `submitting` guard 和 submission key 继续是实际保护。 |
| 提交失败或未知网络结果 | 错误置于当前答卷旁，传输状态以次要详情呈现，用户可以保留当前页面后显式重试；提交路径中的答案和未变更答案的 submission key 保留。 | 不从页面类型推断失败发生在读取还是提交；不显示成功、不清空输入、不生成新的业务身份或 Provider 请求；同一 key 的恢复请求仍由 Owner 幂等受理。 |
| 已受理与结果回读 | 受理卡引导查看真实结果；结果页显示“提交已确认”、提交时间、版本和提交编号。 | 只在已有 result token 的 GET 回读成功后呈现最终完成事实；不向受访者展示内部 `local_only` 或外部效果实现字段。 |
| OAuth、未授权、冲突、不可用 | 复用 H5 的既有身份页和明确原因；401 提示重新授权，403 提示无权限访问，503 保留稍后重试。 | 不自动跳转或发起新 OAuth、不绕过 OAuth、不把非微信提示误写成“正在验证”。 |

## 组件与装配

| 参考/入口 | 采用 | 不采用 |
| --- | --- | --- |
| `web/src/h5/controller.ts` 和 `all`、`one`、`result` 模板 | 既有题目、校验、提交 key、失败保留答案和结果 token。 | 不在冻结模板或 Controller 私自重写 Owner 规则。 |
| `web/v3/h5AuthAdapter.ts` | 沿用其 H5 release 注入次序和无设备 demo 壳的适配边界。 | 不混入后台 shell、选择器或 admin CSS。 |
| `web/v3/shared/ui/visualTokens.css` | 使用既有白底、浅灰、蓝色语义 token。 | 不引入 Ant Design、Vant 或新依赖。 |
| [Vant Field 的 required/错误语义](https://github.com/youzan/vant/blob/main/packages/vant/src/field/Field.tsx) | 参考必填和紧邻可操作区域的错误可见性。 | 不复制组件或运行时；问卷题型和校验仍由 Survey Owner 定义。 |

新增 `surveyPublicHost` 和 `surveyPublicStyles`，并在 `scripts/build-v3-host-adapters.mjs`、`scripts/stage-survey-ui.mjs`、`scripts/test-stage-survey-ui.mjs` 中登记其 manifest 与递归闭包。发布 HTML 必须引用 hash 化同源资源；缺失 Host、样式、闭包文件或 release 元数据应失败，不能退回未装饰页面。

## 验收

1. 实际 PostgreSQL Owner fixture 创建并发布问卷，受信测试会话仅走既有 Survey OAuth/session 边界；Chromium 在 `/q/{slug}` 完成答题，重复点击只产生一次提交，随后 GET 读取结果回执。
2. 失败状态用客户端受控首次失败验证：答案仍在、按钮恢复且没有成功文案；随后同一 submission key 重试由真实 Owner 受理、GET 回读结果且 PostgreSQL 仅有一条该答卷。
3. 在 375、390、430 捕获 `all`、`one`/进度、提交中、结果和错误恢复的真实 H5 画面；移动验收仅覆盖公开 H5，不外推为后台移动壳验收。
4. `npm` 类型检查、V3 build、Survey stage 与 stage closure/缺失拒绝测试通过；Go Survey UI/HTTP 定向测试和 Chromium Journey 通过。

## 不覆盖

不改变问卷定义或版本、OAuth/OneID、支付、完成外部效果、问卷后台编辑器、企微侧边栏、公开商品页或未挂载 H5 页面。已提交答卷的跨设备“再次填写”规则若需要变更，必须由 Survey Owner 另行定义。
