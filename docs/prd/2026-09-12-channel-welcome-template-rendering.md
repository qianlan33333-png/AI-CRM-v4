# 渠道欢迎语客户名变量渲染

## 背景与问题

渠道欢迎语配置允许保存 `{{客户名}}`，但当前回调欢迎链路将配置正文原样交给 Outbound Provider。因此，已验证的一次欢迎外发可以执行成功，却仍向收件人发送未展开的变量文本。该问题是模板契约缺失，不是回调验签、欢迎码、队列或 Provider 送达失败。

## 分类与边界

- **OneID：读取 canonical customer。** 回调接收阶段仍不解析、创建、合并或绑定身份；它没有 `customer_id`，且其 20 秒窗口不得等待后续生命周期。首次外发前只在已关联 canonical `customers.id` 时，通过既有 `customer/port.DirectoryDisplayNameReader` 读取本地、安全展示名；不访问身份表，不调用 Provider，不隐式建客。
- **持久化与外部效果：本地事务 + 既有 Provider write。** 渠道继续在回调 Unit of Work 内冻结并接受同一个 `outbound/channel_welcome` EER；不新建队列、Worker、重试核或 Provider writer。正文快照由 Channel 持久化，Provider 调用保持在任何数据库事务之外。

相关参考：[#58](https://github.com/qianlan33333-png/AI-CRM-v3/pull/58) 的欢迎码与 Outbound 边界；[#232](https://github.com/qianlan33333-png/AI-CRM-v3/pull/232) 的安全 Customer Port 读取、加密冻结与读失败语义；[#235](https://github.com/qianlan33333-png/AI-CRM-v3/pull/235) 和 [#236](https://github.com/qianlan33333-png/AI-CRM-v3/pull/236) 的渠道状态边界。

## 用户可见契约

1. 欢迎语仅支持精确变量 `{{客户名}}`。保存配置时，未知、残缺或带空格的模板标记返回中文修正提示：`欢迎语目前仅支持 {{客户名}}；请删除或改正其他变量后保存。`
2. 每个已接受欢迎意图的首次外发前，系统渲染一次。已关联 Customer 且本地展示名非空时替换为该名称；尚未关联 Customer 或名称为空时替换为“朋友”。插入的名称不再递归解析。
3. 仅当正文包含 `{{客户名}}` 时才读取 `DirectoryDisplayNameReader`。纯文本和仅素材欢迎不受资料投影短暂故障影响；需要变量时，读取出错不是“缺少姓名”：本次不领取欢迎码、不调用 Provider，以可观测的 `customer_name_unavailable` 重试结果结束。读取恢复后仍以同一 EER、同一幂等范围继续。
4. 已存在的历史非法模板不删除、不猜测也不原样外发；首次执行以 `welcome_template_invalid` 安全终止，管理员需修正并保存新的配置版本。
5. 冻结后的最终正文按已配置 Provider 的 4,000 rune 上限校验；变量展开超限时以 `welcome_message_too_long` 安全终止，不截断姓名或正文，也不领取欢迎码或调用 Provider。密文容量覆盖合法 UTF-8 正文。
6. 已冻结正文以后，即使客户展示名或投影变化，所有重试均发送完全相同的正文。密文损坏、AAD 不匹配或无法解密时以 `frozen_message_unavailable` 安全终止，不调用 Provider。

渠道管理页在欢迎语输入框旁常驻显示精确变量和“朋友”兜底说明，但不在前端解析或改写正文。服务端仍是唯一的校验和渲染实现，避免出现两套变量规则。

## 冻结、加密与并发

回调接收时，既有 EER 的 `source`、`target`、`payload` 和 `policy` digest 继续绑定该回调、欢迎码、不可变渠道配置正文/素材快照和发送策略；这些 digest 绝不在后续渲染时修改。首次外发只为该已接受的 `effect_ref` 追加 Channel 自己拥有的单行正文快照：

- 新表 `channel_welcome_message_snapshots` 以 `welcome_intent_id` 为唯一键，保存配置模板 digest、渲染正文 digest、AES-GCM 密文、密钥版本和时间；不保存明文姓名、正文、外部身份或欢迎码。
- AAD 绑定 Channel intent、effect reference 与已接受 envelope 的稳定标识。快照插入及 Customer Port 读取在一个短 PostgreSQL Unit of Work 内完成；Provider 网络调用随后在事务外进行。
- 对欢迎 intent 加行锁，唯一键和不可变表 guard 保证并发首次执行只有一个快照；其余执行只读取、认证并解密该快照。不会创建第二个 EER 或改变现有 EER digest。
- 密钥从既有回调 AES 输入做独立域分离派生；不增加配置项、不记录密钥或明文。

## 验证

- 领域与 HTTP：合法 `{{客户名}}` 可保存；未知或残缺标记被明确中文提示拒绝。
- PostgreSQL：迁移拒绝任一部分 NULL 的 effect envelope；密文不含测试模板或测试姓名；Customer 关联缺失、空名、冻结后改名、并发首次冻结均符合上面契约；快照不可更新、删除或截断。
- Outbound：变量读取失败、非法历史模板、超出 4,000 rune、密文不可用、发送期限已过都不领取欢迎码、不调用 Provider；纯文本在目录读取故障时仍可发送；读取恢复后可重试；Provider 调用没有持有数据库事务。
- 运行时旅程：回调接受、后续 canonical Customer 关联、冻结、EER worker 与 Completion Sink 走真实 UOW，验证重试不会变更正文或 EER digest。
