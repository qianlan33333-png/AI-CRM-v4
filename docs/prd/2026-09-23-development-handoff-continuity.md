# 开发完成后的发布交接连续性

## 业务判断与缺陷合同

开发提交和局部验证完成，只能进入 `code_complete`。若预发布构建、业务自验或交接证据缺失，任务必须留下一个可查询、可重试的阻塞检查点；不能因消息投递失败或任务结束而消失。PR #13 的代码已提交，但当前 required check 因缺准确 staging receipt 失败，完整 CI 未跑，发布状态没有对应项，因此它属于 `code_complete` 后的开发阻塞，不属于 `handoff_ready`。

指挥台只能在真实 receipt、当前 main/head/preview 和 required checks 满足现有合同后接受运行时候选。检查点本身不占用生产串行队列；事件投递 `sent` 和接收 `ack` 都不代表预发验收或真实企微扫码成功。

## 参考与复用

- GitHub [MassTransit/Sample-Outbox](https://github.com/MassTransit/Sample-Outbox) 展示先持久化业务事实、再异步投递的 outbox 思路。
- GitHub [Azure-Samples/transactional-outbox-pattern](https://github.com/Azure-Samples/transactional-outbox-pattern) 展示持久状态与通知重试分离。
- 本仓复用 `release_events.py` 的 flock、原子写、幂等键、lease、重试及 ack；复用 `release_control.py` 的 PR 实时核对与 `release_coordinator.py` 的候选登记。不新增第二个通知队列或 staging receipt 生成器。

## 合同

1. `handoff checkpoint <manifest.json>` 只接收当前 PR head 的干净 worktree，核对 tree、PR 仓库与实时 base/head；目标状态文件必须已存在。
2. manifest 必填工作项、候选检查点 ID、原始任务、负责人、阻塞原因、后续动作、重试条件与证据。一次原子写同时新增 `blocked_development` 项和 `blocked` outbox 事件；重试相同输入幂等，不同来源复用 ID 报错。
3. 后续 `handoff submit` 仍走现有完整验证；handoff 必须用 `supersedes_checkpoint_id` 显式引用唯一活动检查点，并核对同一 PR、工作项和旧提交的源码祖先关系。允许新的任务 ID 接续；成功登记新候选时标为 `superseded_by_handoff`，保留旧事件。提交失败不得关闭检查点。新代码、新证据和新候选不能覆盖旧记录。
4. 新提交仍受阻时，新 checkpoint 也必须用 `supersedes_checkpoint_id` 显式接续同 PR/工作项的旧检查点，且源码是旧提交后代。旧检查点转为 `superseded_by_checkpoint`，新检查点继续阻塞。
5. 指挥台从持久状态发现未处理检查点，按 owner 和重试条件推进；通知仅提醒。任何 `sent` 或 `ack` 不改变业务状态。

## 分类、权限与验收

OneID：不涉及；发布元数据不解析客户身份。Persistence：涉及本地持久状态的原子替换；不写业务数据库。External Effects：只读 GitHub PR API、可重试 Codex 通知；不调用企微写接口。状态文件由发布指挥台授权路径持有，脚本拒绝隐式创建空状态。证据不保存 token、手机号或企微二维码。

验收：同一检查点重跑幂等；不同 payload/来源冲突；无状态文件、脏 worktree、PR head/base 漂移失败关闭；缺 owner/阻塞/条件失败；通知 `sent` 后仍保持 `blocked_development`；真实 handoff 成功才关闭检查点。回滚仅撤销脚本提交，不删除已有检查点或事件；已写事实继续由旧 outbox 读取。

## PR #13 边界

当前检查点记录当前 head、缺 staging receipt 与完整 CI、共享基线收敛依赖和真实企微扫码尚未验收。后续必须由当前 main + PR head 生成新 preview，在唯一预发布节点形成真实包/收据、完成受影响业务虚拟自验及真实扫码的独立验收安排，重跑相应检查后按 `release_control.py` 提交。生产旧计划 11 与 observing 候选不由本机制修改。
