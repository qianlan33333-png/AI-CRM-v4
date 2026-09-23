# 邀请裂变、战队与排行榜

## 已确认业务合同

一个客户只有一个当前邀请归属，最后一次显式接受有效邀请的事务生效；只打开链接不写状态。每个活动的参与、邀请成绩、队伍独立且不可因另一个活动改写。同活动重复参加返回原结果，不换队、不转分、不重复换绑。老客户参加新活动可计分，自邀与失效凭证拒绝。首次直接参加可选队，不覆盖全局归属。当前归属不影响 CRM 员工负责人、不产生佣金。

管理员建队并指定已有可信客户为队长；队长确认参加后可邀请。邀请人首次成功邀请一人得一分，仅直接层级；战队分为队员直接成绩之和。总榜/北京时间周一零点周榜/每日零点日榜，分别支持个人/队内/战队。历史日期可查。同分按先达到分数者优先，再按稳定内部键排序。撤销追加冲正及审计，不删除事实；已登记奖励遇冲正进入核查，不伪造追回。

前端 /referral 为独立公共活动入口（无需注册分销员、无需购买、无需收款准备）。保留分销原有入口，增加裂变活动入口。页面含活动列表/详情、接受邀请、战队、排行榜、本人名次、复制链接/二维码/固定模板海报、邀请明细、规则。管理员 /admin/referral 使用现有 CRM 壳，含活动配置/队伍/邀请事实/归属历史/撤销/人工发奖。

## 分类与架构

OneID: reads canonical customer，通过现有可信微信/Payment 会话桥，禁止 HTTP 自报 actor 或新匹配器。
Persistence: local transaction + internal durable jobs，PostgreSQL16，复用 platform UoW 与 River Jobqueue。
External Effects: 本期无新增 Provider 写、自动分佣或发奖。无 Redis、定时器或第二套金融账本。

Referral 唯一拥有活动、参与、战队、归属/历史、凭证、计分、奖励、审计及版本化事件记录。接受邀请的业务状态、幂等收据、审计和 outbox 在一事务中；按客户锁序列化跨活动换绑，保存历史版本供未来订单快照。现有 Distribution/Payment 不读取 Referral 表，不改变已有归因。

## 调研与复用决定

- 小裂变好友获客 https://doc.xiaoliebian.com/archives/847052442508 ：参考活动/海报/助力/奖励运营流程，首期用可信登录+确认参加替代加企微助力。
- Viral Loops https://developers.viral-loops.com/docs/the-milestone-referral ：参考个人邀请入口及人数里程碑。
- Nakama https://github.com/heroiclabs/nakama ：Apache2 Go 服务，参考周期排行/竞赛模型，不安装整套游戏服务器。
- go-leaderboard https://github.com/dayvson/go-leaderboard ：Redis 方案与本仓约束不符，不直接引入。

Go 自有业务实现复用现有 OneID/Session、Postgres/UoW、Jobqueue、共享UI与二维码库。

## 实施与验收

隔离基线 origin/main 0ef8aed，原主工作区未提交改动不动。后端、HTTP/Composition、前端分工，根任务管理 shell/集成/验收。

必须测试：跨活动换绑不影响历史成绩；同活动幂等及并发；非确认 GET 无效果；自邀/伪造/过期；无来源不改归属；队长/成员与战队固定；三类榜单一致、时区边界/结束边界/冲正排序；奖励重复防护与核查；个人数据及管理员权限/CSRF；授权回跳；现有支付/退款/分销回归。

发布门禁：fast→compile→真实独立Postgres专项→Host浏览器→完整CI→合并→SSH完整包发布→生产只读回读及真实微信参与验证。各门禁分别记录证据，页面 HTTP200 不作为业务验收。

## 运维启用与验收口径

`AICRM_REFERRAL_TOKEN_DATA_KEY` 使用独立随机 32 字节密钥（无填充标准 Base64），仅放在服务器受保护的环境文件。缺少配置时活动接口返回不可用，现有分销/支付照常运行；活动不复用金融接收方准备状态。API 与 effects-worker 使用同一份密钥及同一 main 完整发布包。升级迁移新增 Referral 自有表，保留旧 release 回滚入口，不回滚或删除活动事实。

