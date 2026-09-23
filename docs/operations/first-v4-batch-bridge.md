# 首个 v4 同包批次桥接操作合同

只在旧候选 `6088e57ccf63b62dff46b4e1` 仍 `observing`、生产 active SHA 仍为 `4928f94e53a100ffab73132c0f096c4c4ff6e1d4` 时使用。此桥不修改旧候选状态；只允许当前精确批次多占一次生产观察窗口。PR #5 的 PR #3 单项例外不得与本桥一起消费。

1. 先完成旧生产 receipt/当前 `/readyz` 只读核对，并保存带 `observed_at`（UTC、十分钟内）、`release_sha`、`tree_sha`、`package_sha256`、`readyz.release_sha` 和 `readyz.status` 的 JSON 读回。保留它的 SHA-256，不把旧真实业务项记为通过。
2. 完成最终批次 handoff。成员必须按 `members` 顺序包括 PR #15、#3、#13；PR #17 若进入同包则明确追加。`first_v4_batch_bridge` 字段指向桥 JSON。每个成员写准确 PR URL、当前 head/tree；不得抄旧 SHA。批次 handoff 必须按现有批次校验器证明 accepted package/receipt 与受影响旅程。
3. 桥 JSON 固定 `schema: 1`、`exception_id: first-v4-joint-batch-v1`、`repository: qianlan33333-png/AI-CRM-v4`。`legacy` 写旧 candidate/release/tree/package 和旧 production receipt 的绝对路径与 SHA-256；`v4_root` 写根 commit/tree；`batch_manifest_sha256` 写整个 handoff 的字节摘要。`batch` 写 candidate ID、base main、aggregate head、merge preview、candidate tree、package SHA-256、accepted receipt SHA-256；`members` 按 handoff 顺序写 `pr_url`、`commit_sha`、`tree_sha`。`production_readback` 写步骤 1 的绝对路径与摘要。
4. `queue_authorization` 与 `production_authorization` 是两条独立用户决定，且都必须在一次性入队之前取得并冻结于同一桥文件。每项包含 `decision: approved`、唯一 `decision_id`、`user_thread_id`、`user_message_id`、`decision_text`、`candidate_id`、`merge_preview_sha`、`package_sha256`、UTC `expires_at` 和 `evidence`。证据 JSON 须包含同样的 user/候选/包/preview/决定字段和 `source: user_message`，由绝对路径与 SHA-256 引用。没有用户决定时不要生成批准记录；PRD 或开发委托不等于这两项批准。入队后修改桥文件会破坏消费摘要并使晋级失败。
5. 校验与入队：`python3 scripts/release_first_v4_batch_bridge.py validate --bridge BRIDGE.json --batch HANDOFF.json --queue QUEUE.json`；候选已由指挥台凭 accepted receipt 验收、处于 `staging_acceptance` 或 `frozen` 后，执行 `admit`（同参数）。它在队列锁下将旧候选写入不可改绑的 candidate/桥摘要消费标记，再把新候选移至 `waiting_merge`。不重复执行 `admit`。旧观察不变。
6. 指挥台串行合并、核对合并后 main tree 与 accepted candidate tree。晋级前重新保存十分钟内的独立生产读回 JSON；它可以晚于冻结桥文件，但必须包含与步骤 1 相同的全部字段。使用既有 `release_control.py release promote --prepare/--execute`，两阶段都加 `--bridge-readback FRESH-READBACK.json`；它在精确批次上重验桥、当前 PR heads、旧生产读回、同包和队列标记。执行阶段还用 SSH 实时 `/readyz` 与这份读回比对。晋级器仍只在可信预发 Linux 节点运行，使用原有锁、固定 Host Key 和同一包；网络尝试前保留 attempt。结果不明只读对账，不重新发包。

任一 PR head、main、receipt、package 或读回变化，桥记录立即失效；重建候选、证据并重新取得相应决定。合并保护和 required checks 不变。桥消费后不能用于下一包。技术 `observing`、发送 ack、虚拟预发效果和用户口头陈述不能代替支付宝实付、企微入群码及旧三项真实业务验收。
