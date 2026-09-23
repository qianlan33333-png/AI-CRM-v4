# 共享组件状态示例页

## 业务判断

```text
OneID：不涉及。页面仅使用明确写死的本地示例记录，不解析、创建或关联客户／外部身份。
持久化：不涉及。确认仅更新浏览器内的演示摘要；不请求业务命令、数据库或队列。
Provider／外部效果：不涉及。示例不读取或写入 Provider，不上传、不保存、不发送。
```

## 依据、装配与边界

- 入口固定为既有认证后台路由 `/admin/component-states`：`internal/webshell/handler.go` → `Renderer.RenderComponentStates` → `admin_base.html` 的 manifest `componentStatesStyles`／`componentStatesHost`。不新增导航壳、公开路由或业务页面挂载。
- [PR #317](https://github.com/qianlan33333-png/AI-CRM-v3/pull/317) 已将 V3 `SelectionSession`、Tag、Staff 与 Content Composer 作为共享能力整合；本页直接复用 `tagPickerAdapter.ts`、`staffPickerAdapter.ts`、`contentComposer.ts` 与 `contentPresentation.ts`，不复制 dialog、IME、焦点或暂选逻辑。
- [PR #327](https://github.com/qianlan33333-png/AI-CRM-v3/pull/327) 说明 Material caller 的业务保存仍属于 Product/Radar Owner；本页仅把本地素材 fixture 传入 Composer，不把其目录、URL 或保存契约外溢到 Product/Radar。
- 冻结 donor 只作既有样式和行为参照，不修改。`componentStatesHost` 不安装真实 caller loader、不使用 `standardComponentsHost` 的真实范围，也不默认任何 `group_ops` scope。
- Product Design index 已路由核验：这是在既定 V3 视觉与组件合同内的普通实现，不触发视觉探索、原型克隆或体验审计 focused skill。

## 用户可见目标

`/admin/component-states` 明确标注“演示数据、未保存、未发送”。保留既有 Group／Material 卡片，并新增以下独立真实组件演示：

| 组件 | 本地 entity 契约 | 演示交互 | 不做的事 |
| --- | --- | --- | --- |
| Tag | `source=component-states-tags` + `tag_id`；分组与标签 ID 保持不同字段 | loading、空、首次失败后刷新、403 锁定、只读、失效已选、分组筛选、分页、IME 候选 Enter 与普通 Enter；确认、取消、重开回显 | 不给客户／商品／渠道打标，不发 tag 命令 |
| Staff | `source=component-states-staff` + 本地 `staff_id` + 可信 `user_id` | 同上；显示目录上限说明，失效或缺 `user_id` 的初选仍可查看／移除 | 不把外部 `user_id` 当作 `staff_id`，不读任何业务 scope 或变更负责人 |
| Composer | 与本地 Material fixture 相同的 `media-library` + `kind` + 数字 `id` | 本地文本、变量插入、图片／附件排序与预览、选择失败、确认、取消、重开，以及只读内容呈现 | 不上传、保存或发送；预览零业务效果 |

每张卡使用独立 entity IDs 和 `source`，以免出现 Tag ID、Staff ID 和 Material ID 跨实体混用。所有 loader 均是显式本地 async fixture；403 只锁定当前 session 并保留草稿，错误不伪装为成功。Composer 的只读例子以 `renderContentPresentation(..., {mode:'readonly'})` 展示和编辑预览同一 package，不开放编辑控件。

## 状态与交互合同

1. 临时选择只在 dialog 内存在；取消恢复最近一次已确认的演示摘要，重开由该摘要构造 `selectedRecords`。
2. Tag／Staff 的普通 Enter 或搜索按钮才提交 query；`compositionstart` 到 `compositionend` 期间的 Enter 留给输入法。分页／分组筛选使用当前已提交查询，不把输入中的 draft 偷偷应用。
3. 本地 loader 分别可表现 loading、empty、首次 error／refresh、403、readonly 与 invalid selected；失权、失效项不静默移除。
4. Composer 确认只更新同页的 local package 摘要。变量解析先采集全部 `{{token}}` 再由白名单校验，未知变量显式阻止确认；素材选择器的失败保留 Composer 草稿；取消不改变摘要；排序和 preview 随暂选实时更新。无受控缩略图的素材或补充卡片不保留空的 48px 视觉栏；有受控缩略图及其明确的加载失败占位仍保留该栏，避免详情错列或伪造缩略图。
5. Dialog 关闭会把焦点回到打开按钮；确认／取消都不能抢走仍连接的其他控件焦点。

## 验收与发布闭包

- 更新 `web/v3/componentStatesHost.test.mjs`：三个 adapter 的状态、确认／取消／重开、IME、失效／只读与 Composer package 排序／只读渲染；`contentPresentation` 覆盖无缩略图的单列详情，以及真实缩略图加载失败后保留明确占位的双列布局，素材与补充卡片同样适用。
- 扩展实际认证 `TestPostgreSQLComponentStatesChromiumJourney`：Tag、Staff、Composer 分别打开并完成关键状态，页面不发 `/api/` 请求；1280、1440、360、420 截图覆盖页面和至少一个新 dialog 的布局与焦点。Composer 在 1440／360 验证无缩略图的详情占满可用宽度、窄屏预览可滚到完整内容且固定确认可达。
- `npx tsc -p web/v3/tsconfig.json --noEmit`、状态 Host 定向测试、共享 Tag／Staff／Composer 合同测试、构建与 `stage-new-shell-ui`/install closure 通过。P5 绑定本 PR 的准确 main base 与 head。
- 组件目录只更新 `/admin/component-states` 的 Tag／Staff／Composer “状态示例”事实；不提升任何真实业务页面的 C3 结论。
