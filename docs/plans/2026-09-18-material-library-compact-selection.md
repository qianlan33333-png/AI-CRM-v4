# 图片素材库选择与分组 UI 收敛

## 业务判断
仅调整用户截图对应图片素材库的布局，不改变素材分组归属协议。OneID: not involved；Persistence: stateless UI，继续调用现有 Media 本地事务接口；External Effects: not involved。

## 交互
每行勾选位于缩略图左侧。所属分组列只展示名称，无行内移动入口；单项归组在编辑弹窗完成。移除单独占行的“全部分组 / 已选”卡片，缩短搜索输入，在同一工具栏放“全选”“转移分组”和选择数量，未选择时转移禁用。全选仅当前页，再次点击取消全选；筛选或翻页清空。具体组的编辑组名、删除分组保留在紧凑工具栏，侧栏继续承担组名和计数展示。

## 参考与实现
用户截图为唯一视觉修改目标；GitHub 参考 Ant Design Table 的左侧行选择与批量操作语义：https://github.com/ant-design/ant-design/blob/master/components/table/index.en-US.md 。不引入该依赖。复用 GroupManagement、现有图片 toolbar 与 selectionDialog，通过可选布局入口扩展共享组件，附件、小程序默认布局不变。
Product Design 经插件目录查到但未安装，已提供安装入口，当前未调用该插件，不冒充插件产物。

## 验收
图片首列勾选顺序、组名纯文本、单行工具栏、全选/取消/禁用、筛选清空与批量转移保持有效；图片编辑分组继续正常。回归共享管理器在附件/小程序默认布局。现有真实 PostgreSQL Chromium Journey 增加布局断言并保留 CRUD；fast、前端检查、浏览器和 CI 按最终提交验证。
