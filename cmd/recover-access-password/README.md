# Access 超管密码恢复（服务器维护命令）

仅 Linux root/sudo 可运行，同时要求真实、有效的 CRM admin/super_admin 会话。该入口不在 HTTP 或普通 aicrm-cli 中开放，不绕过原治理 API 对超管目标的拒绝。

由唯一发布工作台从准确候选构建此维护程序并验证 Linux/隔离 PG 旅程；维护程序无需替换或重启应用。数据库配置复用本机已安装 V4 的环境，只使用可信服务器配置，不把数据库 URL 或密码输出。

```sh
recover-access-password --mode inspect --target-id <id> --username <username>
recover-access-password --mode apply --target-id <id> --username <username> \
  --expected-version <inspect结果> --authorization-reference <不含密钥的用户授权引用>
```

apply 的标准输入是 JSON 对象，恰好包含 `password` 和 `session_token`。通过内存 pipe 传入，不把实际值放在 shell 参数、日志、源码或普通报告。保留原密码的空格，不重复尝试未知结果。目标、用户名、唯一超管控制与版本不匹配则拒绝。

密码哈希、会话版本递增、审计同事务提交；角色与绑定不改。审计关联真实管理员 operator ID，明确额外权限来自 `linux_root`，不制造超管会话。完成后用新密码通过生产 HTTPS 登录并读回原账号角色；确认旧会话无效和审计存在。工具失败/超时先 inspect 与新密码登录核对，再决定恢复。

原密码不可从哈希读回，回退应执行另一次准确版本和授权绑定的密码恢复。无需数据库迁移或常规全库备份。
