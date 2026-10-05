# 库存和问卷员工来源退役

用户于 2026-10-05 07:06 UTC 明确授权删除库存、问卷指定员工受众及其来源依赖，并将累计修复集中交付当前发布工作台上线。本 brief 继承已授权审查范围，不增加审批。

商品创建、修改、复制、普通商品及周期商品均按无限库存处理；页面和新接口合同不提供库存。历史 products.stock_quantity 保留原值，更新不触碰，新记录使用兼容占位 0；旧请求库存字段被忽略，旧幂等收据继续严格重放，不放宽其他输入摘要。

问卷仅用可信填写人身份归属，不接受客户端 staff_id；渠道和活动元数据、答案、其他模块员工权限均保留。两个问卷受众模板去掉员工条件。历史 all + 空员工列表仍具原有语义；历史指定员工配置保留原 JSON 和快照，但明确 definition_unsupported、要求主动重新配置，不静默变成全体。管理员页面显示失效原因；新执行及直接入口拒绝失效配置。已接受的不可变外部效果不重写。

OneID：读取现有可信 Survey session 和 canonical customer；不新增匹配或建客。Persistence：复用原本领域事务及收据，保留历史列；无迁移、删除或回填。External Effects：没有新增 Provider 调用；受众失效沿用原 gate，测试仅沙箱替代。

对外合同：退休字段从页面/响应/OpenAPI 移除；旧库存请求兼容忽略，旧问卷员工请求明确 400，旧指定员工受众明确失效。业务机制：无限库存，问卷事实仅按填写人；其他商品规则和受众机制保留。关联模块：Product/Sidebar/CLI、Survey、Segment、直接受众执行消费者。页面：复用现有 Host/表单结构，删除库存和问卷员工控件，对历史指定配置显示重新配置入口。验证：真实 PG/HTTP 历史数据保留、新记录和重放，模板/编译/执行失效，新页面和必要浏览器旅程；Linux 安装由现有工作台执行。历史数据可保留回滚，整包绑定最终 head/tree。真实支付、客户消息与 Provider 结果不由本次本地测试证明。

限制判断：历史指定员工配置若直接删除字段会扩大圈选范围；依据用户禁止静默扩大的明确要求，复用 definition_unsupported 并要求主动重新配置，作用范围仅两个问卷模板。无其他新增限制。

历史兼容 Owner 为 Product / Survey / Segment 原领域。库存字段仅在旧源码调用和旧不可变收据摘要兼容中保留，移除条件是历史调用及收据不再需要；不新增业务入口。历史问卷员工列仅供原记录读回，两个受众模板不再消费；旧指定配置必须用户主动重配，不能后台清洗或扩大。

## 2026-10-05 累计订单导出基线兼容整合

原aeca已被当前工作台接收，但累计批次tip为c58b0a7而非已安装main641。父会话协调按既有基线规则合入原aeca范围，保留订单导出c58全部业务源码。只合并OpenAPI/Composition/CLI catalog和两方发布短清单，CLI由合并后的OpenAPI重生；installed checks使用原commerce-readback/periodic-contact/audience-send-history/audience-confirmation，现有Phone维护build target不变。OneID/Persistence/External Effects沿原分类：不新增身份规则、事务或Provider效果；不涉及新增限制或页面设计。最终准确head复验发生交叠的合同与订单导出真实HTTP/PG、相关消费者，并记录未改源码的旧证据复用。原aeca分支/收据保留；新ref交原工作台，不自行安装环境。

## 2026-10-05 验证环境与直接消费者修复

ce545c 单父候选已由工作台实际执行：cmd/aicrm 的 30 项具名检查通过，两个 WebKit 因缺浏览器夹具跳过导致 CheckIncompleteError；前端首项读取未构建 dist 而失败，尚未构建/安装/晋级。用户及工作台限定本次只修验证问题，继续继承本批及订单导出授权。

macOS 的两个 WebKit 15.4/26.0 真实 HTTP/PG 检查按 [平台清单](../release-checks-macos.md) 运行；Linux 清单只保留未执行部分，30 项原保护日志结合实际输入不变证明复用。六个前端商品源码合同改用本 V4 跟踪的模板绑定与最小测试 DOM；最终构建 HTML/Host 挂载留给原同包 periodic-contact 安装验收。周期商品浏览器用例删除已退役 spfStock 填写，并对新建、普通及周期编辑页断言无库存字段/标签、Alipay 控件实际挂载。没有修改业务实现、接口、身份、持久化或 Provider 机制，没有新增限制及页面设计，也不改变安装环境。

工作台后续实际执行11ace7d：402项具名pass，Product HTTP整包夹带八个依赖jsdom的无关/未改浏览器合同导致准备失败，尚未构建或安装。本轮仅依据准确终态再减清单至23项：Product HTTP用已通过的八个CRUD/安全/退役库存直接合同复用，其他已通过检查以保护日志和输入blob不变证明复用；Survey/Segment/WeCom/Product Store未执行检查、前端及必要浏览器继续由工作台执行。不改业务、发布器、测试函数或安装通道，不重跑原阶段，原30/WebKit/周期/前端收据按原HEAD保留并绑定未变输入。


## 32865a2 执行后：最终构件旅程归安装验收

工作台准确32865a2 Linux执行：剩余后端165个顶层全部pass（保护日志513a177397804aa249acc32a376099d26df84ea8ad3c8de9ade41dfa553f6e9d），十项前端全pass（37c5beaff423c264bcf93f4a8ca0ce8b4cc645c569baf7f9473f13d87d476b09）。最后Survey退役浏览器在fixture准备时缺web/dist/asset-manifest.json而fail（45aace92d5dad925b6dc376d3cec9f2951424adb6591c1469945647f32b7871f），未完成业务断言，绝不当pass。该fixture明确依赖staged release artifact，因此仅从安装前browser lane移走；既有scripts/run-installed-audience.sh已包含TestSurveyAudienceOwnerRetirementChromiumJourney，最终同包必跑它及原send-history/confirmation三项。installed_checks四项与Phone build_commands保持原样。

22个已通过清单条目、原30/402与macOS两项WebKit的执行输入完全相同，保护日志/原HEAD收据保留复用，不重跑阶段。清单必须非空，仅保留一个validate-openapi源码合同。这里只改清单和说明，业务、验证脚本、发布器、OneID/事务/Provider/页面均不变，沿原c58单parent与队首/锁由工作台统一构建、安装和实际退役旅程；已有生产授权继续。Library/GitHub归档不是门禁，本次先国内push/readback和紧凑交接，不等待归档。
