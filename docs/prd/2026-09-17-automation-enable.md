# 自动化话术启停修复

## 业务判断
暂停项提供启用，启用项提供暂停；服务端原始状态为准。启用前检查配置、素材和发布版本；仅草稿未发布时明确确认“发布并启用”，其余阻断原因中文反馈。写入后 GET 验证状态与 execution_enabled；失败不能显示成功，重复点击不能重复提交。恢复操作入口，不批量启用现有话术。

OneID: not involved，话术配置不解析客户身份。
Persistence: local transaction，复用 Automation Owner 已有 publish/activate/pause 服务及事务、审计和幂等收据。
External Effects: 不新增 Provider 写入；启用后现有运行链路仍经原有出站边界。

## 根因及方案
canonical /admin/automation-agents -> automation UI -> RenderAutomation -> frozen admin controller/template。模板固定“暂停”，控制器仅提供 pause 回调；后端 activate 已存在。新增 V3 Host 包装既有控制器渲染值，沿用列表、确认框、toast、transport，不修改冻结供体。

GitHub 参考：https://github.com/n8n-io/n8n/blob/master/packages/cli/src/public-api/v1/handlers/workflows/spec/paths/workflows.id.activate.yml 。借鉴状态命令独立、服务端校验和阻断反馈，不引入依赖。

参考页面：用户截图及当前 agents 模板；复用：表格、confirmBox、toast、request；影响：自动化列表。Product Design Skill 当前目录不可用，该环节未执行。

## 验收
覆盖暂停到启用、启用到暂停、未发布确认、配置阻断、鉴权失败、重复点击、写后回读失败。Host 必须进入 manifest 且 canonical handler 装配。专项检查、完整 CI、部署、生产回读分别记录。
