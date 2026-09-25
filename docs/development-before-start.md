# 开发开始前

1. 只使用 AI-CRM-v4 当前权威 `main`。国内切换激活前它在 GitHub，激活后在预备机裸仓；每个 Codex 开发任务建立独立 `codex/<work-item>` worktree/分支。
2. 写清用户动作、状态/错误分支与验收；检索 GitHub 或成熟产品参考，并确认本仓复用点。父任务形成一份简短 PRD，分类 OneID、Persistence、External Effects。已授权的拆分候选复用父 brief，不重复调研或请求确认。
3. 一个候选交付可独立上线的完整行为或明确缺陷，相关回归测试与代码一起提交。按行为拆分，不按行数拆分；涉及迁移时保持前向兼容，涉及 UI 时编码前使用 Product Design。
4. 开发者先运行 `python3 scripts/dev_preflight.py fast`；Go 改动再运行 `compile`，并执行受影响领域测试。`affected --base SHA --head SHA --dry-run` 给出候选检查范围；实际选定范围、执行结果与准确 head/tree 写入交接。缺环境、缺测试或未知影响时不能报通过，应扩大到全量。
5. 激活前按受保护 GitHub `main`、准确 PR `check` 与[旧发布流程](operations/domestic-release.md)提交。激活后只推国内裸仓 `codex/*` 分支并登记准确 branch/head/base；开发者不能改国内 `main` 或共享预发目录。候选基线过期时由原开发任务更新并重新检查。发布器按[国内主仓流程](operations/domestic-main-release.md)串行处理。

预备机使用合成数据测试，生产只在数据库迁移前备份。生产技术安装读回、真实支付和扫码等业务验收分别记录。发布失败由同一个发布执行者处理；需要代理接手时只用一个 `gpt-6-luna` max agent。
