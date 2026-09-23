# 企微侧边栏 JSSDK 回归修复

## 问题与运行入口

PR #164 后，企微客户侧边栏会显示 JSSDK 配置失败、持续“识别中”或把前置握手失败误报为 `external_userid` 缺失。已发布 `5291366b9742030957f48ebf7464a040a3ab46db` 的只读检查确认：`/sidebar/bind-mobile` 正常返回，未持有可信 sidebar 员工会话时正式签名接口返回 401；运行配置存在。没有把后台登录会话视为企微员工身份。

本次实际发布链路是：

```text
web/scripts/build.mjs
  -> web/scripts/build-v3-host-adapters.mjs
  -> web/dist/sidebar/index.html
  -> assets/sidebarHost-<hash>.js
  -> web/v3/sidebar/main.ts + web/v3/sidebarApi.ts
  -> web/scripts/stage-new-shell-ui.mjs
```

已发布 manifest 的 `sidebarHost` entry point 是 `web/v3/sidebar/main.ts`，并包含 `web/v3/sidebarApi.ts`。`web/src/sidebar/main.ts` 与其历史适配器会先被构建流程替换，未被最终 sidebar Host 消费；它们是未运行旧源风险，不能作为线上根因或另建第二套修复。

`/sidebar/bind-mobile` 没有第二个正式别名：Composition 固定以 `web/dist` 创建 Webshell，Handler 优先返回 `web/dist/sidebar/index.html`。只有该构建文件缺失或不可读时，同一路由才回退 `internal/webshell/templates/sidebar.html`。该回退模板也执行 regular/agent 握手，因此使用相同的企微专用 SDK；不改变回退机制、路由或其它静态资产。

已确认的运行缺口如下：

1. 最终 Host 文档此前没有在 Host module 前加载企微专用 SDK。
2. V3 API adapter 已调用正式签名接口，却只保留 agent 签名；V3 main 直接执行 `wx.agentConfig`，没有 regular `wx.config` 和 `wx.ready`。
3. 无查询参数首访在 SDK 失败后会落到缺少 `external_userid` 的错误路径；OAuth start 不应以该客户标识为前置条件。
4. 通用 `https://res.wx.qq.com/open/js/jweixin-1.6.0.js` 的实际官方字节不导出 `wx.agentConfig`，不能单独支撑侧边栏 agent 握手。

## 架构分类

- **OneID：涉及。** 继续使用既有可信 `corp_id + employee_id + external_userid`：sidebar OAuth 会话、WeCom JSSDK、ContextTokenService 和 Identity Port。URL `external_userid` 仅保留为兼容候选，服务端仍从 HttpOnly sidebar 会话导出员工并验证员工与客户关系；不得猜测、建客、合并身份或用后台会话替代企微员工身份。
- **持久化：不涉及。** 不新增表、收据或任务。
- **外部效果：不涉及新增写入。** 只读取得既有 WeCom 签名；现有 `sendChatMessage` 仍要求 JSSDK 成功和逐次既有回执，不创建队列或 Provider 写。

## 固定版本与接口契约

