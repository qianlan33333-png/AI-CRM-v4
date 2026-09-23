# Radar 公共页同源事件 CSP PRD

日期：2026-09-16

状态：待实现审核。

源码基线：`d2eda00380fa3fee94bbd303398228a20bcd2894`。

## 业务判断与范围

用户打开 `/r/{public_code}` 的图片或 PDF 内容后，页面已有 `track()` 会向同源 `/api/public/radar/{code}/events` 写入既有的内容打开阶段。当前 viewer 的 CSP 是 `default-src 'none'`，没有显式 `connect-src`，因此浏览器按 default-src 拒绝该 fetch，造成未处理的 `TypeError: Failed to fetch`，并丢失既有 Radar Owner 的事件回执。

OneID：既有公开访问会话可能携带已经解析的身份归因，本次不读取、解析、绑定或创建身份。

Persistence：复用既有 Radar Owner 的事件记录 Unit of Work；不新增表、迁移、任务或重试。

External Effects：不涉及。该同源事件只生成既有本地 Radar receipt，不调用 Provider。

范围仅为 `internal/radar/http/handler.go` 的 viewer CSP 加 `connect-src 'self'`，以及同一 Handler 的 HTTP 合同回归。保留 `default-src 'none'`、`img-src 'self'`、`frame-src 'self'`、inline script/style、`base-uri 'none'` 和 `form-action 'none'`；不新增跨源连接、脚本、frame 或身份能力。

## 既有链路与参考

链路为 `/r/{public_code}` → `radarhttp.Handler.open` → 已有 `viewerHTML.track()` → canonical `/api/public/radar/{code}/events` → 已有 `event()` → `PublicService.Record`。该事件端点已有同源检验、session cookie、HMAC event token 和 Radar Owner 回执；不能以 CSP 修复绕过这些检查。

GitHub 本仓参考已核实：`d2eda003` 的 `cmd/aicrm/composition.go` 使用显式 `connect-src 'self'` 维持同源连接同时不放宽其他 source。没有发现适用于 Radar viewer 的现成特例，因此采用该最窄指令而不复制页面壳或客户端逻辑。

前端基线：Radar 公共 viewer，服务端生成的既有单页，不涉及管理端 `admin_base` 或新增组件。组件索引只记录 Radar 管理端素材 picker，没有可复用的公共 viewer CSP 组件。Product Design catalog 在本会话不可用，未作为已完成步骤声明。

## 验收

1. `GET /r/{public_code}` 的成功 viewer CSP 含唯一 `connect-src 'self'`。
2. CSP 保留所有原有 source 指令；不出现 `*`、跨源 URL、放宽后的 script 或 frame 指令。
3. HTTP 回归证明 viewer 仍输出既有同源 event URL；实际 UTF-8 Chromium 验收由 remaining-pages tree 合并本修复后运行，覆盖三个公共页和一个后台页，并确认 Radar 图片/PDF 事件网络请求为 200，且 Radar Owner 记录 `image_loaded` 或 `pdf_opened`。
