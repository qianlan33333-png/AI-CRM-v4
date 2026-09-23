# PRD-08 渠道欢迎语 20 秒路径

状态：批准开发；先读 00-control.md；Terra xhigh；沿用“渠道码”。本次只做这一缺陷，不重建渠道中心。

## 1. 基线与分类

复用固定V3基线中的 `docs/PRD-渠道码中心-OneID-重构与历史导入.md` 的现有渠道行为、internal/wecom 回调验签与 WelcomeGrant、internal/channel/entrant_actions.go、internal/outbound/channel_entrant.go 和现有 External Effects/River。

当前 AcceptEntrantActions 强制 CustomerID，需完成渠道分配后才接纳欢迎语；回调消费可能等 30 秒，效果进入普通 outbound 队列；grant 的十分钟存储有效期不能当作欢迎语可发送期限。

OneID：欢迎语短路径不要求 Customer，正常入客仍用原有显式建客；后续可关联已有 canonical Customer。持久化：可信回调 Inbox + 本地原子接纳 + Provider write/external effect。

## 2. 固定行为

1. 复用现有验签、解密、Corp 校验与回调幂等；辨识渠道状态并读取已准备好的不可变欢迎语版本。
2. 在短事务持久化回调、受保护 grant、欢迎语意图/必要绑定和现有效果接受，然后快速 ACK；DB 失败不能返回伪接受。
3. 欢迎语提交到现有发送内核及时执行。必要时仅为 welcome effect 在共享 River 分配独立注册队列/并发，原执行/claim/receipt 内核不复制。
4. 不等待 OneID/customer sync、员工重新分配、标签写、画像或临时素材上传。只有当前已可用的完整素材快照可用于发送；不得默默降级丢掉用户配置的附件。
5. 普通回调仍进入既有新客流程，负责可信建客、档案和渠道归属。欢迎语记录后续关联客户不改原意图、目标摘要或幂等键。

## 3. 期限与结果合同

- 20 秒是可调用欢迎语的业务窗口，不是 ACK 窗口。冻结首次可信接收时间和 deadline，重复回调不能延长；在出网前检查仍有效，超时返回 expired/not_attempted。
- 回调接纳目标为正常隔离测试 P95 <= 1 秒；从首次接纳到开始 Provider 调用 P95 <= 5 秒，并验证普通发送队列拥堵时不超过业务期限。不能承诺 Provider 网络故障也必然送达。
- 同一回调/同一 welcome grant 的逻辑操作只有一次；使用已有 grant 保护、effect receipt 和稳定摘要，不用 CustomerID 作为前置幂等材料。
- Provider 结果明确成功才记 executed；超时或调用结果不明按现有 unknown 语义处理，禁止新键重试。已知过期与未调用错误不能误记成发送成功或 unknown。
- 不用普通营销消息补发过期欢迎语，不把快速 ACK 宣称欢迎语成功。

## 4. Owner 与窄接口

wecom 拥有可信回调和受保护 welcome grant；channel 拥有配置与业务发送引用；outbound 是唯一欢迎语 Provider writer。可新增窄的 callback-scoped welcome 接纳 Port，携带回调引用、grant 引用、渠道配置版本、素材快照摘要、首次时间与 deadline；不要求虚假的 customer_id。

效果状态和记录继续复用现有模型；如果数据结构目前强依赖客户，做可兼容的最小迁移，将欢迎语待关联与正常入客分配分开，不制造第二套队列或客户表。旧入口不能再重复接纳同一欢迎语。

## 5. 协作与前端

本任务主责 callback 接纳与 welcome effect 路径；提前将事件分派接口告知总控供 09 复用。客户同步逻辑由 01 所有。渠道 QR、分配算法、旧码迁移、打标和管理页仅做回归，不新增产品需求。保持冻结 UI。

## 6. 必须测试

- 合法/错误签名、无 welcome_code、未知/失效渠道、重复回调并发、原收据重放。
- OneID 尚不存在、客户同步阻塞、普通队列拥堵时 welcome 仍可独立尝试；后台入客仍只建一个客户。
- PG 接纳事务各阶段失败、提交后进程重启、出网前重启、调用后回执写失败；不得丢意图或重复效果。
- 期限边界、重放不续期、素材未准备/过期、Provider 失败与 unknown；超期不得出网。
- 单独和共享回调集成测试、相关 race/PG；输出延迟测试条件和结果，不把模拟回执视为真实送达。
- 未合并 PR 交付；本轮零真实 welcome 发送、零生产配置修改。
