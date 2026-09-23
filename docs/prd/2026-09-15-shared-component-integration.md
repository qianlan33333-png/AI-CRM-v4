# 共享选择器与内容呈现整合基线

## 目标

将已经分别完成并经真实页面验证的标签、客服、群聊、素材选择器，以及话术/素材编辑和只读呈现，收敛到一套实际装载的 V3 共享层。用户在管理端看到的是一致的选择、取消、只读、失败、权限变化、中文输入和键盘焦点行为；各业务页面继续使用各自的授权读取和保存命令。

本 PR 不新增业务页面，不把群运营作为其他领域的通用壳，也不修改冻结 donor。

## 开发前分类

```text
OneID: not involved — 共享层不解析、关联或创建客户；客服/标签/群聊 adapter 只使用调用方已有的受权读取结果。
Persistence: stateless — SelectionSession、Dialog、Composer 只保留浏览器本地草稿；调用方已有 PATCH/命令仍是唯一持久化路径。
External Effects: not involved — 本 PR 不上传、发送、排队或调用 Provider；预览和只读仅渲染调用方已读取的数据。
```

## 参考与现有基线

- 借鉴 [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) 的语义 token 和组件目录治理；不引入 Ant Design、React 或新依赖。
- 沿用项目已验证的 V3 `admin_base`、manifest 资产门禁和 `SelectionSession`/`SelectionDialog`。冻结 donor 仅作为行为证据，不能编辑或复制为新的页面框架。
- 真实挂载入口包括群运营 `groupOpsHostAdapter`/`groupOpsStandard`、客服与标签 Host、素材选择 adapter、Radar 素材调用、运营闭环 Excel 内容呈现和共享状态示例页。

## 输入栈与合并顺序

1. Staff 最终头 `9d52bfc0`，其已包含 Tag #305、共享 Session/Dialog #296 和群选择基础 #300。
2. Composer #307 `453013c8`：统一内容草稿、预览/只读呈现、素材/群邀请边界、嵌套顶层 dialog 和 IME/focus 合同。
3. Excel #310 `3e41758a`：运营闭环固定 `[text, excel_card]` 的真实调用；它不把 Excel Owner 合同推广到其他页面。
4. States #303 `d7a6eda8`：共享视觉 token 和状态示例 Host。
5. 保留主线 #304 的群运营 detail/groups epoch、A→B→A 和旧 finally 保护；不得在合并中恢复全目录群聊 crawl。

## 冲突保留规则

| 共同文件/入口 | 必须保留的合同 |
| --- | --- |
| `selectionSession.ts` | Tag 的 `reconcile(items)` 只补已选展示记录，不能改 draft、committed 或分页语义。 |
| `selectionDialog.ts` 与 CSS | Composer 的 optional search、initialFocus、全域 composition/Escape、textarea/select/contenteditable 焦点枚举；Staff 的 scoped 布局。最上层弹窗先关闭，组合输入 Escape 不关弹窗。 |
| `groupOpsHostAdapter.ts` | #304 detail/groups epoch、当前 scope groups projection、old-finally guard；叠 Staff 的受权成员读取、刷新 guard 和真实 picker 调用。 |
| `groupOpsStandard.js` / `materialPickerAdapter.ts` | `group_invite` 是调用方的素材命令语义，不能被当成真实群 `chat_reference`；选择确认失败保留页面草稿。 |
| `admin_base`、renderer、manifest/build、consumer runner | 将所有实际加载的 Host/样式/测试入口合并；缺 asset 继续失败，不能 silent fallback。 |
| 视觉 token/state Host | 只在显式装载的 V3 页面生效；不得泄漏为 donor 或移动/公开页样式。 |

## 验收

- 合并后所有 selector 的取消、确认、失权保留回显、异步旧响应、IME、Escape、焦点循环与刷新语义均由原有针对性测试继续覆盖。
- 群运营：detail/groups 读取保留 epoch 与 scoped projection，不发生未受权全目录读取；群聊 key 与群邀请素材 key 不混用。
- 客服、标签、素材、Radar、GroupOps 和 Excel 使用各自实际 Host 入口加载共享资产；无未挂载 selector 声称已覆盖。
- 运行 V3 TypeScript、构建/manifest、完整 `run-donor-view-consumers.sh check`、相关 PG/Chromium Journeys 及状态示例页。浏览器/构建串行运行，结果绑定最终干净 SHA。
