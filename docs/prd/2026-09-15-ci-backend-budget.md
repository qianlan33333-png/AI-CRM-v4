# Backend CI 时间预算修复

日期：2026-09-15
范围：只提高完整 backend CI job 的总时间预算，确保完整验证、结构化 receipt 和 artifact 能在收尾前完成。

## 业务判断

backend 是合并门禁的完整后端验证，不应因测试已完成后的 Go cache post-save 耗尽 job 预算而被标记 cancelled。成功必须同时保留完整验证和可追溯证据；已上传 receipt 不能单独替代最终 job 状态。

本次只把 `backend` 的 `timeout-minutes` 从 20 调整为 25。它覆盖 setup、完整 canonical verification、receipt、artifact 与现有 cache post-save 的总生命周期。25 分钟为已完成 canonical 1116.949 秒及收尾留出实际余量，同时覆盖 #286 在取消时仍未完成的约 18 分 55 秒下界；它不放宽测试结果。

## 已核实证据

- #284 attempt 1（run 34871202798）在 canonical lane 1116.949 秒后已完成完整验证、receipt 和 artifact；随后约 266 MB Go cache post-save 在 20 分钟边界被取消，backend job 最终为 cancelled。
- #286 attempt 1（run 34870582330）在完整串行 race 测试仍运行时，于约 18 分 55 秒被 20 分钟 job 上限取消；1135.394 秒只是取消时已运行的 canonical 时长下界，不能视为已完成的 canonical 最大值。
- #284 的较早成功 run（34869305196）为 backend 917 秒、canonical 850.620 秒，说明当前较慢 head 已超出原预算的稳定余量。
- 完整成功 run 中出现的 Postgres `ERROR:` 是受测约束拒绝的预期负例日志，receipt 为 success；它不是本次的失败根因。

## 架构分类

- OneID：不涉及。此变更只影响 GitHub Actions job 调度，不读取、解析或写入客户身份。
- 持久化：stateless。workflow 不写业务数据库，不创建内部持久任务。
- External Effects：不涉及。不会调用 Provider、产生外部效果或改变业务 receipt。
- 前端：不涉及。

## GitHub 参考与取舍

[actions/cache v4](https://github.com/actions/cache/blob/v4/README.md) 说明主 cache action 在成功 job 的 post 阶段自动创建未命中的 cache，也提供显式 restore/save 拆分方案。该事实解释了 #284 的 cache post-save 生命周期；缓存作用域、命中率和保存策略需要另行测量，本次不调整。缓存逻辑、key、restore 和保存行为均保持不变。

## 不做的事

- 不减少 Python Excel 测试、`go vet ./...` 或 `go test -p 1 -race -count=1 ./...`。
- 不使用 `continue-on-error`、不把 cancellation 或 receipt 伪装成成功。
- 不修改 frontend、browser、archive SDK 或其它 job 的时间预算。
- 不改 required check、质量报告的精确 PR head 参数、receipt/artifact 或 cache 配置。
- 不重跑既有 PR，也不部署或访问生产环境。

## 验收

1. `.github/workflows/ci.yml` 中只有 `backend` job 的时间预算从 20 变为 25；其它 lane 的 budget 不变。
2. backend 仍调用 `python3 scripts/ci/quality_lanes.py backend`，其命令序列仍包含 Python Excel 测试、`go vet ./...` 和 `go test -p 1 -race -count=1 ./...`。
3. backend 的 `if: always()` receipt、attempt-specific artifact 与 quality report 的精确 PR `head.sha` 传递保持不变。
4. 由本 PR 的新 head 触发完整 GitHub CI 后，最终审核须同时确认 backend job、canonical verification、receipt/artifact 和 required check；本地不镜像运行完整 race lane，既有 run 不 rerun。
