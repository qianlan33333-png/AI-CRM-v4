# 按行为检查与构建：明细方案

> 2026-09-29 实施方案，对应 `docs/prd/2026-09-29-routine-checks-by-behavior.md`。下文是目标与逐项验收表；实际生效状态以准确候选 SHA、预发/生产收据为准。截图提供构建失败现象，未代替服务器日志读回。

## 一、从零设计的最短路径

每次发布只保留六步：**准确差异 → 相关断言 → 一份完整构件 → 预发本次业务读回 → 人工确认 → 同包生产读回**。一次选择结果列出 base/head/tree、变化行为、直接消费者、测试命令、构建命令和预发旅程。发布者核对实际输出；系统不要求人工给出“高风险”标签、GitHub/PR ACK、影子样本或提速证明。

从第一性原理看，只有五项不能再删的技术证明：**被测源码是将要发布的源码；相关测试确实运行且能发现错误；构件内容与来源正确；预发验证的是已安装构件；生产运行的是同一构件。** 人工确认是预发之后、生产写入之前的授权决定，不充当技术证明。编译成功、测试命令退出零、文件 checksum 自洽、HTTP 200 或安装收据各只能证明其中一部分，不能互相代替。

| 步骤 | 输入与动作 | 唯一结果 |
| --- | --- | --- |
| 选范围 | 测试从候选 base→head 比较；构建从已安装应用源码→head 比较；核对 Go import/embed、Web 与动态资源、路由、配置、数据/Provider 合同 | 本次测试、Go 程序和 Web 是否重建 |
| 查行为 | 运行改动测试、直接消费者及必要跨边界合同；核对预期测试确实执行且没有 skip | 相关通过/失败/未验证及原始日志 |
| 造构件 | 在本 attempt 私有目录，复用来源已核实的基包；重建受影响 Go 命令，Web 变化时完整构建 Web 一次 | 单一完整构件、引用闭包、文件清单和摘要 |
| 装预发 | 安装该包并读回本次功能、版本、服务与健康 | 预发真实结果 |
| 人工确认 | 展示 base/head/tree、测试结果、构件摘要、预发业务读回和未验证项；等待明确决定 | 与准确候选及构件绑定的晋级授权，或保持待晋级 |
| 晋级生产 | 安装同一包，独立读回摘要、版本、服务、健康及适用业务输出 | 生产真实结果 |

预发全部完成后停止自动流程，形成可审阅的待晋级结果。确认前不得安装生产、更新生产源码游标或执行其他生产写入；等待多久都不自动同意。确认绑定这一个候选、构件摘要和预发收据。确认后临执行前再核对候选/构件身份，并从预发运行服务重新读回安装包与健康；任何变化都重新走相关检查与预发并再次确认。确认期间保持候选队首位置，但不长期占用执行锁；用既有候选/尝试/收据记录等待结果，不另造审核系统。

`cmd/aicrm` 是 Composition Root，继续负责启动与接线。其文件变化仅触发对应代码编译和合同；业务逻辑以后碰到时搬到 owner 包。无需为了启用简化先拆完整个目录。

## 二、现状事实

- 裂变候选 `07f1c98` → `c6f2f34` 的 Go 依赖图只选 `cmd/aicrm`，现行 high/protected 规则却转成五车道全量；229 个 Go 包是全量执行数，不是影响闭包。`cmd/aicrm` 有 72 个 Go 源文件、146 个 Go 测试文件；每 30 个测试一组的 `cmd_test_groups.py` 仍测整包。
- 当前前端车道跑 Orval、OpenAPI、Excel、媒体、标签等检查，却未直接选 `web/v3/referralCenter.test.mjs`。34 个登记检查由能力表反向依赖扩出，未证明它们都消费本次页面修改。
- CPU profile 失败于元数据严格 `>=5s`（记录 4.999198696 秒）；服务期页面在查询发起后固定等 30 毫秒，断言时按钮仍处加载态。两项尚待准确基线在同环境复现。
- 截图报告第二次 attempt 在构建页面时遇到已有目录；旧指挥台随后用第三次 attempt 和临时构建器修补安装了预发及生产。代码中的直接冲突是 `scripts/domestic_release_build.py:_build_frontend` 先创建 `release/web/dist`，而 `scripts/stage-pr01-effects-ui.mjs` 拒绝任何已存在目标；完整构建的 `stage_frontend` 会先删该目录。须用该 attempt 日志和隔离重放确认现场与代码路径一致。

## 三、检查项逐项去留

