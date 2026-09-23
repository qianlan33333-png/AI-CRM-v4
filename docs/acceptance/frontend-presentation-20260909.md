# 前端展示细节优化 · 实施与验收

本次从最新主线 `7907723` 创建隔离分支 `codex/frontend-polish-0909`。保持后台布局、配色和业务语义，覆盖后台、企微侧边栏、H5 问卷与会员分享页。

## 架构分类

- OneID: not involved。新增代码只读取当前 DOM 和操作 Promise，不解析身份或改变客户归属。
- Persistence: stateless。新增状态均为页面生命周期内展示状态；不新增数据库、任务、Provider 调用或自动重试。
- 上传/保存继续由原 API 和恢复状态机处理，不变更请求体、权限、幂等键或 accepted/queued/outcome_unknown 语义。
- 冻结供体和 source index/lock 不变。V3 自有组件、样式及构建后注入是唯一改动入口。

## 实现

1. `surfaceFeedbackHost` 与三份分端展示样式纳入生成文档和服务端模板；初始 HTML 有 loading 占位，辅助 Host 异步加载，不阻塞正文解析。同源普通导航给轻量提示，前进后退清除；资源失败和长时间未挂载提供人工重载入口，不自动重提业务命令。
2. `actionFeedback` 绑定真实 Promise：商品/周期商品图片、雷达图片/PDF、素材库保存增加局部转圈和禁用。保留原始 DOM 节点及禁用状态；重复 change 在捕获阶段阻断，完成后可以再次上传。素材库原有回读失败/结果未知恢复保持不变。
3. 统一后台与移动端字号、行高、字重、焦点和控件尺寸；渠道五步结构保留，标题/统计/字段比例收敛；小屏客服行与后台导航正常进入文档流。
4. 会员分享页通过 `scripts/member-grid-presentation-source.mjs` 派生冻结模块。Owner 为 V3 Web presentation；原始 SHA-256 和转换锚点必须一致。只增加表格滚动、读取单飞、错误重试及保留已读列表；token 清理、分页游标和原接口保持不变。删除条件：会员分享源码正式转为 V3 唯一权威后移除此派生 seam。

## 自动验收入口

- 新增 `actionFeedback`、`surfaceFeedbackHost`、`memberGridFeedbackHost` 与商品/雷达上传反馈测试，纳入 `run-donor-view-consumers.sh check`。
- 原有商品保存恢复、周期商品素材、素材库、雷达、侧边栏发送恢复、问卷提交测试继续运行。
- 现有 `TestPostgreSQLAdminShellLayoutChromiumJourney` 增加真实脚本慢下载/失败截图，以及渠道 1024/1366/1440/1920 与 200% 等效视口检查。移动端 320/375/390/414 使用实际发布 CSS 的明确标注样例，与真实问卷/侧边栏业务 journey 分开记录。
- 发布清单、脚本引用、服务端模板资源与三个 stage 闭包都有校验。

## 验收状态口径

开发交付证据位于本地 `frontend-polish-evidence`（不打入生产包），含截图、演示和逐页检查清单。执行结果以该目录报告及本分支 Linux CI 为准。macOS 对两个现有 Chromium journey 有平台跳过，不能把该次整组检查标为全通过。

真实微信/企微客户端的软键盘、安全区和 SDK 操作需要客户端验收；浏览器样例不冒充客户端证据。本次不部署生产、不执行真实发送。
