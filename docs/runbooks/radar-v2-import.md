# Radar v2 一次性迁移 Runbook

该命令是唯一允许读取 v2 Radar 的路径；API 运行时永远不连接 v2。读取列不含任何身份原值，旧点击只进入 `radar_legacy_events`，不会生成实时 UV、OneID 关联或已验证身份。

1. 用只读 v2 DSN 导出安全快照：`go run ./cmd/migrate-radar-v2 --mode inspect --snapshot /secure/radar-v2.json`。
2. 记录输出的 `snapshot_sha256`，执行 `--mode dry-run`，核对定义、事件与隔离数量。
3. 先完成 v3 Media 迁移；缺失或停用的图片/PDF 会进入 quarantine。
4. 在目标环境执行 `--mode import --actor-id <admin> --batch-key <reviewed-key> --snapshot-sha256 <digest> --confirm`。
5. 执行 `--mode reconcile`，要求 source、import、quarantine、mapping、legacy event 数量可解释。

迁入定义统一为 `disabled + unionid_required`，保留原 public code，复核后再由管理端逐条启用。工具不读取或推断 UnionID、OpenID、手机号、external_userid，也不调用 Provider。
