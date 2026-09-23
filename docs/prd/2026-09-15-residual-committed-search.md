# 管理端剩余提交式搜索

## 目标

将权限管理和问卷运营日志两个仍会在每次输入时执行搜索的 V3 实际入口，接入已审核的 [`committedTextSearch`](../../web/v3/shared/ui/committedTextSearch.ts) 合同。输入始终只是草稿；只有普通 Enter 触发现有读取或本地筛选。输入法 composition、`isComposing`、`keyCode 229`、候选确认 Enter 和 blur 都不提交。

[PR #287](https://github.com/qianlan33333-png/AI-CRM-v3/pull/287) 是共享合同与既有真实入口的参考：它以精确 selector 注册、保留调用方原有读取/分页逻辑的方式修复中文输入法，不做全局表单拦截。

## 分类与边界

| 判断 | 结论 |
| --- | --- |
| OneID / 外部身份 | 不涉及。权限页仅按既有 `wecom_userid` 查找候选企业员工；不解析、匹配、创建或合并客户/身份。候选员工不假定有 `staff_id`。 |
| 持久化、内部任务 | 不涉及。搜索只改变内存中的已提交查询与页面显示。 |
| Provider / 外部效果 | 不新增读取或写入。权限页沿用既有受权目录 GET；问卷日志只在已加载记录中本地筛选。开通、角色、绑定、登录和问卷推送命令均不在本次范围。 |

本次会话的 Skills catalog 没有可用的 Product Design 路由，故该步骤未完成；这是既有管理端控件的交互一致性修复，没有新页面、信息架构或视觉探索，继续沿用已审核的共享组件与页面样式，未安装、替代或伪称已使用该插件。

## 实际入口

| 真实调用方 | 当前行为 | 本次行为 |
| --- | --- | --- |
| `internal/webshell/static/admin_console/admin_access.js` 的 `#admin-access-search` | 每次 `input` 调用 `renderUsers()`，在已受权的员工列表内按姓名或精确 `wecom_userid` 过滤。 | 保留未提交草稿；Enter 后才更新已提交查询和现有本地列表。刷新继续使用已提交查询，即使输入框已有新草稿。 |
| 同文件的 `#admin-access-employee-search` | 每次 `input` 延迟 250ms 调用 `loadEmployees(true)`，函数直接读取 DOM，重置候选及暂选员工。 | Enter 后立刻作废旧请求并开始原有目录 GET。展示中的查询、游标和待执行查询分离：新查询失败时明确保留哪个旧查询的授权行，并禁用不匹配的“加载更多”，不会混用旧游标和新参数。若刷新把已暂选员工标记为已开通，锁定该行、清除暂选并说明原因。401/403 依既有权限失效合同清除敏感视图、关闭超级管理员转移弹窗并停止开通入口。 |
| `internal/webshell/static/admin_console/survey_operations.js` 外推记录输入 | 每次输入筛选行，空初始结果时 handler 可引用尚未创建的 `tbody`。 | 使用同一共享合同，只在 Enter 后对已加载 current/global 记录本地筛选。模式切换保留已提交查询与未提交草稿，清空后 Enter 恢复该模式的全部本地记录。没有 Provider 请求或受控测试派发。 |

## 验收

- 精确调用方测试覆盖：普通 Enter、composition/229 候选 Enter、blur、清空、问卷模式切换、权限页刷新/分页的已提交查询、目录乱序/取消、失败后旧游标禁用、已选员工刷新为已开通、以及 401/403 清敏感视图后旧 users GET 不复活敏感行或转移目标。
- 现有受权 PostgreSQL + Chromium Access Journey 验证目录读取只有普通 Enter 触发，且 `wecom_userid` 仍是唯一候选标识。
- 问卷已认证 Chromium Journey 覆盖本地日志的普通 Enter、候选 Enter、清空和空初始记录。
- TypeScript、构建、受影响 Host/consumer 测试与正式 manifest/stage 绑定最终提交。不会把定向验证写成全仓回归，也不会合并或部署。
