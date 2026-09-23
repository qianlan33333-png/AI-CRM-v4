# 持续治理：变更影响、行为保持和安全巡检

## 业务判断与交付范围

用户要减少“修改 A 后 B 失效”“新配置覆盖旧能力”，并让源码未改变时的新漏洞仍然被发现。本期复用现有完整 CI、GitHub Actions 和 Go 工具链：增加能力影响报告、当前版本作者声明、安全扫描及每周报告，不引入付费服务或常驻基础设施。

本改动不涉及 OneID 解析、客户归属或 Provider 调用；不产生业务持久化、内部持久任务或外部效果。生成的 CI 诊断 artifact 保留 30 天。业务事实、支付收据、数据库清理、生产巡查和备份均不属于这些脚本的写入范围。

本期成功条件：漏登记生产目录失败；变更能找出共享依赖的消费者和既有保持性测试；高风险作者声明绑定当前 PR head；安全扫描失败、不可用和 skip 不能变绿；保持既有五条完整验证 lane；每周重新查询漏洞库而不自动升级依赖或修复业务。

## 能力影响如何运行

`capability-impact.json` 登记能力、负责人角色、风险、路径、依赖和真实测试。现有负责人仅是角色分工，尚未分配个人时不等于已有个人接受责任。所有受控生产路径都必须匹配能力，不能通过空 required_roots、excluded_paths 或 `internal/*` 吞掉新增领域。

`governance_impact.py` 读取 base 到已提交且干净的 tested HEAD 的 Git diff，从实际 Go import 补充消费关系，再沿“消费者依赖共享能力”的方向求影响闭包。手工依赖用于前端、API、迁移和交付链等源码 import 无法表达的关系。报告给出直接能力、受影响消费者、依赖来源、测试文件/函数、head、tested SHA 和 tree。删除测试、过期路径和未知依赖也会失败。

这是一份保守的领域级最小注册表，不能证明已穷尽每个动态调用或业务语义。新增能力应细化登记、保持性断言和消费者；新增领域必须显式登记。影响报告帮助审核，**不会用来裁剪完整测试**。backend 仍执行既有全量测试，Host ChromiumJourney 仍由现有自动发现执行，不恢复手工浏览器正则。

真实业务保持性用例已映射到完整 lane：

| 风险 | 真实测试 | 实际断言 |
| --- | --- | --- |
| 修改商品映射时覆盖无关字段、旧状态回写 | `internal/product/app/external_push_test.go: TestCommerceMappingExplicitSwitchPreservationCASAndReplay` | 未显式设置时保留 field mapping；显式清空可执行；旧版本 CAS 被拒绝；幂等重放保持结果 |
| 旧页面覆盖较新的运行配置 | `internal/config/app/setup_wizard_test.go: TestSetupWizardSaveRejectsNewRequestWithStaleDigest` | 陈旧 digest 拒绝，较新的配置仍保留 |
| 批量保存与单字段编辑交错 | 同文件 `TestSetupWizardBatchAndManagerSetSerializeOnSharedAdvisoryLock` | 两种更新共用串行锁边界 |
| 暂停能力被配置编辑重新激活 | `internal/groupops/app/paused_webhook_test.go: TestPausedWebhookConfigurationPreservesLifecycleCASAndReceipt` | 配置保存保持生命周期、CAS 和收据 |

## 风险声明与独立审核边界

高风险 PR 必须填写模板的三个单行字段：`Governance-Head`（完整 40 位 PR head）、`Governance-Preservation`、`Governance-Validation`。声明至少写清保持的效果、受影响消费者和实际验证边界。脚本拉取 PR 当前正文，因此正文修订后重跑 job 即可；追加提交后必须刷新 head。规则自身的改动强制高风险，即使注册表把其风险降级。

作者声明只证明声明已填写，无法证明内容真实，也**不等于独立批准**。报告固定标记 `independent_review=unverified`，另只读核验 main 保护分支；无权限时报告 unknown，不默认安全。

2026-09-18 只读核验发现 main 要求严格 `check`，但 required_approving_review_count=0、require_code_owner_reviews=false、require_last_push_approval=false。因此“独立审核强制执行”是部署启用缺口，本提交没有宣称已经完成。要落实独立审核，仓库维护者需启用至少一位独立 reviewer，并开启新提交后失效/最近推送批准策略，或使用等效的独立受保护规则。规则与 workflow 本身可被有写权限的人修改，单靠仓内脚本无法提供独立信任根；不以伪造批准、作者自证或永远无法满足的 job 代替这项配置。

