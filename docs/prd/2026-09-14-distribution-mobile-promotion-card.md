# 分销中心移动商品卡精简与真实链接复制

日期：2026-09-14
范围：优化 H5 分销员端的注册、推广商品、收益和明细页面，先修复商品卡在手机上的窄列排版，并把已具备推广资格的商品操作收敛为可复制的真实分销链接。分佣结算的生产启用另走受控配置核验；不扩展商品购买、支付或后台页面。

## 业务判断

分销员需要在手机上立即看清三项事实：推广什么商品、售价多少、预计能分多少。只有服务端已判定商品可售、策略已开启、本人有效购买资格通过，且当前商户分佣结算与 receiver 都已就绪时，才允许调用既有 promotion-credentials 端点签发受控 `/d/dpc_...` token。

未就绪时不得拼接普通商品 URL 充当“分销链接”。页面只显示最短的真实阻断原因及必要操作，例如完成收款准备、重新微信登录或刷新状态。商户能力关闭仅称“商户尚未启用分佣结算”，不归咎于用户微信身份。

现有卡片即使无封面也保留 96px/72px 的图片列；唯一正文 div 按 CSS 自动布局落在首个 72px 图片格，手机内容因此挤成竖列。商品卡改为单列，不再渲染封面、退款等待说明、公开编号、协议或启用状态等非成交决策信息。注册、收益和明细沿用现有卡片、tabs、列表和状态组件，仅收紧手机宽度、信息层级和必要反馈。

## 界面与交互

- 商品卡仅显示：商品名称、售价、`预计佣金 ¥X（Y%）`、主操作“复制分销链接”。
- 页面头部仅保留“分销中心”；仅在当前无法生成链接时显示简短状态和真实操作。收益页及服务端佣金明细不在本次删除范围。
- 点击“复制分销链接”时才请求既有凭证端点。签发成功后尝试复制；浏览器拒绝剪贴板时显示已签发的受控链接和同名复制按钮，供用户重试或手动复制。
- 手机宽度下，商品网格与商品卡均使用 `minmax(0, 1fr)` 单列；主操作占满卡片可用宽度，避免窄列或横向溢出。
- 注册页只保留协议确认和可信微信登录必要反馈；收益页保留金额、状态筛选及加载更多；明细保留商品、订单、佣金与结算事实。三者不得增加购买或支付动作。

## 架构分类

- OneID：不新增或修改身份、客户归属；继续消费既有可信会话。
- 持久化：0165 由 Payment 为 receiver 增加受限 `failure_class`，仅保存空值或 `provider_permission_denied`；0166 为已验签、精确匹配的 CLOSED 分账指令保存八类有限诊断；0167 由 Distribution 增加 `settlement_not_paid` 异常。旧失败保持空值（未知），不回填或推断历史 Provider 响应。
- 外部效果：凭证签发复用现有受控 HTTP 契约和既有幂等键；UI 不能自行生成 token 或 URL。Provider 查询仍在 UoW 外，状态、异常、审计、outbox 与既有 reserve-unfreeze 接受仍在各自既有同一 PostgreSQL UoW；不新增队列、EER 表或自动重试。本 PR 不修改生产配置；真实 Provider 验收另行受控记录。

## 分佣结算启用边界

系统侧启用须在独立受控运维核验中确认商户权限、普通支付配置和完整认证材料后，显式配置：

- `AICRM_WECHAT_PAY_PROFIT_SHARING_ENABLED=true`
- `AICRM_WECHAT_PAY_PROFIT_SHARING_AUTH_MODE=certificate|public_key`
- certificate 模式使用完整有效的平台 X.509 证书；public_key 模式使用真实 public-key ID 与匹配公钥。

配置加载会拒绝缺失前置条件。页面和本 PR 的业务代码不写生产环境变量、不自行调用微信 Provider 或受理资金操作。生产开关的受控启用、receiver 恢复和生产读回属于独立发布动作；即使开关已由运维启用，也不得把它当作个人 receiver 已就绪或资金已到账的证据。

## Provider 失败诊断

官方 SDK 的原始 `APIError` 含 response body、message、detail 和 headers，不能进入 Payment、Distribution、审计或页面。本 PR 仅对添加 receiver 的官方 `403 NO_AUTH`（SDK 文档明确为商户无分账权限）映射为 `provider_permission_denied`：外部调用仍记为已尝试/已执行并终态失败，不自动重试。Payment 在同一完成事务持久化该有限类别和审计，Distribution 同步为 `receiver_provider_permission_denied`，公众端和管理端只显示“支付侧拒绝，请联系商家核验分佣权限”。所有其他状态、代码、网络错误和历史空类别继续按 unknown 或泛化失败处理。

## 已提交分账指令的 CLOSED 处理

微信的 CLOSED 只证明该接收方没有收到该次分账，不能据此抹去商户对分销员的佣金义务。Payment 仅在 receiver 的类型为 `PERSONAL_OPENID`、账号、金额均与原指令一致时，才把官方八种 `fail_reason` 映射为有限失败类别；未知或空原因不推断。Distribution 对普通拒付和部分退款后的剩余款项保留 `current_payable_minor`，记录 `settlement_not_paid` 与可处理的有限中文原因，并沿既有稳定键解冻支付侧 reserve。

