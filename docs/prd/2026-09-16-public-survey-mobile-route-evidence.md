# 公开问卷移动路由验收补齐

日期：2026-09-16
状态：已批准实施

## 业务判断与范围

本项只为已挂载的公开问卷路由补充隔离 PostgreSQL + Chromium 页面证据，不增加页面、接口、业务写入、支付或 Provider 调用。

- **OneID/外部身份**：不新增身份解析、匹配或建客。测试复用现有公开问卷 fixture 已签发的临时 Survey session；OAuth 的成功、失败和回跳仍由 Survey Owner 的既有路径处理。
- **持久化与外部效果**：复用既有独立 PostgreSQL fixture 和既有问卷提交/readback。新增截图只访问已挂载页面或既有 Owner 的失败跳转，不重复提交、不发起 Provider 网络调用，也不新增内部任务。
- **Provider**：禁用真实 Provider；现有 fixture 的本地 OAuth transport 仅用于既有 Owner session 合同，新增证据不增加它的调用。

## 已有参考

- [PR #346：统一公开问卷 H5 展示](https://github.com/qianlan33333-png/AI-CRM-v3/pull/346) 已提供 `public_survey_chromium_*` 的真实 PostgreSQL/Chromium journey、OAuth Owner 和提交 readback。
- `internal/survey/http/handler.go` 是公开入口与 OAuth 失败回跳 Owner；`web/src/h5/controller.ts` 只将 `auth`、`all`、`one`、`result` 连接到实际问卷读取或结果读取。

## 路由分类

| 路由 | 分类 | 依据 | 本次证据 |
| --- | --- | --- | --- |
| `/h5/auth.html` | 实际公开入口 | `/q/{slug}` 由 Survey Owner 进入 OAuth gate 后跳转至该页 | 375/390/430，可见授权失败提示且不触发操作 |
| `/h5/all.html` | 实际公开作答页 | OAuth 完成后按 display mode 跳转 | 375/390/430，既有一次提交/readback |
| `/h5/one.html` | 实际公开逐题页 | OAuth 完成后按 display mode 跳转 | 375/390/430，既有可恢复失败和一次 recovery/readback |
| `/h5/error.html` | 实际 OAuth/identity 失败页 | Owner 的 fail-closed redirect | 375/390/430，通过既有 Owner 受控失败路径进入 |
| `/h5/result.html` | 实际公开结果页 | 已确认提交的结果链接 | 375/390/430，既有 Owner result readback |
| `/h5/`、`loading`、`done`、`signup`、`active`、`expired`、`pay`、`qr` | 构建 carrier / 不适用 | 仅由 `PublicUIBinding` 静态服务；H5 controller 不为其读取业务数据，模板动作禁用。真实服务期入口另为 `/s/{code}`、`/s/{code}/pay` | 记录代码依据，不伪造业务流 |
| 渠道公开移动页 | 不适用 | 当前 composition 只挂载 admin channel UI，channel share URL 为空 | 不新造页面 |

## 验收

在 `AICRM_REQUIRE_CHROMIUM_JOURNEY=1` 与 UTF-8 PostgreSQL fixture 下，现有 journey 对每个实际问卷页面在 375、390、430 宽度验证真实路径、可见内容、无横向溢出和安全的可见操作/失败边界；截图与日志绑定干净 SHA。静态 carrier、测试截图或 fixture 不声明为生产 readback。