本地真实 Host 旅程覆盖：真实管理接口建活动和队伍、两场活动显式接受、跨活动换绑和重复参加不回改、375/430 手机显示、日周总榜、复制自己的邀请入口、后台客户目录选择与归属历史、撤销邀请与榜单冲正、人工奖励待核查；同时断言没有新增支付分账指令。授权 Provider 和真实微信手机验收与本地 fixture 会话测试分开报告。

数据库专项包含：同活动/跨活动并发参加、邀请人与管理员撤销竞争、奖励与撤销竞争、审计/Outbox 失败原子回滚、上海日期及周边界、队长归队与唯一性、无来源参与、伪造/自邀/过期/停用、奖励去重及关联核查范围、活动结束持久任务和排名快照。前端、编译、专项、CI、发布及生产业务验收分别留证，不互相替代。

## 方案一：活动详情数据看板与可审计明细（2026-09-18 已批准）

### 参考与边界

开发前已检索 GitHub 上的 CRM 导出及裂变榜单实现。GopherCRM 的受权限保护、服务端筛选导出接口可作为“导出不依赖浏览器已加载页”的交互参考；Talon.One 的活动维度邀请码可作为“凭证绑定活动和邀请人”的边界参考。两者均不引入本仓：本方案仍以 Referral 的可信微信身份、活动参与事实和 PostgreSQL 单事务审计为唯一依据。

一级「裂变活动」只承担活动列表、筛选和新建。进入单个活动后，默认页先显示参与人数、有效直接邀请人数、战队数及按加入日期统计的当前有效邀请；下方显示真实参与明细。配置、战队、邀请明细、归属历史和人工奖励都保持该活动上下文，不在一级堆叠表单。

参与人数是该活动所有已确认参加者，包含已确认参加的队长、无来源参加者和直接邀请数为零的人。有效直接邀请人数只统计未被撤销的直接邀请，不汇总下级链路。指定但尚未确认参加的队长单列为「待加入」，不计入参与人数。活动进行中仍可新增战队；既有 participation、团队归属和 score 事实绝不迁移。已结束和停用活动不能新增战队。

管理员详情契约：

- `GET /api/admin/referral/campaigns/{campaign_id}` 返回核心指标、每日指标和战队摘要。战队摘要含队长公开展示、已参加人数、待加入状态和有效直接邀请人数。
- `GET /api/admin/referral/campaigns/{campaign_id}/participants?team_id=&state=&cursor=&limit=` 返回所有参与者，不以邀请事实代替参与者；成员行含所属队、邀请人、加入时间、直接邀请数和状态。
- `GET /api/admin/referral/campaigns/{campaign_id}/invitations?team_id=&inviter_customer_id=&state=&cursor=&limit=` 返回邀请下钻，状态区分有效与撤销。
- `GET /api/admin/referral/campaigns/{campaign_id}/export?view=participants|invitations|teams` 复用相同筛选，从单个数据库读快照导出全部匹配行。管理员权限与同源会话不变；CSV 只含公开展示字段，拒绝公式注入，不含客户主键、OpenID、手机号或 Cookie。

队长选择继续由客户目录返回的 canonical Customer ID 提交，服务端在同一事务内核验 canonical root、客户 active 状态和 provider-verified 身份。建队不增加 campaign version 参数，因为命令不改写 campaign；它只锁读活动生命周期。错误分别为活动不可配置、队长资格不足、队名已存在、队长已被占用和幂等请求内容不一致，页面保留填写内容并给出对应处理方式。精确重试复用原幂等键；用户修改队长、队标或队名后创建新的命令键。

队长本人页面只通过自己的可信会话返回 `captain_team` / `is_captain`。被指定但未参加时显示身份和「同意规则并加入战队」；参加成功后才开启自己的邀请链接、二维码、海报和分享文案。管理员不代替队长接受规则，公开接口不暴露他人的身份事实。

本轮仍然：OneID 只读 canonical/verified Port；持久化使用 Referral PostgreSQL 读模型及既有 UoW、receipt、audit、outbox；内部结束任务继续 River；不增加 Provider 写、资金效果、订单归因或佣金。
