# AI-CRM-v4 全系统换维度审查交接

本轮从当前代码的 Composition、HTTP、worker、CLI 和领域 Port 建立 36 个实际模块的清单。确认并完成 17 项独立最小修复，均有准确复现和针对性本地合成回归；其他结论限于表内入口与验证范围。不承诺零 bug、全组合穷举或所有模块完整验收。

## 基线、环境和授权

- 准确 base：`da810b3a77f96d58340cfcbd074243997b85a5e3`；tree：`4e00c0f773064705ad3aa188197bed61a7ec6a87`。
- 2026-10-07 07:31:06 UTC / 15:31:06 北京时间再次只读核实：国内 `refs/heads/main` 仍为该 base，canonical 工作区干净；未把旧 `122600c` 当最新生产。
- canonical：`/Users/qianlan/Downloads/新CRM/main`，`AI-CRM-v4` 是其别名；本轮独立 clone：`/Users/qianlan/Documents/Codex/2026-10-07/task/AI-CRM-v4-audit`。
- 本地 ref：`codex/full-system-dimension-review-20261007`。最终准确 HEAD/tree、全部独立修复提交与证据摘要由 `audit-evidence/FINAL_CANDIDATE.json` 和 `FIX_INDEX.json` 绑定；文档提交与行为提交分开。
- 只读补充了 AGENTS、开发入口/frontdoor/core skill、memory_summary 和历史报告，没有修改记忆、旧工作区或他人变更。
- 全部动态测试使用合成 fake 或新建 `/tmp` PostgreSQL16 Unix socket-only 实例、随机独立 schema。测试已结束，PG 和浏览器 fixture 服务已关闭。无真实客户、真实登录、生产/共享库、付费 AI、支付退款或 Provider 调用。
- Go 1.26.6 darwin/arm64。Node v24.21.0、npm 11.19.0；仓库 pin 为 Node24.18.0/npm11.12.1。本地资源构建、Host 构建、TypeScript 检查和 Go 应用编译通过，不能称 pin 版本、Linux 或生产可复现构建已通过。依赖复用只读 node_modules，源码/冻结 view 来自当前 V4。
- 未 push、merge 或 deploy。发布交接只使用原工作台；本轮不另建发布机制。

历史安装报告表明：35 条旧修复安装55936a1；PAY1/PAY2后续最终安装本轮base da810b3。旧报告的未修状态是历史。其安装报告是历史证据，真实资金及Provider效果仍由用户实测；不把旧测试当本轮新执行。

## 风险排序与换维度

P1 优先身份/会话归属、持久状态转换、重复回执、取消与迟到结果、成功证明保持、历史原文和来源；P2 商品非支付、优惠券、裂变/分销非金融、名单标签、媒体渠道群运营；P3 配置、开放接口/CLI、分页、Unicode、日界线和浏览器导航。按具体链路选择锁步并发、事务/回滚、具名 PG 合同、纯应用/性质边界与实际浏览器，不要求每模块跑所有类型。

## 17 项已修通过

完整 SHA/tree/parent、变更文件、red/green 日志及 SHA-256 见 `audit-evidence/FIX_INDEX.json`。五项影响、断言和边界见对应 lane 报告。

