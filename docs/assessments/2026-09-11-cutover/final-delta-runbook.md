# 正式停写、最后增量与切流操作手册（2026-09-11）

**状态：可用于执行前复核；本文没有执行停写、生产导入、服务重启或 DNS 修改。生产数据尚未迁入。**

分类：涉及外部身份与持久化。身份仅走 Identity Port；未知归属保留，不猜 UnionID scope、不把付款人当权益受益人。历史业务由各 Owner 原子导入、保留幂等收据与审计；不重放旧队列，不触发支付、退款、企微写或历史自动化。

## 1. 固定范围与当前证据

- 源：150.158.82.186，数据库 `openclaw_wecom`；目标：124.220.53.183，生产库 `aicrm`。
- 演练库：`aicrm_cutover_rehearsal_20260911`，隔离 API `127.0.0.1:8792`。**以下已通过结果全部来自演练，不是生产。**
- 正式域名：`www.youcangogogo.com`。用户同意短暂停写，开始时间取决于准备进度。
- 已暂存候选 `/opt/aicrm/cutover-candidates/cbffcb3cf1eddc66366ce3b2ac6f045bbab273ba`，360 文件 hash 校验通过；活动生产仍为 `142d58a`。执行前重新确认候选及活动 SHA，不能用某个旧 `/tmp` 二进制代替此版本。
- 历史资金：923 订单/支付、138 退款；115085462 分交易金额、4983370 分源成功退款。独立新 run 再导入已通过，行数不增加。112 付款身份未归属；923 历史受益人为空，不由订单补发权益。
- 问卷：10 定义、1587 答卷、6665 答案；原始摘要、映射和已合法发布定义对账通过。
- 周期会员/领券：95/61 条，156 条原生 Owner/API 读回通过。定义与用户持有记录分别核对。
- 人群：38 包、29248 源成员（14556 在包、14692 已退出）；375 未解析保留；14170 去重后的有效成员。32 归档、6 暂停，历史包不自动运行动态规则。原生逐包/成员 API 107 页读回通过。

上述数字是已有快照基线，**不是最终源库应固定的数量**；最后快照新增/变更必须逐项解释。

## 2. 受保护文件与工具（现场存在）

目标基础目录记作 `B=/var/backups/aicrm/cutover-prep-20260911`。目录0700、文件/密钥0600；不要 `cat` JSON、stream、环境、密钥、回执原文到终端。打印汇总与摘要即可。

| 文件（相对于 B） | 用途 |
|---|---|
| `target-before.dump`、`target-config-before.tar.gz`、`release-before.txt` | 早期生产备份；正式操作前需再备份，不能替代最终备份 |
| `commerce-raw/source.enc`、`commerce.key` | 全量7表原始资金/身份事实；原密文 SHA `4c9cf780ad6a336ede350df19bd8ecb3497bc48a91693ffb0814a07ce2ec1c35` |
| `commerce-normalized-crossrun-v1/{manifest,preconditions,evidence}.json` | 最新已演练的新 run；不能直接用于最新生产 CAS |
| `survey.enc`、`survey.key`、`target-survey-data.key` | 问卷冻结快照、快照密钥、目标数据密钥 |
| `commerce.enc`、`commerce.key`、`commerce-review-before.json` | 商品/周期商品/券定义及受控旧券差异证据 |
| `sidebar-source.stream`、`sidebar-source.json` | 原会员与领券事实 |
| `sidebar-existing-scoped-oa.json`、`sidebar-oa-live.enc`、`sidebar-oa-source.json` | 独立身份验证后的派生会员/券 manifest；OA 证明有一小时有效期，不能过期照搬 |
| `audience.enc`、`audience.key` | 原人群快照 |
| `audience-existing-wecom-proof.enc`、同名 `.key` | 同 Corp 历史外部联系人证据，仅解析已有根 |
| `audience-derived-v3.enc` | **当前演练最新父快照**；SHA `611f67ffdb63ffc149eca094efe1ed36db23ca75a01852f901d0ecf9f0c31ef7` |
| `pending-refund-query.json`、`refund-query-audit.log` | 三条未完成源退款的独立 Provider 只读核验，不回写源历史状态 |
| `sidebar-final-native-api-reconciliation.json`、`audience-api-member-readback.json` | 正常鉴权 API 读回证据 |

