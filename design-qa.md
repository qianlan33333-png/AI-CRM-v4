# Product Design QA — 规格、联系资料与结算

final result: passed

范围：用户选定的 3 个完整移动结算版本（none/mobile/shipping_address），保留问卷双展示及侧边栏现有组件。既有 Product Design image-to-code 路由；未新建页面体系。

## 对照证据

源图：本任务 exec-9623dbda（不采集）、exec-843957f1（手机号）、exec-040dd9d7（收货）。源图均 852×1846，统一归一为 393×852 CSS viewport。实现为 Go 实际模板导出的本地页面，合成课程/规格/优惠及联系资料 fixture，非真实认证/支付回执。

最终截图与同画布左右对照保存在 `docs/design/customer-contact-reuse/`；左为源图、右为实现。商品名“课程”、三人成行版 ¥999、优惠 ¥100、实付 ¥899、收货空表单、微信支付选中，状态一致。

第一轮 P2：区域占位过长导致窄列拥挤；收货块/支付块垂直间距偏大；支付宝图标反色；手机号单行缺少分隔；底栏按钮垂直位置偏低。已修订并重新截图比较。最终无 P0/P1/P2。P3：原生单选/地区选择箭头与不同平台字体存在细微差异；使用 Bootstrap Icons MIT 品牌轮廓，非原图栅格或表情替代。

## 已操作

- CUA In-app Browser：三个 393×852 空表单版本、折叠/展开规格详情、支付方式切换、常用电话和完整地址预填，均可编辑。
- 320px 窄屏 document clientWidth/scrollWidth 均 320，无横向溢出。
- 运行时控制台未见 error/warn。
- 问卷 all/one 结构化地址和晚到预填保护、侧边栏 staff 脱敏/admin CSRF 审计查看，通过实际 TS/DOM 旅程；前轮 CUA 已检查双模式表单。
- PostgreSQL + Chromium 管理商品规格保存/读取和付款页实际服务端旅程通过。

预发、真实微信/支付宝、实际客户权限及履约依然由发布工作台独立验证。本报告只确认本地范围的视觉与交互，不代表发布或真实支付成功。