| 提交 | 业务后果与最小修复 | 验证证据 |
|---|---|---|
| d2616a3 | CLI --wait 超时未约束首个状态请求及轮询；统一绑定等待 context，期限内提前完成保持成功 | root-cli-wait-red/green.jsonl；CLI直接合同 |
| ce64042 | 身份拆分后旧问卷 receipt 仍返回他人 root 的成功 ID/token；三个回放点重查当前 lineage，旧结果 forbidden 保留 | identity-survey-split-replay-red/green.log；真实合成 merge→submit→reverse PG |
| fc1dd54 | 45/200 字符数据库合同被字节长度拦截，中文创建/复制/导入失败；使用 rune 计数 | commerce-coupon-unicode-red.log；app/PG green |
| 97993f2 | 合法历史大写/数字开头 slug 无法打开或领取；HTTP沿用既有URL-safe grammar | commerce-coupon-slug-red.log；public routes green |
| 1c3b014 | 归档原文的换行、缩进、全角空格被裁剪；保存精确正文 | channel-media-archive-text-red/green.log；4 PG历史合同 |
| 873daa0 | 生成完成 item→run 与取消 run→items 反向锁序导致死锁/取消回滚；统一 run→item | async-concurrency-red/green.jsonl，锁步并发 |
| 851fa85 | 周期相同完成回执并发重放误冲突；先锁action，再读receipt | async-concurrency-red/green.jsonl，唯一receipt及payload drift |
| 391cbaa | 雷达今日数字随PG会话时区变化；上海自然日聚合 | channel-media-radar-today-red/green.log，UTC/LA/Shanghai对照 |
| 5a4bc18 | 群详情缺失/null成员列表冒充零人数成功事实；拒绝nil，显式空列表保留 | channel-media-group-members-red/green.log，目录直接消费者 |
| 2788300 | 两管理员同browser key启停不同分销员只落一条outbox；事件键与actor-scoped receipt一致 | commerce-distributor-toggle-red.log；distribution green，2/2/2/2事实 |
| d16eba7 | HXC终态refresh迟到发布覆盖current；检查MarkPublishing影响行，并先锁publishing run | identity-survey-hxc-late-publish-red.log；4 PG green |
| dd16af4 | 后续客户归并/员工停用阻断已接受AI计划重放；先读原actor/payload receipt，仅新请求验可变资格 | async-ai-replay-red/green.jsonl，四入口及新键/drift对照 |
| 6980c9a | 并发旧Excel失败观察抹掉送达证明；在读binding前复用plan锁 | async-excel-proof-red/green.jsonl，现有16组完成聚合合同 |
| af61523 | Machine status混读旧plan和新summary；在status UoW内持plan锁 | async-ai-status-red/green.jsonl；不保证外层recipients同快照 |
| 270df9e | 原始建客来源随合并漂移；从既有首次self-owned merge账本还原原owner | identity-survey-overview-red/green.log，反转及链式合并 |
| 86b14e3 | 固定券使用期已截止仍显示可领取；映射既有ended，保留未来窗口提前领 | commerce-coupon-fixed-availability-red/green.log，5纯app合同；4公共入口补跑 |
| b8c0925 | 问卷必答选项被Tab跳过，键盘无法填写；补聚焦语义、Enter/Space、重绘焦点，复用click/Controller | frontend键盘red/green；实际截图；最终3问卷合同 |

无新增数据库migration、身份推断、队列、治理或执行框架。撤销对应独立提交可回滚源码；归档修复不能恢复历史已裁掉的原文。各模块继续复用既有 Port/UoW/lease/fence/receipt。

## 36 个实际模块与验证边界

入口均由当前代码/Composition识别，完整入口路径及具名测试位于 lane 报告；静态注册条目、菜单、HTTP200和accepted不算业务全覆盖。

