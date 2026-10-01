# 企微独立账号 UnionID 纠正

仅适用于已审核的两个独立企微账号，且旧 hxc UnionID 错绑到另一账号。通过现有 Identity Owner Store 执行；工具不直接写其他业务领域，Provider 只读。

保护计划字段参见 `internal/identity/port/account_correction.go`，仅包含内部 ID、版本、operator 和稳定 run_key。先确认最新候选、错绑身份、来源 subject 与两个客户版本，保存修复前备份。原始 UnionID、外部联系人 ID 只在内存读取，不能添加到计划或日志。

在 V4 指挥台核验的准确源码上构建该工具，先安装同一候选的 HXC 重放修复，随后使用生产现有受保护环境变量：

```sh
migrate-wecom-account-identities --plan-file /protected/reviewed-plan.json --mode inspect
migrate-wecom-account-identities --plan-file /protected/reviewed-plan.json --mode dry-run
migrate-wecom-account-identities --plan-file /protected/reviewed-plan.json --mode apply --plan-sha256 <inspect返回的摘要> --confirm-apply
```

真实 Provider 读取在数据库事务外。dry-run 执行全部状态校验并回滚身份、来源、候选、冲突、收据、证据及审计。apply 同事务提交，旧身份停用并保留历史归属，不移动订单、手机号、历史客户及运营事实。结果不明先按原 run_key 读回证据与审计；同参数重放不重复纠正，新参数不能复用已提交 key。

检查输出 `replayed`、两个新 UnionID 身份的内部 ID、停用身份 ID、拒绝候选、关闭冲突与 reviewed subject。独立读回两个客户均 active、分别持有企微身份和对应实时 UnionID，旧绑定 retired、历史解析收据保留、来源当前 customer 正确、审计一条、没有 merge 记录。

再经正常管理员 API 请求 HXC 看板刷新与企微全量同步；读取资料完整版本、标签基线及人群包 #27 的首次 source_rebase 校准和零运营触发证据。历史同步 conflict_count 保持原收据数字，不能直接修改为零冒充本轮成功。
