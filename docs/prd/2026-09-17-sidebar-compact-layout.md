# 侧边栏紧凑客户摘要与素材列表

## 分类与业务判断
OneID: reads canonical customer。只改变已有姓名、手机号状态、脱敏手机号、OneID 的布局，不解析或重新绑定身份。
Persistence: stateless。复用现有换手机号、图片发送和雷达复制链接操作，不新增 Provider 写入、持久任务或重试。

客户摘要常驻顶部，姓名与状态在左，手机号及 OneID 在右；换手机号保留为小型次要操作。相比折叠摘要，常驻能持续确认当前客户；相比把所有字段挤成单行，两列在 320–430px 更易读。模块导航保留现有六项和选中态，减少留白。

素材统一为预览 / 名称与标签 / 右侧操作三列。长名称最多两行，标签可换行但限高，无标签仍显示真实素材名称。图片保留发送；雷达保留复制链接，不把复制伪装成发送。窄屏不将按钮降到下一行，发送中禁用和失败恢复逻辑不变。空态、加载态、搜索输入法、分页及上下文切换安全不变。

## 参考
- Intercom Inbox: https://www.intercom.com/help/en/articles/6258745-the-inbox-explained ，客户上下文和工作内容共处侧栏。
- GitHub 搜索并阅读 Chatwoot ContactInfo.vue: https://github.com/chatwoot/chatwoot/blob/develop/app/javascript/dashboard/routes/dashboard/conversation/contact/ContactInfo.vue ，身份概要、详情字段和操作的层级分离。
- 实现复用本仓 V3 presentation.css 和审计过的 standard overlay 构建适配器；冻结供体不变。

## 验收
真实 Host Chromium 在 320、360、390、430px 检查不横向溢出、素材按钮位于同一行最右侧、长名称不会推走操作、客户摘要字段保留；复跑现有图片加载、搜索、发送恢复、OneID 上下文切换旅程。提供实际截图。360 业务时间线修复独立提交。