| 模块 | 实际入口/链重点 | 本轮结论与边界 | 报告 |
|---|---|---|---|
| access | login/logout、session_version、角色治理、凭据轮换 | 单元、HTTP及PG会话/角色/回放/回滚；未遍历全角色×API | root-contracts |
| address | port/address静态省市区数据 | 合成目录probe与读取兼容；无独立测试文件 | identity-survey |
| adminops | execution_runtime、安全投影、控制写 | 小包合成合同；不调用运维Provider | root-contracts |
| aiassistant | admin/machine plan、review、Excel观察 | 3修复；具名单元/PG；外层状态一致性仍有线索 | async |
| automation | runs、generation、cancel、audience runtime | 1锁序修复；取消迟到/quiet-hours等局部合同 | async |
| channel | catalog、acquisition配置、历史、entrant receipt | 配置回放/CAS/引用/历史4 PG；Provider未实测 | channel-media |
| clicatalog | port/catalog注册→CLI BuildRequest | 静态注册及CLI消费者合同；无独立模块测试 | root-contracts |
| config | 整单保存/setup CAS/runtime release、安全设置 | app/store/HTTP合同；未发布真实运行配置 | root-contracts |
| configmigration | source快照、target群运营历史映射 | 3具名纯历史映射合同；金融恢复排除 | root-contracts |
| coupon | rule生命周期/slug、public、领取owner | 3修复；Unicode/slug PG及public合同；固定截止只纯app | commerce |
| crmcli | invoke --wait/--all、原点、schema | 1超时修复；10顶层具名合同 | root-contracts |
| customer | directory、360/history、sidebar annotation、tag history | 过滤分页/晚到读取/保护投影/PG回滚；未验全历史源 | identity-survey/frontend |
| distribution | 非金融register/admin enable、trusted session | 1outbox修复；9非金融合同；冻结session仅静态线索 | commerce/identity-survey |
| externaleffects | Worker lease/fence/completion、admin状态 | 仅outbound fixture的过期/迟到/停止证据；金融owner排除 | async |
| groupops | plans/webhook、catalog cursor、目录/邀请消费者 | domain/app/HTTP及2PG目录合同；无真实群发 | channel-media |
| hxcdashboard | refresh/publish、workspace/share、注册事实 | 1终态发布修复；4PG及投影合同；重叠attempt未证实 | identity-survey |
| identity | trusted subject、merge/reverse、query、来源统计 | 1来源修复；合成merge/reverse/重放/原子合同 | identity-survey |
| media | image/attachment/package/preparation/invitation | 7 store+4 HTTP PG引用/保存/删除竞争；无真实上传 | channel-media |
| messagearchive | Inbox→解密→normalize→CommitBatch→history | 1原文修复；4PG cursor/归属/保留合同；官方SDK未跑 | channel-media |
| openplatform | OAuth/MCP/REST、machine capability、permission | 小包协议/来源/权限/fake operation合同；无外部调用 | root-contracts/async |
| operationcycle | strategy/run/action、claim/renew/event、runner | 1并发回放修复；租约/fake runner/socket合同 | async |
| order | order owner与客户历史只读Port | 只识别当前入口和排除；相关支付退款动态目标未跑 | 本报告排除 |
| outbound | frozen intents、Provider router、receipt投影 | fake非金融错误/unknown/顺序/digest合同；无真实送达 | async |
| overview | independent Owner读模型、unknown口径、游标 | 静态合同；金融相关动态旅程排除。identity来源读单独已测 | root-contracts |
| payment | 支付/退款Provider与HTTP/owner | 受限排除，仅读取旧报告识别范围和已装修复 | 本报告排除 |
| platform | UoW、idempotency、readshare、time、runtime/jobqueue | 局部纯合同及fake localhost/River insertion；信号/长保留未跑 | root-contracts/async |
| product | 普通/周期生命周期、public/share/member-grid | 41顶层单元/HTTP/PG合同；库存不恢复，无支付调用 | commerce/frontend |
| radar | public event、session、stats/visitor/history | 1时区修复；4PG及分页fake；OAuth未实测 | channel-media/frontend |
| referral | campaign、participation/team、invitation/rank | 24具名合同含9PG/cursor；individual隐藏team契约为线索 | commerce |
| release | candidate/receipt/state、observation | 小包纯生命周期/并发合同；未真实发布 | root-contracts |
| segment | configuration/version、snapshot publication、scheduled cursor | 非金融PG/rollback/10万合成members；sender replay线索 | async |
| sidebar | bootstrap/context、每能力authorize、owner cursor | 13 backend handler合同；身份/section范围；完整浏览器切换未验 | channel-media |
| survey | definition/public OAuth、submit/result、history | 1lineage及1键盘修复；合成PG/Controller/浏览器；退休受众不恢复 | identity-survey/frontend |
| tag | catalog/receipt/outbox、sync、history recovery | Unicode/排列/回滚/引用PG；无Provider标签写 | async |
| webshell | canonical模板、Host/Adapter、admin客户页 | 分页/迟到/fake保存/导航/时间合同及整体构建；非全站UX验收 | frontend/root-contracts |
| wecom | OAuth/context、directory/contact/archive、群详情adapter | 1缺字段修复；具名单元/3PG；无官方OAuth/目录调用 | channel-media |