现有目标辅助脚本：`/tmp/read_rehearsal_api.py`、`/tmp/read_audience_rehearsal.py`、`/tmp/read-sidebar-rehearsal-api.py`、`/tmp/run-reviewed-rehearsal.py`。这些是**演练脚本**，不得改一个数据库名字就作为正式切流脚本执行。真实业务接口需正常登录及 CSRF；脚本受保护保存响应，仅输出计数。

仓库中可构建的工具：

```sh
node scripts/prepare-donor-source-views.mjs
# 在候选的精确源码版本构建；BIN 是操作人创建的独占受保护目录。
for name in migrate-commerce-capture migrate-commerce-history migrate-survey-v2 migrate-v2-config-definitions migrate-sidebar-history migrate-sidebar-oa-proof migrate-audience-history migrate-cutover-identities migrate-cutover-wecom-provision; do
  GOOS=linux GOARCH=amd64 go build -o "$BIN/$name" "./cmd/$name"
done
```

`BIN`、新一轮目录 `R`、摘要变量必须在执行会话中明确指定；它们是参数，不是声称已存在的远端脚本。数据库连接/Corp/App scope 由受保护的服务器进程环境通过子进程环境或参数传递；不手工拼造 scope，不打印 DSN。每次调用前只读确认 `current_database()`、目标主机及版本；演练必须精确匹配演练库。生产选择要单独记录，禁止隐式默认。

## 3. 停写前硬门槛

1. 再核对完整备份可恢复、候选迁移及导入角色权限。需包含0127、0129、0131、0134、0135及候选其余依赖迁移，按仓库正式迁移顺序执行，不能只挑这五个 SQL。新表权限使用既有迁移角色制度，不能临时授予全库超级权限。
2. 盘点源全部入口：正式域名、IP/sslip 别名、独立端口、后台修改、任务、定时器和内部调用。把具体 unit/入口及停止/恢复责任人记录下来。仓库**没有已验收的通用一键停写脚本**，不要猜 unit 名直接批量 stop。
3. 执行 `docs/assessments/cutover-domain/drain-readonly.sql` 的只读聚合，保存结果。已观察183373旧 consumer 和22392 outbox待办，不能为了“清零”重放或标记成功；须明确隔离旧自动化/效果 worker。未知外部效果、正在发出的退款/消息先处理，不能切后盲重试。
4. 正式 TLS、WeCom 六项成组配置、支付密钥/商户和 App scope 再核对。保留目标现有数据加密/HMAC/session等密钥。只存在候选文件不代表已生效。
5. 用户需决定**微信小店切流后是否继续接收新订单**。目前完整小店订单回调仍由旧 Owner 接收；V3 退款 callback 不是其替代。此项未明确前不得承诺旧系统完全退出。
6. 373 人群候选最近实时企微核验失败，未建客；另2条没有可用历史证明。保留375未解析账本，不能用历史 proof 冒充实时核验。若本轮要求全部可执行人群，需另行解决该缺口；否则明确接受隔离记录及范围。

## 4. 最后增量顺序

### A. 建立停写点 T0

协调停止旧系统新下单、问卷提交、领券/核销、会员修改、后台定义/人群编辑及定时任务；目标仍禁止正式新业务写。对写入口返回明确维护/可重试响应；不能返回成功后丢请求。已发出的支付及 Provider 回调单独处理，见第5节。

记录 UTC 时间、源/目标 SHA、所有写入口与 worker 状态、队列聚合。重新做源/目标数据库与目标配置备份。重新完整提取各领域到**新独占目录**，保留源系统命名、source key，不覆盖旧快照；跨领域提取不共用事务，只有实际停写才能得到一致业务边界。两个快照之间的回调写必须在下一轮收敛。