| 现有项 | 新方案 | 依据与改动点 |
| --- | --- | --- |
| `cmd/aicrm/*`、`internal/platform/*` 等固定 high/protected | **删除静态全量触发**；依赖和行为决定范围 | `docs/governance/capability-impact.json`、`scripts/ci/impact_selection.py` |
| 能力表宽泛 `depends_on` 反向扩张 | **删除作为必跑依据**；保留真实跨域合同关系即可 | `scripts/ci/governance_impact.py`、能力表 |
| 五车道 preflight/backend/frontend/browser/archive-sdk | **删除日常整车道门禁**；执行本次命令清单 | `scripts/ci/quality_lanes.py`、`scripts/domestic_main_release.py` |
| 每能力挂一个历史测试名当覆盖证明 | **删除代理断言**；纳入改动测试、直接测试和实际用户旅程 | 能力表、`scripts/ci/affected_plan.py` |
| `cmd/aicrm` 命中即跑 146 个测试文件，或每 30 个分组 | **按具名相关测试运行**；保留 `cmd_test_groups.py` 已有的 JSON 事件审计思想，确认所选测试各跑一次且无 skip；业务逻辑逐步移到 owner 包 | `scripts/ci/cmd_test_groups.py`、`scripts/ci/quality_lanes.py` |
| 普通候选 `go vet ./...`、`go test -race ./...`、229 包 | **去全仓常规运行**；改动包/消费者有并发风险时跑对应 race | `scripts/ci/quality_lanes.py`、`scripts/dev_preflight.py` |
| 前端一改就跑所有 Orval、Excel、媒体、标签脚本 | **只跑实际入口和合同**；生成客户端或 Excel 变动时才跑对应专项 | `scripts/ci/quality_lanes.py` |
| 所有 ChromiumJourney、归档 SDK | **按本次旅程选择**；归档依赖变化才测 SDK | `scripts/ci/quality_lanes.py` |
| 后端、前端、浏览器各自准备 npm/Excel 环境 | **按输入准备一次并复用**；不为无关检查安装依赖 | `scripts/ci/check_preparation.py`、发布控制器 |
| GitHub PR 审核查询、发布前人工 ACK、影子十 PR/计时对/30% | **从国内日常路径删除**；只保留预发结果完成后的生产晋级人工确认 | `scripts/ci/local_first_gate.py`、`scripts/ci/affected_shadow.py`、旧 PRD |
| 每条 lane 前后重复要求工作树干净 | **删重复检查**；构建/测试统一从固定 SHA 的不可变 checkout 执行 | `scripts/dev_preflight.py`、发布控制器 |
| CPU 元数据必须不少于配置五秒 | **改成与外部实耗时宽范围相符**；允许毫秒偏差，拒绝 1 纳秒等明显错误值；保留可解析、非空、脱敏和权限 | `internal/platform/diagnostics/cpu_profile_test.go` |
| 服务期页面请求发起后固定睡 30 毫秒 | **有截止时间地等待查询完成或明确失败**；断言微信提示、禁用和个人信息不可见，长期加载/网络错误失败 | `internal/product/http/service_period_public_journey.mjs` |

这些删除不改变身份归属、鉴权、事务、幂等、外部效果、迁移的具体业务断言。相关失败退回原任务；意外跑到的疑似无关失败，在相同环境复现准确基线并核对调用关系后单列。证据不足保持未验证，不凭失败幅度小就放行。

## 四、构件与打包逐项去留

目标结构只有四块：**受影响 Go 程序构建、Web 完整构建一次、静态/迁移载荷准备、最终构件汇总**。每块只写自己的临时输出；最终汇总核对文件集合、路由/静态与动态 import 引用闭包、类型和摘要，再原子提交完整包。分包脚本不拥有总目录，也不附带其他业务测试。已有页面清单同时限定公开资源和后台私有资源，删除前须以真实路由授权与引用闭包替代；缺少证据时保留这一安全边界。