lane报告路径均在本工作区 `audit-evidence/`：`root-contracts.md`、`identity-survey.md`、`commerce.md`、`async.md`、`channel-media.md`、`frontend.md`。原始日志、截图及各lane输入SHA清单保留；`BASELINE.json`保存初始代码目录计数。早期进度说35模块是计数口误，实际36。

## 证据口径与最终检查

red、skip、环境构建/连接失败、fixture预期错误均单独保留。后续精确补跑不删除原收据，不把包exit0中的SKIP算PASS。测试计数在lane内使用顶层Test和具体package；跨lane/重复运行不相加成覆盖率。`surveyAdapter.test.mjs`业务断言输出PASS且exit0，但JSDOM teardown异常，明确为非clean pass，未当线上缺陷修复。

已在最后行为代码上补跑4个coupon public合同、3个survey Host/keyboard/resume合同。最终资源构建、Host构建、TypeScript noEmit及Go应用编译通过；最终树的候选具名清单由既有 `docs/release-checks.json`承载，并使用既有preflight只做计划解析。清单不含原金融测试，installed_checks为空，因为本轮未执行安装或生产读回。不能用此清单扩大已排除范围。

重点源码hash与lane执行后摘要再次核对，输入未变的局部证据可复用；摘要是重点输入清单，不是全部传递依赖的冻结证明。最终`EVIDENCE_INDEX.json`绑定每份交接证据；准确最终head/tree/ref以外部FINAL文件为准，避免报告自引用commit。

## 剩余线索、阻断与用户实测

1. Open API `v1OperationStatus`仍把status、recipients、scope分次UoW读取；AS5仅修复status内部plan/summary。外层完整响应混合时刻尚未动态复现。
2. HXC同一run非终态重叠attempt缺尝试级隔离、`publishing→running`可能相互干扰；终态发布已修，非终态线索未复现。
3. 分销浏览器会话保留发行时customer，后续精确subject归属未动态证实；与原payment可信桥相邻，仅静态记录，不构造金融/身份代理目标。
4. Segment ReplaceSenders可变资格验证早于receipt；Coupon关联已归档商品导致公共入口404；Referral individual模式隐藏team；问卷完成页旧模板title及身份拆分409原因文案，均为未证实契约/体验线索，不冒充缺陷或已修。
5. 实际浏览器读取 `/frontend-evidence`被 `net::ERR_BLOCKED_BY_CLIENT`阻止，立即停止该动作，未换路径、工具或代理重试。浏览器双击仅证明进入完成页，精确POST次数未验证；独立JSDOM单次请求证据单列，不能替代被阻断的动作。
6. 官方Provider/模型、真实发信/送达/上传/OAuth、手机/读屏、真实身份切换全旅程、官方归档SDK、全量历史源迁移、OS信号/长期清理及Linux/pin构建未验。用户实测仍是最终业务效果证据。

受限排除始终有效：PAY1/PAY2原支付/退款对抗、相同目标改写/替代测试或代理、相关HTTP/PG对抗构建、金融恢复、D6退款锁序全部未执行。库存及问卷指定员工受众不恢复。没有新的平台cybersecurity拒绝；新增浏览器读取阻断单独记录，不能据此启动替代路径。

## 原发布工作台交接

原路由来自 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json`，工作台thread `01a0f85a-9de6-7701-a12a-9c774080389b`。提供本地Git bundle、证据归档、准确base/head/tree/ref和具名check manifest，后续沿原工作台处理。该交接材料不授权自动push、merge或deploy；本轮没有操作生产。行为提交可分别审阅/撤销，无额外部署机制或数据迁移。