### B. 定义先行

`migrate-v2-config-definitions --mode extract --commerce-only --snapshot NEW --snapshot-key-file KEY --source-revision EXACT_SOURCE_SHA`，源 URL 从 `AICRM_SOURCE_DATABASE_URL` 注入。

随后同工具 `--mode dry-run` → `--mode apply --confirm-apply` → 再次 `--mode dry-run`检查目标差异，并通过Owner/API逐字段读回；**commerce-only目前不支持 `--mode verify`**。每步绑定该工具输出的 `--manifest-sha256` 与实际 `--actor-admin-user-id`，保留 `--commerce-only`。源36普通商品、2周期商品、15券是基线，最终以源快照计数和逐字段一致为准。

任何既有定义差异先停。source17优惠券历史审查使用单项 `--review-coupon-source-id` 与精确 `--review-coupon-before-sha256`；演练 before 摘要不能用于生产。不能批量覆盖线上编辑、库存/发行计数或旧 slug。

### C. 身份证明独立处理

先复用已核验的同 Corp 外部联系人并 Resolve。新增客户必须由显式 Identity Provision 工具建立，禁止用裸 UnionID/手机号匹配。

两位 OA 主体使用 `migrate-sidebar-oa-proof --mode verify --source PROTECTED_SOURCE --proof NEW_PROOF --key-file KEY --sha256 EXACT_SOURCE_DIGEST`；只有三笔签名查询全部精确匹配 AppID、OpenID、金额、商户单号、SUCCESS 后，才可在有效期内 `--mode apply --proof NEW_PROOF --key-file KEY --sha256 PROOF_DIGEST --confirm-provision`。Provider 失败即停止该身份升级；不影响保留未归属资金事实。

### D. 全量资金快照 → 新 run / 新 CAS → 完整导入

源机使用只读 `AICRM_COMMERCE_SOURCE_URL`：

```sh
"$BIN/migrate-commerce-capture" --mode capture --directory "$R/commerce-raw" --key-file "$B/commerce.key"
```

源、目标分别核验密文 SHA，传输完整目录；目标对最终目标库只读规范化：

```sh
"$BIN/migrate-commerce-capture" --mode normalize --directory "$R/commerce-raw" --key-file "$B/commerce.key" --output-directory "$R/commerce-normalized" --run-key "$RUN_KEY"
"$BIN/migrate-commerce-history" --mode dry-run --snapshot "$R/commerce-normalized/manifest.json" --manifest-sha256 "$MANIFEST_SHA" --history-delta-preconditions "$R/commerce-normalized/preconditions.json" --existing-identities-only
```

dry-run通过后，同一参数改 `--mode apply --confirm-apply`；随后 `--mode reconcile`，然后同 manifest replay验证幂等。**绝不加 `--order-only`。**每次新的源快照都用新 run，并基于实际目标版本重新生成 preconditions。CAS冲突停下重新审查，不能删除收据或改摘要。

全量覆盖微信支付/退款、微信小店订单/退款、支付宝（即使0也证明已读取）。失败/closed/processing/requested退款均保留，只有 completed进入成功退款总额。小店4条returned没有退款证据，继续保留缺口，不能造退款。源退款1的实时SUCCESS与源PROCESSING是两个证据；源2/3的Provider404不变成退款成功。

### E. 问卷

`migrate-survey-v2 extract --source-url PRIVATE_SOURCE_URL --snapshot NEW --snapshot-key-file KEY`；先 `validate`。完整参数通过受保护服务器子进程传递，不在终端日志显示连接串。

`import --target-url PRIVATE_TARGET_URL --snapshot NEW --snapshot-key-file KEY --data-key-file TARGET_KEY --append-only --confirm-import`，再 `reconcile`同参数（去confirm），加 `--append-only --allow-audited-enabled-definition`。仅对已确有可信原 scope 的既有身份使用 `--confirmed-unionid-scope`；禁止为新增裸UnionID伪造scope。