只有两种已经持久化的业务事实可以在 CLOSED 后取消该笔已提交佣金：原购买金额已全额退款，或该笔佣金历史中存在 `qualification_revoked_after_paid` 且 reason 同为 `qualification_revoked_after_paid` 的明确撤销事实。资格证据不可用、冲突、当前重新购买合格或未知结果均不能触发取消。异常金额为 `max(current_payable_minor - paid_minor, 0)`，已付事实不会被重新记为待付；不生成负向佣金调整或新的分账付款。

取消和异常处理也受同一佣金终态约束：全额退款或明确资格撤销使佣金取消时，同一 PostgreSQL UoW 会把对应仍打开的 `buyer_refund_after_paid`、`qualification_revoked_after_paid` 业务异常收敛为已解决并保留历史审计，不能继续展示或接受追回、商户承担操作。服务端再次核验佣金终态和未付金额；已取消或零欠佣的 split 查询、追回和承担均拒绝，只有原 payment reserve 的 unfreeze 查询仍可用于核实资金释放。

追回和商户承担只针对已确认到账后的售后差额，并从最新佣金事实计算，不能依赖异常创建时的金额快照：退款后的可处理额为 `max(paid_minor-current_payable_minor,0)`；明确资格撤销的可处理额为 `paid_minor`。同一佣金已登记的追回与商户承担从 append-only `distribution_commission_adjustments` 账本累计扣减，合计不得超过该可处理额。未付或 unknown 的 CLOSED 仅保留欠佣异常，不可登记追回、承担或伪装成人工付款。

异常的 `reason`、`evidence_reference` 和 `amount_minor` 是产生异常时的不可变业务来源。管理端的 Payment 查询、追回和商户承担只能更新处理状态，并把查询观察值、人工原因、凭证和金额写入已有 append-only 审计、收据及调整账本；不得覆盖来源字段。管理端写操作统一先锁佣金、再锁异常（nullable settlement join 仅 `FOR UPDATE OF e`），避免与 due worker 的锁序相反。管理员先查询后，后续 due 重放仍能依据原始资格撤销事实取消未付佣金；同一凭证重复不会重复记账。

Payment 对管理端的稳定分账指令引用统一为严格 `psinst_<正整数>`。Distribution 只接受该格式，管理异常列表和详情用同一引用计算可查询状态；旧的 `psinstr_` 形式不能被前端或服务端误当作可查询指令。

## 参考与复用

- [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md)：借鉴语义清晰、单一主操作及 token 一致性；不引入依赖或平行设计系统。
- [copy-to-clipboard](https://github.com/sudodoki/copy-to-clipboard/blob/e1f76689ea515c0821ac11b615155f5a8f418cbe/index.ts)：借鉴由用户点击发起复制并提供可见回退；不引入该包，失败时沿用本仓可见链接/选择复制回退。
- 本仓组件索引：`web/v3/distributionCenter.ts` + `web/v3/distribution.css` 是公共分销中心唯一入口；商品编辑页已有 `productAdapter.ts` 的复制回退可复用行为。不得修改 frozen donor 或另建页面壳。
- Product Design 路由：已按 `product-design:audit` 审查既有截图和当前真实 CSS；目标已明确，故不做 ideate/canvas，改动后以 Chromium 手机 viewport 截图验收。

## 验收

1. 320px、375px、390px、393px、430px viewport 的商品卡无窄列、无横向溢出；以计算样式和内容宽度断言商品正文不是 72px 图片列，商品标题、售价、预计佣金和复制按钮完整可读。
2. ready 商品点击后真实调用 `/api/v1/distribution/products/{id}/promotion-credentials`，只接受当前 origin 的 `/d/dpc_...` URL；复制内容与对话框链接相同。
3. Clipboard 不可用或失败时，token 仍以可见受控输入框呈现；提示明确，不显示普通商品 URL。
4. receiver、可信微信会话或商户分佣结算未就绪时，不调用凭证端点，显示对应短操作。
5. `403 NO_AUTH` 的模拟 SDK 错误只留下 `provider_permission_denied`；Payment receiver、Payment/Distribution 审计、公开 profile 与管理端 read model 可读该安全类别，不保留 SDK Body/Message/Detail，其他错误仍是未知结果。
6. 精确 `PERSONAL_OPENID` / 账号 / 金额匹配的 CLOSED 指令写入有限失败类别；八种、空和未知 `fail_reason` 都有 Payment adapter 覆盖。普通 CLOSED、部分退款、全额退款、明确历史资格撤销及资格证据不可用的真实 PostgreSQL 回归分别验证佣金金额、异常收敛、调整和 stable unfreeze 副作用。
7. 真实 PostgreSQL 管理流验证：查询观察不改写资格撤销来源，随后 due 重放仍只取消一次；已到账售后按最新 `paid_minor/current_payable_minor` 计算，追回和承担共同受 append-only 账本上限约束，未付动作被拒绝。
8. `psinst_` 的 Payment 稳定引用可由真实 PostgreSQL 管理异常详情和列表读取；取消佣金不再暴露资金操作，取消后的 unfreeze 仍可查询。
8. 既有服务端资格严格过滤、分页、收益与后台分销管理回归保持通过；构建后以真实 Chromium 移动截图确认挂载 assets 和计算样式。
