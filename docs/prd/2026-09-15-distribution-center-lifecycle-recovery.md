# 公共分销中心的读取恢复与并发边界

## 业务判断

`/distribution` 是公共分销员已登录后的既有页面。它从既有可信会话读取既有 canonical customer 范围内的分销投影：

- **OneID：读取 canonical customer。** 本项不解析、创建、关联或合并身份，也不改变 OAuth、购买资格或归因。
- **持久化：不涉及。** 仅修正浏览器内已读取事实、筛选和游标的生命周期；不新增表、写模型、任务或队列。
- **外部效果：不新增。** 不发起 Payment、退款、分账或 Provider 调用。现有注册、收款准备和推广凭证请求继续由既有领域接口处理，保留其原始幂等键与服务端审计边界。

## 已确认问题

审阅 `web/v3/distributionCenter.ts` 的真实请求和渲染路径，确认以下客户端缺陷：

1. `reload()` 的临时网络或 5xx 失败直接 `root.replaceChildren(...)`，清除已经确认的商品、收益、当前 tab、佣金状态和可继续读取的游标。临时不可用因此被误呈现为整个页面没有可用事实。
2. 佣金状态切换没有请求序号或请求归属。先选择 A、再选择 B 时，A 的迟到成功响应可覆盖 B 的列表和 cursor。
3. 商品和佣金的“加载更多”按钮都没有 single-flight 或 cursor 身份校验。重复点击可用同一 cursor 并发读并重复追加条目，迟到页也可覆盖新游标。
4. `reload()` 没有授权 epoch。401（包括 bridge 失败）只替换当前 DOM，未清空内存中的受权事实，也没有拒绝较早 `reload()` 的迟到成功响应；旧请求可在登录页后再次渲染原客户的页面。

已确认**不是本次缺陷**：`mutationHeaders(operation)` 已将每项写操作的 idempotency key 保存在页面级 Map 中，未知结果后的同一操作会复用原 key。本项必须保留该行为，不为重试生成新 key。

## 参考与复用

- [PR #274](https://github.com/qianlan33333-png/AI-CRM-v3/pull/274) 定义公开分销生命周期、真实 PostgreSQL/Chromium 旅程和 Provider 默认关闭边界。
- [PR #301](https://github.com/qianlan33333-png/AI-CRM-v3/pull/301) 规定分账成功确认只取同一结算引用的审计事实，不能改称银行到账。
- 复用公共唯一入口 `web/v3/distributionCenter.ts`、`web/v3/distribution.css`、`distributionPresentation.ts` 与现有 `TestPostgreSQLDistributionChromiumJourney`。不新建页面壳、状态标签或平行 H5 运行时；样式继续限定在 `body[data-ui-surface="distribution"]`。
- Product Design 路由当前不可用，未伪造调用；按仓内前端一致性组件索引、公共壳和现有视觉 token 落地。

## 方案与验收

- 按页面读取 generation 与授权 epoch 提交完整的 `/me`、商品、收益、佣金快照。临时失败保留最后确认快照、tab、过滤选择和游标，并显示可重试的“未更新”提示；真实 401 清除敏感快照、使所有旧请求失效，并仅呈现登录入口。
- 佣金筛选改变时建立新的请求 generation；只有当前 generation 且同一筛选值的成功响应可以改列表、cursor 和 DOM。失败保留已确认的当前筛选结果并说明未更新。
- 每个 load-more 路径维护 in-flight/cursor 所属权，触发时禁用同一按钮；只把仍属于当前 tab、筛选和 cursor 的成功页追加一次。失败不清空已有条目或 cursor，按钮可安全重试。
- 写操作继续使用原 `mutationHeaders` key；未知结果只重新读取服务端事实，不创建新 key、不宣称外部效果已成功。
- DOM 合同测试覆盖上述交错响应、401、临时失败、重复 load-more 与未知写请求 key；真实 PostgreSQL/Chrome 仅验证已有公开 read journey 和 375/390/430 宽度的最终可见状态。测试不调用 Provider，不改变 OAuth、购买资格、归因或支付。