| 环节 | 固定契约 | 实际依据与验证 | 禁止的错误接线 |
| --- | --- | --- | --- |
| 浏览器 SDK | 唯一 `<script>`：`https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js` | 官方资源冻结到 `web/v3/sidebar/testdata/wecom-jweixin-1.0.0.js`，SHA-256 `0ade9f7a4d1adcb626e48a8c87ae4037a4509b9e22262846bd15d3f19ee0cda2`；真实资源 VM 合同覆盖 Mac、Windows、iOS、Android wxwork UA 与 native bridge | 不并载通用 1.6.0；它会先占用 `jWeixin` 且没有 `agentConfig` |
| 最终页面入口 | `sidebar/index.html` → `sidebarHost` → `web/v3/sidebar/main.ts` / `sidebarApi.ts` | Host manifest、`build-v3-host-adapters.mjs`、`stage-new-shell-ui.mjs` 和最终 stage 合同 | 不修未消费的 `web/src/sidebar/main.ts` 来制造假绿 |
| regular 签名 | `GET /api/sidebar/jssdk-config?url=<actual-no-fragment-url>` 的 `config` → `wx.config` → `wx.ready` | `internal/wecom/handler.go`、签名 adapter、OAuth/JSSDK 协议测试 | 不跳过 regular 阶段或把 agent 签名给 regular |
| agent 签名 | 同一响应 `agent_config` → `wx.agentConfig` | 侧边栏 API adapter 保留双签名；真实 SDK 原码 bridge 合同 | 不在 `wx.ready` 前调用，不拿 regular 签名代替 |
| OAuth | `/api/sidebar/oauth/start?next=…`，`/api/sidebar/oauth/callback?code=…&state=…` | 当前 Go handler 和协议旅程 | 不要求或传递 `external_userid` 来创建员工 OAuth 会话 |
| ContextToken / 客户读取 | `getCurExternalContact` 成功后才 bootstrap | 当前 sidebar bootstrap 与 OneID/员工关系验证 | 不以后台 session、URL 值或 HTTP 自报身份替代可信 tuple |
| 兼容 query 候选 | 带 `external_userid` 查询参数可保留既有只读降级入口 | 当前 V3 initialize 与服务端关系校验 | 不把候选参数当已验证客户，也不删除合法本地只读降级 |

SDK 合同的真实 bridge 顺序为 regular `preVerifyJSAPI`、`agentConfig`、`getCurExternalContact`；Windows 还会调用 `wwapp.initWwOpenData`，iOS/Android 还会调用 `getNetworkType`。这些由官方 SDK UA 分支决定，测试只将它们作为资源兼容性证据。

## 修复行为

1. 最终 Host 在 module 之前只插入上述企微专用 SDK；stage 测试检查唯一脚本、顺序、V3 Host entry 与 adapter 闭包。
2. API adapter 保留 regular 和 agent 两套签名、原始 agent ID 和 exact no-fragment URL。
3. 初始化遵循 `wx.config -> wx.ready -> wx.agentConfig -> getCurExternalContact -> bootstrap`。
4. 同一 URL regular 成功后，显式 agentConfig 失败可只重试 agent；每次重试直接从正式接口取得并校验新的双签名，不依赖 sessionStorage 清除成功，且必须匹配已确认的 corp、agent、URL。regular 成功的全局 SDK 状态不被错误重配；缓存被禁用或抛出 SecurityError 时只退化为网络读取。
5. regular 或任一 SDK 超时会令当前 document 状态不确定，真实 UI 只提供“重新打开 Sidebar”，不让旧回调跨轮改变状态。正常失败显示单一阶段与真实“重试读取”入口；请求中的“重新读取”也递增初始化代际，迟到外部联系人回调不会发 bootstrap。
6. 无 query 首访只有 SDK 成功后才读取外部联系人并 bootstrap；401 展示已有 OAuth 入口。带 query 的兼容候选仍可由服务端验证后保留只读 `degraded_ready`，发送保持禁用。

## 验收

- `scripts/sidebar-wecom-jssdk-contract.mjs` 校验官方固定资源 SHA、四种 wxwork UA、regular ready 保持及 bridge 握手。
- 最终 staged DOM e2e 校验专用 SDK 唯一加载、V3 Host 闭包和完整双签名顺序；不允许退回退休 JSSDK 路由。
- SDK 未载入、regular 失败、agent 失败、外部联系人失败、401、agent 签名重取、缓存删除/读取被拒、URL/identity 不匹配、迟到联系人回调都断言实际 UI 和请求边界。
- Go 协议旅程覆盖无 cookie 的 401、OAuth start/callback、可信 sidebar cookie、双签名、既有 OneID 客户和 ContextToken 签发。
- PostgreSQL + Chromium 用官方 SDK fixture 与 mocked native bridge 覆盖成功/失败握手；不产生 Provider 写入。

官方 SDK、模拟 native bridge、四种 wxwork UA 与 Linux Chromium 仅证明资源、协议和最终 Host 的回归合同，**不等同于用户企微 WebView 的现场成功**。本机原生企微 WebView 的 Computer Use 访问未获批准；本次不会绕过该限制，上线后仍需由获授权设备做现场握手复验。