| 现有项 | 新方案 | 依据与改动点 |
| --- | --- | --- |
| 增量构建先 `mkdir release/web/dist`，首分包要求不存在 | **消除冲突**；一个总编排创建本 attempt 工作区，空目录存在可以使用 | `scripts/domestic_release_build.py:_build_frontend`、`scripts/stage-pr01-effects-ui.mjs` |
| 完整构建的 `rm -rf release/web/dist` | **只清理由本 attempt 拥有的临时目录**；未知旧目录保留并报明来源 | `scripts/run-donor-view-consumers.sh:stage_frontend` |
| 首包造目录，Survey 和新 Shell 分包要求目录存在 | **取消相反前置条件**；分包只返回文件/manifest 条目，由总编排合并 | 三个 `scripts/stage-*.mjs` |
| 多个分包复制同一资产并逐字节比较 | **只由一个归属方产出一个路径**；重名冲突报双方来源，最终校验一次 | Survey/New Shell stage 脚本 |
| 几份固定 `entryKeys`、页面/模板/批准文件白名单 | **从实际入口、静态及动态 import、路由和访问权限生成**；逐项校验公开可达文件，私有资源仍需鉴权 | 三个 stage 脚本、`scripts/build-v3-host-adapters.mjs` |
| 每个 stage 后测试全量旧界面、GroupOps Host 演练 | **构建内只验证最终资产可用**；业务旅程随真实影响执行，stage 逻辑改动时跑自身合同 | `scripts/test-stage-*.mjs`、`scripts/test-groupops-history-release.mjs` |
| `web/scripts/build.mjs` 删掉并重建所有入口 | **暂保留一次完整 Web 构建**；它已处理共享 chunk，先去掉重复安装/stage/业务测试。只在量到 Web 构建本身是瓶颈、且能证明动态 import 与旧文件闭包后，再考虑逐入口增量 | esbuild metafile、Host adapter builder |
| `internal/platform/*`、`internal/externaleffects/*`、任意脚本/锁文件直接 full build | **按 base/head 的实际程序与资源依赖构建**；图不可用、真正全局输入变更才 full | `scripts/domestic_release_build.py:classify_paths` |
| 前端步骤每次执行两套 `npm ci` | **每个 attempt 对必要依赖只准备一次**，允许复用 npm 下载缓存；跨 attempt 的安装目录未经锁文件、工具版本及平台核实不直接复用 | `scripts/domestic_release_build.py:_build_frontend`、shell builder |
| `release-fast` 先造 tar/provenance，外层又复制和重新列清单 | **国内流程只造一份完整目录和一次最终清单**；归档交付需要时再生成压缩包 | `scripts/run-donor-view-consumers.sh`、`scripts/domestic_release_build.py:build` |
| `--out` 非空/符号链接/构建中被占用即失败 | **保留成品保护**；重试换新 attempt 私有目录，绝不覆盖旧包 | `scripts/domestic_release_build.py:build` |
| 基包清单、最终摘要、文件类型校验 | **保留并补来源绑定与引用闭包**；自洽的摘要可覆盖错误来源或过期页面文件，须核对基包对应的已安装源码身份和 manifest 引用 | `verify_release_inventory`、安装收据、Web manifest |
| migration 或组件载荷删除要说明 | **按数据/服务后果检查**；历史迁移不能静默删除，组件退场只核对消费者和回退 | `_validate_payload_deletions` |

`release/web/dist` 的存在本身不是拒绝构建的理由；是否由本 attempt 拥有、内容是否可信才决定能否使用。任何重试都不得把旧目录伪装为本次新构件。Web 每次变化完整构建一次，最终仍交付包含所有运行文件的完整包。

## 五、对抗性设计测试

下表逐项给出故障注入、原方案的缺口与修订后的预期结果。前三项做了本机最小可执行复现；其余为代码和构件合同的桌面推演，待实现时仍要用真实候选重放。这里验证的是**方案是否定义了正确结果**，不是宣称新选择器或发布器已经通过测试。

