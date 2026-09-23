# PRD：后台内容哈希静态资产缓存

## 结论与目标

正式后台的代表页实测显示，内容雷达的业务读取接口首字节约为 62--65ms，页面 HTML 约为 179ms；同次加载却会下载多份内容哈希 JavaScript/CSS，单个 chunk 最大约 175KB，且这些 `/assets/` 响应均为 `Cache-Control: no-store`。本 PR 只消除这类可验证静态资源的重复下载，不缓存业务数据、HTML、会话或 API 响应。

目标是让已登录管理员在第二次进入同一后台页面时命中浏览器缓存的内容哈希 runtime assets，同时保持发布后立即加载新 bundle、未登录访问仍受会话保护、客户/订单/身份/令牌等数据绝不进入共享缓存。

## 开发前分类

```text
OneID: not involved。仅处理构建期静态 bundle 的传输头，不读取、解析或写入客户身份。
Persistence: stateless。只在 HTTP 响应上设置静态资源缓存策略；不创建表、任务、队列或外部效果。
External Effects: not involved。没有 Provider 调用、意图、收据或重试。
```

## 事实与参考

- v3 的 `web/scripts/build.mjs` 已采用内容哈希 `entryNames`、`chunkNames` 和 `asset-manifest.json`；`web/scripts/performance-budget.mjs` 已对初始资源链设预算。
- `cmd/aicrm/composition.go` 当前把通用 `/assets/` 交给管理员会话门控后的 `externaleffects.UIHandler`；该 handler 对成功资源也无差别返回 `Cache-Control: no-store`。
- v3 Sidebar 的 `/sidebar-assets/` 已只服务构建产物，并使用 `public, max-age=31536000, immutable`，可作为同仓安全先例。
- 旧仓 GitHub 提交 `597054b7c65eb1ce65294172d00e686ccc0314f5`（`perf: 优化 CRM 与企微侧边栏加载链路`）采用内容哈希 ESM 分包、manifest/预算检查，并只对 hash assets 设置一年 immutable；HTML 保持重新校验、API 保持私有不缓存。
- 生产 Caddy 当前将请求反向代理到应用；没有独立的 `/assets/` 缓存规则。因此本 PR 必须由应用按响应类别设置头，不能假定边缘层会修正 `no-store`。

## 用户可观察行为

1. 已登录管理员第一次打开后台页面，仍取得该发布对应的全部 runtime assets。
2. 在未发布新版本时，再次进入该页，内容哈希 JS/CSS 从浏览器缓存命中，不重新请求或下载。
3. 新发布生成新 hash URL；HTML 仍不缓存，因此会引用新 URL，客户端不会继续执行旧 bundle。
4. API、HTML、登录、会话失败页、403、404 和任意非静态响应仍保持现有 `private, no-store` 或 `no-store` 语义。

## 最小实现范围

1. 在 `internal/webshell` 提供一个通用的后台 runtime asset handler，读取已构建的 `asset-manifest.json` 并在进程启动时建立允许列表。
2. 仅当请求路径同时满足以下条件时才按长期静态资源返回：
   - 路径位于 `/assets/`；
   - 规范化后无路径穿越；
   - 对应 manifest `files` 中的构建输出；
   - 文件名符合构建器输出的内容哈希命名（入口、chunk 或 file），而不是仅因位于 `/assets/`；
   - manifest 中的 SHA-256 与实际文件一致；
   - 成功读取并确认 MIME 为允许的静态类型。
3. 对上述成功 `200`（及等价 `HEAD`）响应返回：
   `Cache-Control: private, max-age=31536000, immutable`，并保留或补齐稳定 `ETag`。初版不引入 gzip/zstd；若部署已有压缩 middleware，必须沿用其既有变体协商并保留正确的 `Vary: Accept-Encoding`，否则不发送该 `Vary`。
4. 保留管理员会话门控。未认证/过期会话的重定向或拒绝响应不得取得长期缓存头；权限 middleware 的现有安全头和状态码保持不变。
5. 非哈希标准组件、旧兼容脚本、未在 manifest 的文件和 manifest 本身不进入本 PR 的长期缓存集合，继续现有无缓存策略。它们日后须先接入内容哈希构建或版本化发布契约，才可申请缓存优化。
6. 由 Composition Root 将 `/assets/` 交给该静态 handler；`externaleffects.UIHandler` 仍仅负责其页面路由，不再成为全后台 bundle 的通用文件服务器。不得改变任一业务模块的 API、数据库或外部效果路径。

## 失效、权限与数据边界

- 失效由内容哈希 URL 保证，而非 TTL 或人工 purge；新发布改变内容即产生新 URL。
- runtime assets 是公开可复用的代码/CSS/图片文件，不得含用户资料、API 响应、令牌、环境变量或按会话生成的内容。实现会以 manifest 白名单避免把任意磁盘文件误当静态资源。
- `private` 只允许浏览器的本地缓存复用已验证的成功静态 bundle；管理员会话门控继续在每个网络请求前生效。认证失败响应不可带长期缓存头，避免缓存登录重定向、错误页或权限差异内容。
- HTML 继续 `private, no-store`，所以每次进入后台都会取得当前发布的 hash 引用；API 与业务读模型不增加浏览器、Caddy 或服务端缓存。
- 该 PR 不引 Redis、CDN、新进程、队列或数据库迁移。

## 验收与证据

1. 单元/HTTP 合同：manifest 中的 hash asset 成功返回 `200`、正确 MIME、private immutable Cache-Control 和稳定 ETag；若现有 middleware 压缩响应，验证其 `Vary: Accept-Encoding` 与实体变体一致。
2. 负向合同：未认证、403、404、路径穿越、非 hash、manifest 外文件、manifest SHA 不符或进程启动后文件被替换的请求均不返回 immutable/ETag 缓存头；HTML、API 和 `asset-manifest.json` 保持现有语义，未在 manifest 的旧资源仍可由兼容 handler 返回 `no-store`。
3. 发布合同：构建生成的 HTML 只引用 manifest 中的 hash asset；改变输入后的新构建产生不同 URL；现有 `performance:check` 继续通过。
4. 浏览器冷/热对比：同一已登录会话以空缓存打开一个代表后台页记录 JS/CSS 请求数、传输字节、load 时间；不清缓存再次打开同页，确认所有可缓存 hash assets 的 `fromDiskCache`/`fromMemoryCache`，并记录减少的网络传输字节与页面可用时间。两轮均记录 API 的请求数和耗时，证明没有借缓存掩盖业务读取。
5. 正式只读回查：发布后检查一条 hash asset 的响应头、后台 HTML、一个 API 响应和未认证资源请求，分别符合上面的缓存边界。

## 风险与止损

- 若 manifest 不能完整覆盖某个正在使用的 runtime asset，保持它无缓存而不是猜测；修复构建映射后再纳入。
- 若发现 bundle 含 PII、token 或按会话数据，立即停止该路径的长期缓存并报告；不以移除认证来换取性能。
- 若压缩实现无法保证 `Vary`/ETag 一致，先只发送未压缩实体和正确缓存头，或复用已有安全 middleware；不可部署错误变体缓存。
- 若热加载测量仍慢，下一轮再按真实瀑布调查页面级重复 API 或慢查询；本 PR 不预设其存在，也不扩大到读模型缓存。

## 非目标

- 不缓存客户、交易、会员、标签、渠道、订单、运营计划或任意 API 数据。
- 不变更 OneID、鉴权规则、外部效果、支付、Provider 调用或任务执行。
- 不重构后台页面、合并业务接口或引入新基础设施。