任一旧定义/问题/答卷摘要漂移必须停止，append-only不是任意覆盖。发布沿用原version1定义的 Owner enable审计，保持原slug；不能重建定义为新版本来绕对账。完成后验证原 `/q/{slug}`、授权入口及真实问卷提交；已有历史数量对账不能代替新提交验收。

### F. 周期会员与领券

通过仓库 `scripts/capture-sidebar-history-source.sh OUTPUT`，使用明确的 `AICRM_SIDEBAR_SOURCE_SSH_HOST/USER/KEY_FILE/KNOWN_HOSTS_FILE` 提取新只读stream；不得省略主机密钥核验。

**新增提取工具缺口：现有 `inspect-stream` 强制要求 `--unionid-scope` 以 `wechat-open-platform:` 开头；当前没有独立确认的Union scope，不能填占位值运行。已有 `sidebar-source.json` 的演练成功不能证明这次新stream可安全规范化。需要补充显式无Union scope的捕获模式并测试，或先获得真实scope确认；在此之前停在受保护stream，不生产导入。**

取得合法规范化manifest后，同工具 `--mode bind-external-proof`绑定精确 `--external-proof/--external-proof-key-file/--external-proof-sha256`、已确认`--corp-id --confirm-matched-corp`，需要时加入独立 `--oa-proof/--oa-proof-key-file/--oa-proof-sha256`，输出 `--output-snapshot NEW_DERIVED`。

对派生文件依次 `--mode preflight` → `--mode apply --confirm-apply` → `--mode reconcile`，每步 `--snapshot` 与 `--manifest-sha256` 完全一致。95会员/61领券是基线；新状态变更若触发旧摘要冲突必须停止并评估Owner delta，不把旧行当已完成而忽略更新。资金导入不替代此步骤，不补发历史权益或推送。

### G. 人群最后

`migrate-audience-history extract --snapshot NEW --snapshot-key-file KEY --source-system EXACT_EXISTING_SOURCE_SYSTEM`，只读源连接变量 `AICRM_AUDIENCE_SOURCE_DATABASE_URL`。保留原source namespace；缺失Union scope继续缺失。

使用 `cmd/migrate-audience-history/extract-frozen-wecom-proof.py` 从精确哈希绑定的新原始commerce快照派生独立proof，再 `derive-existing-wecom`。原始业务snapshot不重写；独立derivedAt与proof/hash记录身份解析时点。

- **同一个原始快照重新解析**：必须 `--predecessor-snapshot` 指向目标最新已导入的派生快照。演练当前是 `audience-derived-v3.enc`，不能退回v2。生产首次导入没有该父批次，不能强套演练父链。
- **真正新的源快照**：保留真实新capturedAt及源行摘要，走新业务快照CAS；不能伪造时间来绕过旧批次。

派生后 `preflight` → `apply --confirm-static-import --admin-id ACTUAL_ADMIN` → `reconcile`，每步带 `--snapshot --snapshot-key-file --expected-sha256`。既有package映射复用，375未解析不可静默删除。暂停/归档状态不自动改为启用。

## 5. 支付回调与最后快照之后的新写

**停下单不等于停止支付回调，DNS切换也不是数据库截止点。**在T0前已打开的收银台仍可能付款；小店仍可能发新事件；旧回调可能更新订单、退款、权益、券、outbox。

候选方案保留精确旧资金callback由源5001拥有，目标按固定源IP桥接；普通页面/新下单则由源固定目标IP代理到V3。准确配置与路径见 `docs/assessments/cutover-source-proxy/README.md`及配套nginx/Caddy候选。禁止普通路径回旧站，禁止两方向通配代理形成环路。

执行必须选择并记录：