| 反例 | 本次证据与发现 | 修订后必须得到的结果 |
| --- | --- | --- |
| 空 stage 目录已经存在 | **实测**：调用 `stage-pr01-effects-ui.mjs` 返回 1，报 `refusing to overwrite an existing stage`，即使目录由本次编排创建 | 本 attempt 所有的空目录可继续；未知目录仍拒绝 |
| 指定的 Go 测试已改名或不存在 | **实测**：临时 Go 模块运行 `go test -run '^TestRenamedAway$' ./...` 返回 0 和 `[no tests to run]` | 必须比对预期测试名与 JSON 运行事件；零执行不得报通过 |
| 旧 JS 与空入口 manifest 同被写入 checksum | **实测**：`write_release_inventory` 与 `verify_release_inventory` 均通过 | 还要核对 Web manifest 的文件闭包、路由引用和不可达的旧资源；checksum 仅证明字节完整 |
| 自洽但属于另一个源码版本的基包 | 清单本身不证明基包属于本次已安装 app SHA | 基包元数据、已安装收据、源码身份及摘要一致才允许复用 |
| 文档提交使 source main 前进、app SHA 保持旧值，随后再改 Web | 单用候选 base→head 计算构建范围可能漏掉已安装应用之后的运行变化 | 测试按候选 base→head，构建按已安装 app SHA→head；分别标明来源 |
| 候选修改选择器，使自己的测试被排除 | 候选不能为自己签发较宽松的检查清单 | 本次选择由受信任旧策略执行；规则变更做工具合同演练 |
| 新增的相关测试被路径映射遗漏 | 旧能力表的单个历史测试名不能覆盖新增用例 | 完整 diff 中新增/改动测试进入清单，日志确认执行 |
| JS 动态 import 或生成资源没有进入构件 | 静态 import 图及文件 checksum 均可能看不见运行时 fetch | Web 完整构建，校验静态和动态闭包，并在受影响预发旅程实际加载 |
| 全局 CSS 或共享模块影响多个页面 | “只改一个文件”不能证明只影响一个入口 | 从真实引用扩大到消费者；无法确定时扩大 Web 旅程 |
| CSP 允许了过宽的主机或无关路由 | 只检查目标页头像出现会漏掉授权面扩大 | 检查目标页的精确主机和非目标路由拒绝，预发从真实响应读 CSP |
| CSS 压缩只在 jsdom 文本测试通过 | jsdom 不证明移动视口留白已经缩小或没有遮挡 | 预发真浏览器在目标移动视口读计算几何/截图并检查头像显示 |
| CPU 元数据为 1 纳秒但大于零 | 原 PRD 的“只需为正”太弱 | 与实际经过时间作宽范围一致性检查，毫秒误差不失败 |
| 服务期请求永远 loading 或网络报错 | 只“等待预期文字”可能无限等待或吞掉错误 | 等待完成/失败信号，有总截止与错误诊断；错误终态失败 |
| 基线也失败，但变更会触及该调用方 | “基线同失败”单独不足以证明无关 | 仍按本次相关失败处理，不能例外放行 |
| 失败重试遇到旧目录/符号链接/重名文件 | 路径存在与否不足以判断所有权 | 新 attempt 私有目录；未知目录/符号链接拒绝，来源冲突报错 |
| 预发目录已有新文件，但 HTTP 仍由旧进程提供 | 文件和安装收据不证明正在运行的服务使用新包 | 从实际服务读版本、关键页面资产和本次行为 |
| 生产包摘要对，但 CDN/浏览器仍提供旧页面 | 磁盘摘要不证明用户入口得到新页面 | 生产从真实入口读关键资产/响应，区分缓存问题与安装成功 |
| 预发成功后没有人工答复 | 技术通过不等于生产写入授权 | 保持待晋级和队首，不自动生产，也不以超时默认同意 |
| 人工确认了 A 包，等待期间 head 或构件变为 B | 模糊的“同意上线”不能授权另一个对象 | 复核 SHA/tree、构件摘要和预发收据；不一致则重新检查、预发和确认 |
| 人工确认期间预发服务被替换或退化 | 旧预发收据不证明此刻仍运行那份包 | 晋级前重新读回预发包和健康；漂移则重新预发并再次确认 |
| 文档/发布工具单独变更 | 源码 SHA 可前进而应用 SHA 合法保持原值 | 只做对应文档/工具合同；核对两种身份，不安装应用 |
| migration 仅改 SQL 而无 Go 编译 | 不需因此构建所有程序，但数据后果独立存在 | 验证前向兼容、迁移前备份和实际消费者读写 |

设计测试后的关键改动：撤回“任一 Web 变化逐入口增量构建”和“CPU 元数据只需为正”。前者在当前共享 chunk、动态 import 与分层 stage 条件下增加陈旧资产风险；先做一次完整 Web 构建并删掉重复准备与测试。后者改成宽范围的有效性判断。只有测出 Web 构建本身仍是主要耗时，并能证明部分重建的完整闭包时，再单独提出逐入口优化。

## 六、实施顺序与完成判据

### 1. 证据归因

在同一 Linux 前置下重放 CPU 和服务期测试的准确基线与候选，记录请求完成/页面终态、外部时长/元数据、SHA/tree 和日志。读取第二次 attempt 构建日志，隔离重现目录冲突。结果分别标为候选引入、基线同样失败且无关、或未定。没有证据时不先修改发布结论。

### 2. 统一构件目录与构件生产

