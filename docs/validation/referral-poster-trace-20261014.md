# 裂变海报候选验证说明

已批准PRD：2026-10-14-referral-poster-trace。测试收据和实际截图位于 /private/tmp/aicrm-referral-poster-evidence；机器交接绑定最终准确head/tree，不把本段文字当测试收据。

Product Design：IAB 375/430实际构建组件显示、复制、关闭重开及console检查完成；根目录design-qa.md记录全图及昵称/二维码局部对照。视觉fixture使用当前活动原图加测试昵称/链接，独立真实PG Chromium旅程验证业务接口及导出。5份实际JPEG导出唯一二维码由zxing-cpp 3.1.1解码并等于对应期待链接。

修正已存在的OneID旅程测试断言：baseline a401b5d的free_rank_counter_and_detail_share_canonical_promoter仍要求邀请明细Participation.InviterCustomerID，当前安全投影不再返回此字段。改为核对EntryKey、邀请对象CustomerID、valid及ScoreDelta=1，继续通过两会话的计数/排名验证可信根；未修改业务查询或归因机制。

环境：macOS arm64，Go1.26.9、Node24.18.0、npm11.12.1、本机隔离PG16。Chrome旅程需在本机沙箱外启动临时profile；首次沙箱startup失败未计通过。npm使用独立临时缓存及真实lock文件，未修改系统缓存权限。

未验证：最终Linux应用构件、共享预发安装、生产安装/读回、当前活动5持久化坐标实际写入、微信iOS/Android原生长按保存及真实扫码归因。活动5slot1目标810/1932/370，保留图片快照与话术；生产部署/配置写入等待对应授权。通过结果仅是本候选的相关检查，不称全仓回归或Provider成功。
