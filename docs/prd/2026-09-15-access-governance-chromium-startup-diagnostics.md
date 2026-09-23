# Access Governance Chromium 启动诊断 PRD

## 背景与业务判断

GitHub Actions 浏览器门禁在 `run34915094898` 的 job `104211050309` 中，
`TestPostgreSQLAccessGovernanceUIChromiumJourney` 启动 17.95 秒后失败。CI 收据只
显示 `Chromium DevTools did not start`；同一串行执行的其余 13 个 Chromium
journey 都通过。这个结果阻止 Access 治理能力进入交付门禁，但不能证明 Access
页面、访问权限合同或 Chromium 镜像存在业务缺陷。

源码显示 Access journey 以 `stdio: "ignore"` 启动 Chromium，并只轮询
`DevToolsActivePort`。它吞掉文件读取错误，未监听 spawn error，也不检查子进程的
退出码或信号；失败后的 profile 被清理。因此当前失败不能区分启动失败、进程早退，
还是存活进程未在既有 8 秒预算内发布 CDP endpoint。

OneID：不涉及。此 journey 验证管理员和员工访问治理，不读取或解析客户身份。

Persistence：既有 Access 所有者 PostgreSQL 事务和回读仍由现有 fixture 负责；本项
不改 schema、业务状态或事务边界。

Provider／外部效果：沿用既有已验证企业目录读取 fixture；不增加 Provider 写、持久
任务、队列、重试或外部效果。

前端基线：保留现有管理端 Access UI、实际 HTTP 与 PostgreSQL Chromium journey 的
所有业务断言。改动只限测试启动观察，未增加页面、组件、交互或样式；冻结 donor
不变。Product Design 路由已核对，本项不审计或重设计用户体验。

## 参考与取舍

- 本仓 `internal/webshell/chromium_launch.mjs` 已提供 stderr 脱敏／截断，以及
  launch error、exit/signal、存活超时三种分类。`media_refresh_chromium_journey.mjs`
  与 `owner_handoff_chromium.test.mjs` 已用该模式。
- Chromium DevTools Protocol 的 [GitHub FAQ](https://github.com/ChromeDevTools/devtools-protocol/issues/55)
  说明 `--remote-debugging-port=0` 的 endpoint 会写入 stderr 和 profile 的
  `DevToolsActivePort`。因此采集受限 stderr 有助于归因，不能只延长等待。
- 不新建通用 launcher、重试或 CI 豁免。Access 仍使用既有约 8 秒启动预算；共享
  formatter 接受可选预算，以免失败消息错误声称其使用了其他 journey 的 30 秒标准。

## 范围

1. Access journey 将 Chromium stderr 保留在最多 1KiB 内存尾部，并监听 spawn
   error。
2. 每次未找到 `DevToolsActivePort` 后检查 launch error、exit code 和 signal；早退
   立即生成共享的脱敏诊断，超时则记录仍存活进程。
3. profile 清理保留在 finally 的既有有界终止路径；清理不得覆盖原始启动错误。
4. 共享 formatter 可准确显示 Access 的 8 秒预算，同时保持其他调用者默认的 30 秒。

不在范围：延长超时、重跑成功掩盖失败、跳过门禁、重试 Chromium、改变 browser binary
resolver／argv、改变 Access 业务断言，或修改 Provider／数据库合同。

## 验收

- fake Chromium 通过 `--version` 探测后向 stderr 写入 profile 和 URL 并以 23 退出；
  实际 Access journey 必须输出 `exit_code=23` 和 `<profile>`，且不得输出原 profile
  或 URL。这证明接线而不是只验证 formatter。
- 共享 contract test 覆盖默认 30 秒和显式 8 秒，以及 early exit、signal 与 spawn
  error 的分类、profile／URL／控制字符脱敏和 stderr 最多 320 字符的截断。
- 正常的 Access PostgreSQL + Chromium journey 仍完整通过现有业务断言。
- 下一次异常必须报告 launch error、exit/signal 或 `process=still_running` 中的一种；
  若仍是存活超时，另以该收据诊断 Chromium 启动条件，不能把本修复称为 Chromium
  根因已解决。