先改目录所有权冲突，再将三个 stage 的目录创建、覆盖、清单操作收回总编排；四类组件各自产出，最终只汇总一次。增量 Go/整套 Web 与首次完整构建使用同一目录合同，失败重试分配新私有路径。合并重复资产/白名单/演练时保留真实路由权限、静态及动态依赖闭包和摘要校验；复制基包前核对它与已安装源码身份和收据的绑定。`scripts/test_domestic_release_build.py` 与 stage 专项用完整、增量、空目录、未知非空目录、符号链接、重名冲突、旧基包、失败重试重放。无任意旧目录删除，无半包发布。

### 3. 一份选择结果替代五车道

改 `scripts/ci/impact_selection.py`、`scripts/ci/affected_plan.py`、`scripts/ci/governance_impact.py` 和能力表，使准确 base/head 图、页面入口、路由与业务合同输出直接测试和构建清单。`scripts/ci/go_affected_graph.py` 仅在已有图缺失真实关系时调整。选择器规则自身变化仍由受信任旧版本验证，避免候选自我缩小检查。未知关系先调查，无法判定才扩大。

### 4. 执行相关测试并放宽时序边界

让 `scripts/ci/quality_lanes.py`、`scripts/dev_preflight.py` 和 `scripts/domestic_main_release.py` 执行清单，不再默认完整 lane。更新 `scripts/ci/test_*.py`、`scripts/test_domestic_release_controller.py`、`scripts/test_domestic_release_lifecycle.py` 的对应合同。检查 Go JSON/Node 测试结果，要求清单中的测试各实际运行一次，无 skip 或“no tests to run”。基线归因后修改 CPU 与服务期两个测试：保留真实安全/业务结果，改为宽范围元数据合理性及有截止时间的终态等待。故意极短 profile、敏感标签、错误终态和长期加载仍必须失败。

### 5. 加入预发后的人工晋级关口

预发测试和读回全通过后，把准确身份、完整构件摘要、执行的检查及跳过/未验证项、预发页面/服务读回整理为一条可审阅结果，明确标记“待人工确认”。准备动作到此结束；晋级动作须由明确的人工作出，并携带该候选 SHA、构件摘要和预发收据摘要。未确认不执行生产动作。收到确认后检查候选、包和预发收据仍一致，并重新读取预发运行服务的包与健康，再同包晋级。确认期间队首顺序保留，执行锁可释放；生产结果不明先只读对账，不重复安装或再次索取宽泛确认。用已有候选、尝试和收据实现，不增加第二套审批状态机。

### 6. 删除旧试验、重复打包和文档规则

移除日常路径中的 high/protected、固定车道、历史测试哨兵、影子样本、GitHub ACK、重复准备与构件重复归档；只留下预发后的人工晋级确认。更新 `AGENTS.md`、`docs/development-before-start.md`、`docs/operations/domestic-main-release.md` 和三个 V4 项目 skill 的冲突口径。先使新执行器和构件合同通过代表案例，再改生效说明；不维护两套长期并行的发布流程。

### 7. 代表性重放后启用

至少覆盖文档、CSS、裂变 CSP+页面、普通 Go、身份/支付、迁移、发布工具、共享依赖、未知依赖；验证新增相关测试不会漏选、故意业务失败会阻断、无关基线失败能独立归因。构件重放完整和增量、目录冲突及同候选重试，预发核对实际提供的头像资源、CSP 和移动视口留白。对“未确认”“确认了旧包”“确认后候选变化”分别演练不得写生产；准确确认后再从运行服务读回同包摘要/版本/业务输出。纯文档/工具提交核对源码与应用 SHA 可以合法不同。没有固定 PR 数量、速度百分比或第二个人工审核门槛。

## 七、本次候选与实施边界

以裂变候选作回放样本，目标检查为 `web/v3/referralCenter.test.mjs`、裂变相关 Host/Chromium 旅程、CSP 在目标页允许指定头像主机且其他路由不放宽、`cmd/aicrm` 编译、前端相关入口构建与预发真实页面读回。它不因 CPU、服务期、GroupOps、Archive SDK 或 229 个 Go 包自动失败；现有 CPU/服务期失败未完成基线归因前，也不把候选称为已通过。

本改造不涉及 OneID/外部身份、业务持久化或 Provider 写入；涉及既有发布状态持久记录和生产部署外部效果，复用现有队列、锁、attempt、收据，不新增发布队列、风险分数、第二套审批系统或影子期。实际开发任务提交准确候选和证据，由唯一发布工作台串行预发、等待人工确认后再同包生产与读回。