1. 短暂冻结callback接收：返回可重试失败、不假ACK；等待已在事务中写完成，再最终capture。恢复后Provider重试仍会写源，仍需要后续delta。
2. 继续源接收callback：保持源旧effect/自动化consumer隔离，按新的完整快照持续对账。每轮记录capture时间、表摘要、增量sourcekeys、状态/金额差异、导入receipt、Owner/API读回；资金之外的连带会员/券变化也重提取。

两者都不能以第一次快照完成宣布源退休。需列出尚未终结的旧交易与退款，Provider只读核验并追踪最终状态，确认最后callback之后的新快照/导入收敛；小店新订单持续进入则不存在自然终点，必须先确定native接管/暂停策略。

**当前缺口：尚无已验证的持续增量调度/水位推进/回调退役执行工具，也无已验收的“资金callback写源→会员/领券更新→跨领域delta”完整实测。现有工具可安全发现冲突，但不保证所有更新自动合并。**在这些更新仍可能发生时只能明确保留旧callback Owner与人工受控增量职责，不能宣称完成完全切流或关源机。

## 6. 验收、域名生效及停止规则

每领域必须同时通过：完整源覆盖、未解析/失败分类不丢、金额/状态、幂等/版本链、无Provider效果、原生API读回。正常登录+CSRF访问，不绕鉴权。订单/历史支付含138退款，问卷原slug与历史答卷、客户95会员/61券、人群逐包成员均需检查。归档人群列表默认隐藏：用 `/api/admin/ai-audience/packages/{id}`及其 `/members`逐页读；不存在可依赖的 `include_archived=true`开关。

最终SSL启动候选已暂存 `/etc/caddy/formal-cutover-20260911/`，源代理候选 `/root/aicrm-source-frontdoor-candidate-20260911/`；按source-proxy文档协调单写所有权。先目标正式TLS直连验证，再旧入口固定目标IP代理，再由用户改DNS：

```sh
curl --fail --resolve www.youcangogogo.com:443:124.220.53.183 https://www.youcangogogo.com/healthz
curl --fail --resolve www.youcangogogo.com:443:150.158.82.186 https://www.youcangogogo.com/healthz
```

两者都应为同候选SHA；不使用`-k`。再人工完成微信授权、控制金额下单/支付及可核验回执。付款成功、退款成功、群发/图片发送各自的Provider验收不能由HTTP200代替。用户决定正式开写后再解除目标维护，源普通业务始终保持转发而非双写。

立即停止：摘要/字段/CAS漂移无法解释、未知资金效果、身份归属改变、数据缺失、密钥/scope不匹配、源仍有未纳入入口新写、回调Owner不明、真实登录或支付未通过。停止时保留快照/日志/失败receipt，不删账或盲重跑。

目标尚无新业务写时，可在停写下恢复配对配置和角色；目标已发生新业务写/效果后，不能直接恢复旧库或路由回旧写入口，必须再次停写、核对新账与回执后选择唯一Owner。

## 7. 本轮仍需执行或明确的事项

- 最终停写入口/unit清单与执行、最终源/目标备份、新完整capture、正式迁移/导入/API读回均未执行。
- 生产真实前置/CAS/权限不同于演练；需在实际生产preflight后执行，不能复制演练preconditions。
- `migrate-sidebar-history inspect-stream`仍强制Union scope，缺显式无scope规范化模式；不得伪造scope。commerce-only没有独立verify模式，需dry-run差异与Owner/API逐字段复核。
- 373实时企微失败与2无证明人群成员如何验收；原未知记录仍保留。
- 微信小店新订单需求与旧资金callback留存、持续delta责任/工具和退役条件。
- 正式域名授权、真实提交/支付/受控Provider回执人工验收，及目标开写时机。

本文核对了现有CLI参数、仓库提取脚本、目标受保护文件名和演练记录；没有把缺失的停写/增量编排器伪装成可执行脚本。新增自动编排会涉及真实角色与callback所有权，应在上述缺口明确后单独实现与演练。