## 安全门禁和债务

三个 CLI 的源码版本固定在 `scripts/ci/governance-tools.json`，使用 Go checksum database 安装，并检查实际二进制 Go module/version；不使用 latest 下载脚本、付费私库 SARIF 或持续运行服务。

| 工具 | 固定版本与来源 | 执行范围及局限 |
| --- | --- | --- |
| oasdiff | [v1.11.7](https://github.com/oasdiff/oasdiff/releases/tag/v1.11.7) | API 有改动时与 PR main base 比较，WARN 及以上 breaking 阻断；只检测规格变化，无法发现实现违背规格或业务语义变化。选择兼容仓库 Go 1.26.6 的版本，升级工具要审核并回归 |
| govulncheck | [v1.1.4](https://github.com/golang/vuln/releases/tag/v1.1.4) | 扫描已知 Go 源码可达漏洞；新增可达符号阻断，存量只接受明确批准且未过期的例外。不是完整安全审计或前端依赖扫描 |
| Gitleaks | [v8.30.1](https://github.com/gitleaks/gitleaks/releases/tag/v8.30.1) | PR 扫新增 commits，每周扫完整 Git 历史；`--redact=100`，报告不输出 secret 内容。依赖规则的启发式检测不能证明无秘密 |
| npm audit | Node 24.18.0 / npm 11.12.1，与现有 CI 一致 | 扫描所有当前 Git 树中的 package.json/package-lock.json 成对项目，包括 dev/optional/peer；所有严重等级和未批准存量均阻断，不做 reachability 推断 |

`governance_npm.py` 在只含 manifest/lock 和空配置的临时目录运行 `npm audit --package-lock-only --ignore-scripts`，不安装依赖或运行生命周期脚本，不能被仓库 `.npmrc`、`omit=dev` 或 audit-level 环境变量缩小范围。每次重新查询官方 registry；输入改变时也扫描 base 的 lock，给出新增、存量和修复计数。输入未变、同树 CI 复用、每周源码未改也重新扫描。npm reportVersion、完整 metadata、实际 findings 与退出码必须一致，空/错误/截断报告无法通过。历史冻结证据 `.frozen.json` 仅用于字节校验，既不安装也不作为当前执行依赖；实际活跃项目不能没有 tracked lock。

2026-09-18 的前端工具治理将当前执行工具固定为 [Orval 8.33.0](https://github.com/orval-labs/orval/releases/tag/v8.33.0)、esbuild 0.25.12、js-yaml 4.3.2；fast-uri 在 lock 中解析为 3.1.8。依据维护者的 [Orval 生成期文件/网络读取公告](https://github.com/orval-labs/orval/security/advisories/GHSA-cxq5-97v7-87j8)、[js-yaml 合并资源消耗公告](https://github.com/nodeca/js-yaml/security/advisories/GHSA-2883-xcg3-v3hh) 和 [esbuild 开发服务公告](https://github.com/evanw/esbuild/security/advisories/GHSA-67mh-4wv8-2f99) 升级；不把工具仅在开发时使用当作豁免。Orval 使用[官方 v8 迁移配置](https://orval.dev/docs/versions/v8/)，正式重新生成两个客户端，保持显式 fetch 和 combined-type aliases。

根 package.json/package-lock.json 现由 V3 build and security maintainers 负责安全升级。`docs/donor-manifests/v3-toolchain-ownership.json` 只迁移这两份执行文件；原供体完整内容保存在 `toolchain-v2/*.frozen.json`，原 PR01 清单、PR03 内嵌 hash 和旧供体不改写，其他供体前端字节门禁保持。独立校验与恶意回归会拒绝修改历史字节、重写历史 hash、把第三个源文件加入豁免或使用浮动工具版本。历史证据不是依赖豁免；运行工具锁文件仍必须通过真实 npm 门禁。

govulncheck 的 [JSON 模式即使有漏洞也返回 0](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)。本门禁解析连续 JSON 对象，按包含函数的 trace 判断源码可达性；不以进程 exit 0 当作无漏洞。Go 输入改变时用相同工具分别扫描 base/head；Go 输入相同时明确标为 identical_go_inputs。空输出、截断、工具版本不符和扫描异常一律没有 clean claim。

<a id="security-debt"></a>
`security-debt.json` 是可追踪债务，不是自动豁免名单。首次扫描发现 `GO-2026-5026` / `golang.org/x/net/idna.(*Profile).ToASCII`，官方条目未给出严重等级，因此登记为 not_published，不编造评级。集成提交 `a8c089c8fb3dde3e6c7d32462092dc71f00be496` 已将 x/net 固定为修复版本 v0.55.0；后续真实 govulncheck v1.1.4 / Go 1.26.6 的完整 JSON stream 解析得到 `reachable={}`，SBOM 也确认该版本。台账改为 `remediated_locally`，保留原发现、Owner 与证据，不删除历史记录。原 2026-10-02 日期仅为修复跟进期限，没有批准或豁免意义。扫描原件 `/tmp/aicrm-governance-upgraded-vulns.json` 的 SHA-256 为 `0026bca6d4c5f2bdeb0bc924984be5e70a40fe3480732f911179cd56162655a4`；该原件未内嵌完整 Git HEAD/tree，因此不能用于宣称之后所有集成修改均已通过安全扫描。

npm 台账另登记工具链本地修复：基线有 16 个受影响 package 条目（1 critical、14 high、1 moderate，包含传递依赖，不能说成 16 个独立漏洞），在干净已提交 `7acbed6453c7c10ebd236c97975137f70049768a` 上，增量扫描和每周扫描模式均得到根项目与 `web/v3` 为 0。集成提交为 `63ebf9dabdfb6c64f3a2777260a4a084cb314c9b`，后续为OpenAPI校验显式加入Swagger Parser 13.0.0，根依赖锁已变化；早先扫描不代表最终集成HEAD。具体原报告路径与摘要在台账内，最终版本重新扫描。此结果没有 npm 例外，也不表示 registry 未收录的漏洞不存在。最终集成版本的完整 GitHub Linux CI、实际生产版本仍须分别验证。

已有可达漏洞的例外需状态 approved_timeboxed_exception、具名 Owner、原因、跟踪链接、批准者及批准证据，且最长 30 天并未过期。新增可达符号不能沿用存量例外。台账中的批准证据仍需独立审核真实性；字符串校验不是对远端 review 的密码学证明，也不能解决上面的保护分支缺口。

每周 workflow 在 main 当前源码和最新漏洞库上重跑，不因“源码没改”复用旧安全结果。发现问题或扫描不可用会使该工作流失败并保留脱敏 artifact/summary，遵循 GitHub 的现有通知设置；只报告，不自动发消息、开 PR、升级依赖或修复业务。

## 执行与验证

常规 `CI / check` 先要求新增 governance job 实际 success，再执行原有 `verification.py gate`；原有 preflight/backend/frontend/browser/archive-sdk 的全量执行或经 API 验证的同树 main 复用保持不变。安全 job 即使 main 复用也会重新执行；新增关键 lane 的 skipped/cancelled/failure 均不能放行。安全扫描与声明失败分开产出证据，方便一次看到多个待办。

本地在干净已提交树运行（BASE 为要比较的提交；产物放工作树外）：

```sh
python3 -m unittest discover -s scripts/ci -p 'test_*.py' -v
python3 scripts/ci/governance_impact.py --base "$BASE" --out /tmp/aicrm-impact.json
python3 scripts/ci/governance_security.py install --tools-dir /tmp/aicrm-governance-tools
python3 scripts/ci/governance_security.py scan --base "$BASE" --tools-dir /tmp/aicrm-governance-tools --out /tmp/aicrm-security
python3 scripts/dev_preflight.py fast
```

`test_governance.py` 对恶意或错误规则做回归：漏领域/空覆盖/通配吞新领域、删测试/陈旧映射、把规则改动降风险、陈旧/重复/空作者声明、关键 lane skip、JSON exit 0 有漏洞、空/截断扫描、错误二进制版本、新可达漏洞借存量例外、未批准/过期/无 Owner/无限期例外。实际业务保持测试沿用上面的真实应用用例，不复制业务实现写镜像测试。

本地专项通过只能证明对应范围；完整 GitHub Linux CI、保护分支审核配置及生产启用分别验收，不能互相替代。
