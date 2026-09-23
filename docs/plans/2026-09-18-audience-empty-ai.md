# 人群包优先展示与 AI 空包配置优化

业务判断：新建包为空容器，绑定核心产品后由统一 AI 分配；历史规则包保留算法和刷新。列表编辑只修改名称与分组，不能改变成员来源。默认显示人群包，商品关联改为名称/编号搜索选择。

OneID: reads canonical customer through existing assignment APIs; no new identity behavior.
Persistence: Segment local PostgreSQL transaction with existing audit, receipt and Outbox. Empty packages have no current configuration; no schema migration or new queue. No new Provider write/external effect.

## 实现
- 创建 API 增加 creation_mode=empty；旧 template_key 请求不变。空包无 configuration/snapshot，不能配置规则或激活算法刷新，可复制为空包。
- 读取 API 返回 membership_mode=empty|rule|core_ai 与 core_product_id，基于持久配置判断。
- 默认人群页签；创建/编辑共用名称、分组弹窗，已有版本冲突保护。元数据允许运行中编辑，筛选配置仍要求暂停。
- 核心产品复用商品目录读取与标准选择交互；字符串 product_reference 保持兼容。旧规则包转 AI 前显式说明替换当前成员视图、保留历史。
- 空包/AI 包详情隐藏算法配置，链接到核心产品配置。

## 参考与组件
GitHub: https://github.com/ant-design/ant-design/blob/master/components/select/demo/search.tsx (already inspected); no framework dependency.
Product Design image-to-code: supplied screenshot and current CRM are selected target; keep approved layout/style. Shared selection component extension must be registered in component map. Existing tabs, dialogs, confirmation and HTTP mutation wrappers retained.

## 验证
真实 PostgreSQL：空创建/复制/读回、规则兼容、AI 绑定、刷新拒绝、元数据 CAS、历史保留。DOM/Chromium：默认页签、商品搜索/失败保留/清空、列表编辑、创建空包和绑定。发布门禁通过后正常发布，生产只读验收。
