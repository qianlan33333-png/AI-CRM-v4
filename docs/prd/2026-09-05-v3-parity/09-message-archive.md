# PRD-09 企业微信会话存档：旧版能力先上线

状态：批准开发；先读 00-control.md；Terra xhigh。

## 1. 来源与优先级

依据用户提供的《企业微信会话存档与客户聊天记录_PRD_v1.1_官方机制校正版.md》；附件位于 `/Users/qianlan/Downloads/企业微信会话存档与客户聊天记录_PRD_v1.1_官方机制校正版.md`。用户随后确认先迁移旧能力，本 PRD 覆盖其全部消息专用 Renderer、新诊断中心、切流/停旧和生产执行要求。

V3 当前无完整 messagearchive 领域。供体为总控 pin 的 `aicrm_next/extensions/archive/message_archive/`：sdk_subprocess、archive_sdk、sync_service、repo、application、api；采用已验证拉取内核，新增官方通知接入，不照搬旧定时触发器。

OneID：只解析带 corp scope 的可信 external_userid，读取 canonical/现有 lineage；不 Provision/Link/Merge。持久化：Provider read + 现有 Webhook Inbox + PostgreSQL 批次事务；无 Provider write，无 External Effects。

## 2. 交付范围

- msgaudit_notify 独立严格 parser，复用现有加解密与 Inbox。验签并持久化后快速 ACK，HTTP 内不调 SDK。
- 正常拉取只能由通知驱动；人工 oneshot 仅用于初始化/故障恢复测试。不新增 Archive cron、ticker、周期 River pull 或自建 lease/fence。
- Go 本地 SDK Runner 隔离官方 C SDK，保留 stdin/framed stdout、超时与释放句柄行为；不把 C SDK 链接进 Web 主进程，不保留 Python 运行时依赖。
- 从已提交 last_seq 使用官方 GetChatData 分页至空 chatdata，沿用批量解密、msgid 去重与游标同事务。
- 先实现旧版文本/图片显示、私聊/群聊读取、时间/员工/类型/关键词过滤和搜索；其他类型保留 common envelope 与完整受保护 payload，明确 unsupported，不丢失。
- 复用官方 GetMediaData 分片和私有媒体访问能力，图片可读；媒体引用及可获取内容保留，不提供公开 URL，不进入营销素材库。
- 最小 health、同步运行证据、人工一次补拉和未解析重试接口，不新建诊断中心或全类型 UI。

## 3. 数据与可靠性

messagearchive 拥有消息、参与者、媒体引用、游标、运行/解析问题和导入收据；wecom 拥有 Provider 读及 SDK 适配；identity/staff 通过 Port 解析。

每企业作用域 `(corp_scope,msgid)` 唯一；每批消息、参与者、问题、业务收据与游标同事务。解密/Provider 临时失败不得跳过该页前进；可隔离的未知类型保留原事实后推进。SDK 网络与媒体下载不持有数据库事务。

多个通知使用现有 Inbox 调度安全处理；并发不能跨越未提交批次推进游标。每个 delivery 有工作上限，未排空由原持久任务继续，不新建周期拉取。容量耗尽与重试耗尽保留可人工恢复证据。

员工和机器人不能作为 Customer；未知参与者保留 unresolved/conflict。canonical 读取经 Identity Port，不访问其表、不改写原始客户归属事实。

## 4. 接口与页面

- 新增窄 `wecom/port.MessageArchiveReader`：health/fetch/decrypt/media。
- messagearchive 只读 Port 提供 CustomerID 消息页及搜索；客户入口使用 canonical CustomerID，媒体入口先验证对应消息访问权限。
- 兼容旧版消息读取/搜索交互，通过 Host 将旧身份入口转为 V3 安全 CustomerID 契约；不开放手机号/UnionID/裸 external_userid 直接查询。
- 优先复用旧版已有聊天显示区域及页面素材；不将聊天重新塞入已明确排除它的精简侧边栏，不改 Customer 360 产品边界。
- 读取/搜索/媒体/人工恢复复用现有认证、CSRF、RBAC、审计、no-store；普通 API/日志不输出私钥、正文日志、SDK 引用或裸 Provider payload。

## 5. 历史数据

实现 inspect/dry-run/apply/reconcile 的隔离测试工具，导入消息、可靠来源身份、媒体引用与原始事实；逐行 source digest/结果收据。保留未解析消息，不按手机号或默认员工兜底。

同步游标只可在对应快照完整且对账通过后初始化；已有 V3 新消息时不能覆盖或倒退游标。源继续独立运行，不停止旧 writer、不修改旧 cursor、不切换回调。本轮不把生产快照 apply 到 V3 生产。

## 6. 验收

- 官方协议形态夹具验证加密通知、错误签名、错误 corp、重复通知；外部联系人原 parser 验证不放宽。
- SDK Runner 缺失/超时/非法 frame/密钥版本、句柄回收与敏感内容不泄漏；Linux 构建与真实动态库可加载验证另列证据，不能以纯 Mock 宣称 SDK 已验证。
- PG 多页空页、第二页失败回滚、msgid 去重、重启继续、并发通知、未知消息不丢、unresolved 不建客、媒体分片校验。
- 旧版读取/搜索/图片和权限 journey；源快照每行有对账结果；无通知且无人工操作时自动 GetChatData 为零。
- 本轮禁止调用生产 SDK 拉取正文；缺少可用测试 SDK 时继续完成可验证工作，并把未完成的 ABI/动态库验证精确列为阻断，不能跳过后称完成。
- 输出未合并 PR、逐项测试结果和后续真实通知/SDK/授权范围配置待办。

