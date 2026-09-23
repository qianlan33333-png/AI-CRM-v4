# 核心产品与人群包中文配置体验

## 业务判断与范围
按用户最新指示，页面顶部参考素材库设置「核心产品配置 / 人群包管理」两项导航，每次只显示一个板块，禁止上下同时铺开。通过 ?tab=products / ?tab=packages 保留刷新和前进后退位置；切换保留规则草稿、人群包分组及分页。产品配置板块内部的已有能力按「配置产品 → 编写分配规则 → 验证并使用」呈现；三个步骤可往返，切换不丢草稿。最多五个产品，以紧凑表格展示；新增/编辑只打开一个产品，已绑定人群包不可改绑。产品信息独立于销售商品，仍复用现有人群包。
试运行先保存草稿，使用草稿验证，不写成员；正式分配使用已发布版本，明确确认实际入包。异步接受不得显示完成；结果展示中文状态、目标产品、理由、依据及是否入包。未发布、未启用产品时明确提示。发布不自动重洗存量客户。
所有固定模板用服务端中文名称和说明，内部键保持不变。成员明细的系统枚举翻译为中文；用户录入内容不翻译。未知状态显示待核实，不猜测成功。

## 架构与复用
OneID: reads canonical customer；保留现有 customers.id 输入和后端可信客户校验，无身份解析变更。
Persistence: 沿用现有本地事务、版本校验及推荐任务；不新建表、任务队列或外部调用。
终端：管理端；沿用 admin_base → admin_audience / admin_audience_detail、AudienceOperationsHTTP、aud-btn、aud-table、原生 dialog 和 AICRMConfirmation。只在模块内扩展样式。

## 设计依据
用户选定 artifact-template-crm 的 assets/reference.png：浅灰底、大块白色面板、蓝色操作、紧凑表格。Product Design image-to-code 应用于既有项目，生成单一方向参考，不替换管理端壳。
GitHub 参考：https://github.com/ant-design/ant-design/blob/master/components/steps/demo/simple.md ，通过 gh api 读取；借鉴分步导航，不引入组件库。
生成参考：/Users/qianlan/.codex/generated_images/01a09642-d877-7ae2-a9a7-3e245901b6f9/exec-cac37bab-a5d3-4e91-b2f8-ef5193c17924.png 。图中示例内容不写入生产；壳继续使用现有CRM品牌。

## 验收
中文模板显示但提交原始键；切换步骤和保存产品不丢规则草稿；试运行只提交 preview；正式分配明确使用已发布规则；单产品编辑、不可改绑、最多五个；结果及历史枚举中文；真实管理端产品保存/规则发布回读；桌面及窄屏无页面横向溢出。完成前保存视觉对照报告，区分本地验证、CI及生产发布。
