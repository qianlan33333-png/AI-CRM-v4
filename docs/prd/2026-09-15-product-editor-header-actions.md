# 商品编辑页顶栏操作

## 目标与范围

普通商品和周期商品的编辑页目前在管理端单壳标题下再次显示“编辑普通商品”或“编辑周期商品”，并在正文摘要卡中放置顶级返回、保存操作。本次将这两个既有 DOM 控件移入唯一 `.admin-topbar` 的右侧操作区，隐藏重复正文标题和其摘要操作卡。

“售卖信息”内的保存、指标、素材/标签、购买后动作、周期商品成员能力及分销比例和退款复核等待天数不移动也不改写。取消、保存、重试、禁用和既有回调继续由 Product 页面原节点承担。

## 架构判断

```text
OneID: 不涉及；本次不读取或分配客户身份。
Persistence: 无新增；移动的是既有 Product Owner 表单节点，仍沿用原保存命令和事务。
External Effects: 不涉及；不调用 Provider、不创建任务、队列或效果。
```

## 前端装配

| 参考 | 复用 | 本次处理 | 受影响调用 |
| --- | --- | --- | --- |
| #338 标签与 AI 详情 | `mountPageHeaderActionElements` | 移动既有动作节点，保留 listener、href、busy/disabled 和 source-origin 回收 | 普通商品、周期商品编辑页 |
| #339 经营总览 | 管理端唯一 `.admin-topbar` / `.admin-topbar-meta` | 不创建第二标题或页面内 action row | 普通商品、周期商品编辑页 |
| 商品组件索引 | `productAdapter` 和 Product Owner 保存路径 | 保留各维度保存、商品指标、分销规则 | `productForm`、`spProductForm` |

Product Design 路由：本会话的 Product Design catalog 不可用，未执行其 audit/image-to-code；按已验证的共享组件和当前管理端壳实施，不生成平行视觉方案。

## 验收

1. 两类已存在商品编辑页仅有管理端壳标题；正文不再显示对应“编辑…商品”标题或顶级返回/保存摘要卡。
2. 顶栏右侧是原有返回和保存节点；点击、禁用和保存后的当前维度回读保持原语义。
3. 普通与周期商品在 1280、1440 宽度下均保留唯一顶栏、可见操作和原售卖信息分销字段。
4. 相关 Node、类型检查、构建和真实 PostgreSQL Chromium 旅程通过；发布闭包按 PR01 → Survey → new-shell 生成并绑定准确 HEAD。