## 7. 供体核对补充

总控已读取 sdk_subprocess.py 与 archive_sdk.py：现有独立子进程只实现 fetch/decrypt，framed stdout 必须按 bytes 读取；不得将包含 SDK 噪声的整个 stdout 当 UTF-8 文本日志。Go Runner 的 health/media 是本合同所需的最小补齐。

旧 archive_sdk.py 只接受 publickey_ver=1 且尝试多种 RSA padding。这里按用户附件的官方机制校正：依据 publickey_ver 选择密钥，仅使用官方 PKCS1 解密规则，不复制“多算法试到成功”的旧 fallback。测试使用生成测试密钥与协议夹具，不读取生产私钥来跑测试。

## 8. SDK 可用性证据

2026-09-05 已只读核对旧环境的 `/home/ubuntu/wecom-sdk/C_sdk`，取得动态库、官方头文件及版本文件的本地验证副本 `/private/tmp/aicrm-v3-archive-sdk-20260905`；未读取存档Secret、私钥或聊天正文，未改变旧环境文件和服务。版本为20250205，Linux x86-64动态库 SHA-256 为 `79ced4de6b18d5e96a21cd06f325794dc8957f8925120538d56d4ce827d3dfd0`，传输完成后与服务器一致。

这证明真实SDK可取得，尚不证明Go Runner的Linux ABI已经通过。开发者应基于该头文件实现窄隔离层，并在隔离Linux验证构建/加载/句柄生命周期，使用生成或官方测试向量；不能拉取生产消息来当自动测试，也不擅自把生产配置或私钥打包到PR。

进一步通过[官方文档91774](https://developer.work.weixin.qq.com/document/path/91774)原始HTML核对并完整下载[官方x86 SDK包](https://wwcdn.weixin.qq.com/node/wwcomm/sdk_x86_v3_20250205.tgz)：包SHA-256为 `afa8c017da2994ad2215933f2fcc6042d40d935663ad42d6e1e9d7716652f0d8`，其中C_sdk动态库与上述旧环境库的SHA-256完全一致。本地包在 `/private/tmp/aicrm-wecom-sdk-x86-v3-20250205.tgz`。Linux PR CI可以从官方源取得固定包并验证包/库双重摘要，完成真实加载与句柄验证；不把二进制提交到代码仓。

## 9. 旧展示复用入口澄清

供体确有 `aicrm_next/app/admin_console/static/admin_console/customer_profile_sections.js`（ui-preview中也保留副本）的 renderMessages/loadMessages 客户详情嵌入片段，并非独立聊天工作台。沿用该片段的文本展示、读取和转义行为，图片改用受权私有媒体引用，不泄露sdkfileid。

现有 `docs/08-PRD-客户档案与跨域详情接线.md` 与 webshell 验证明确排除精简Customer360/企微侧边栏聊天。09将旧片段封装在归档领域自己的最小读取Host入口，如 `/admin/message-archive/customers/{customer_id}`，仅复用旧展示/搜索/图片能力；不修改360/sidebar页签、schema或其排除测试。安全API使用归档领域路由和canonical CustomerID。模块入口的共享导航由最终集成协调，不重新设计聊天产品。

## 10. PR139 总控审核修正

- 同一签名通知在不同本地接收时刻重放，必须返回成功并保留首次收据；接收时钟不得使同一业务通知的payload摘要漂移。保留通用Inbox的真实载荷冲突检查。
- 分批显式重解析必须有继续位置，前一批永久未匹配的参与者不能阻止后续已具备可信身份的记录被处理。
- 历史重复判定需与对账使用同一完整不可变事实边界；相同msgid而seq或其他事实变化应明确隔离，不能先报duplicate再无法对账。
- Archive Store不得直接JOIN Access的admin_users；通过稳定只读Port补充员工显示名，按页合并读取避免重复查询。
- 真实PG注入批次后条写失败，前条消息、参与者和游标一起回滚；解除故障后原页可恢复。真实并发通知验证游标CAS与去重。纯内存Store及直接回调UoW不计此证据。
- Linux SDK检查须证明实际句柄和输出内存释放，不得删除失败断言或跳过Linux测试。
- 3362619改为Access Port后，CustomerStaffIDs仍同时筛同一参与者行的customer_id与staff_user_id；正常客户/员工为两行，结果必为空。需按message_id连接Archive-owned两类参与者，再由Access补显示名，并补真实PG员工下拉/筛选旅程。页面聚合staff ID先去重再检查批量限额，不能因同一员工在多条群消息重复出现而拒绝整页。

## 11. 最终发布清单缺口

2026-09-06总控在已绿组合6ca8dc0核对发现：已有 `cmd/migrate-message-archive` 及测试，但现有CI发布制品未构建该命令，安装清单也未要求该文件。独立导入工具必须随本轮候选交付，不能到生产再临时从源码编译。

此项不改变OneID、数据Owner、导入协议或运行机制，只补既有命令的打包与安装验证。执行者从总控指定的准确组合HEAD创建独立分支，在现有Build release artifact步骤加入Linux amd64导入命令，安装器要求该可执行文件，现有合成安装测试加入该文件及缺失时明确失败的反例。同时核对已在workflow构建的 `migrate-commerce-history` 在安装器的必需工具清单中，避免同类遗漏。

不改CI触发条件、发布授权、生产配置及默认开关，不调用部署工作流，不执行导入。交付仅为未合并PR、准确HEAD、命令构建和安装契约证据；真实历史快照执行仍在下一阶段。
