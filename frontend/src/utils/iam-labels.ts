import type {Change} from '../types/iam'
export const actionLabels:Record<string,string>={
 'report.authorize':'报表受众授权','report.subscribeTeam':'报表团队订阅','report.subscribeReview':'报表订阅审批','report.stopEmergency':'报表紧急停用','report.restoreEmergency':'报表恢复上线','report.reveal':'敏感字段明文查看',
 'dashboard.authorize':'仪表盘受众授权','dashboard.export':'仪表盘导出','dashboard.exportReview':'仪表盘导出审批','dashboard.subscribe':'本人仪表盘订阅','dashboard.subscribeTeam':'仪表盘团队订阅','dashboard.subscribeReview':'仪表盘订阅审批','dashboard.stopEmergency':'仪表盘紧急停用','dashboard.restoreEmergency':'仪表盘恢复上线',
 'self.profile':'本人资料','self.security':'本人账号安全','notification.self':'本人通知','platform.identity.manage':'平台身份管理','platform.sso.manage':'平台单点登录配置','platform.space.create':'创建工作空间','platform.space.manage':'管理工作空间','platform.ops.view':'平台运行状态','platform.backup.manage':'平台备份管理','platform.restore.execute':'平台恢复操作','platform.audit.view':'平台审计','platform.governance.recover':'治理恢复','source.manage':'历史数据源总许可（已退役）',
 'report.page':'报表入口','report.view':'查看报表','report.design':'开发报表','report.submit':'报表送审','report.review':'复核报表','report.publish':'发布与回滚报表','report.offline':'下线报表','report.export':'申请和领取报表导出','report.exportReview':'审批报表导出','report.subscribe':'本人报表订阅',
 'dashboard.page':'仪表盘入口','dashboard.view':'查看仪表盘','dashboard.design':'开发仪表盘','dashboard.submit':'仪表盘送审','dashboard.review':'复核仪表盘','dashboard.publish':'发布与回滚仪表盘','dashboard.offline':'下线仪表盘',
 'query.page':'数据工作台入口','query.read':'执行只读查询','query.write':'执行原范围的写操作','query.reconcile':'核实查询执行结果',
 'catalog.page':'数据目录入口','catalog.metadata':'查看元数据','catalog.preview':'预览数据','catalog.classify':'维护目录标注',
 'connection.page':'我的连接入口','connection.use':'使用本人连接','connection.manage':'维护本人连接','connection.proxy.manage':'维护本人代理','connection.task.view':'查看本人连接任务',
 'source.page':'数据管理入口','source.definition.manage':'维护数据源定义','source.account.manage':'维护正式执行账号','source.driver.manage':'维护驱动和模板','source.task.view':'查看本人可见的连接任务','source.runtime.view':'查看运行监控','source.runtime.manage':'运行启停管理','source.policy.map':'数据策略映射','metric.manage':'指标口径维护',
 'operation.page':'业务办理入口','operation.apply':'提交业务申请','operation.review':'审批业务操作','operation.execute':'执行业务操作','operation.reconcile':'核实业务执行结果','operation.template.manage':'维护业务模板','operation.account.manage':'维护业务执行账号',
 'change.page':'变更工单入口','change.apply':'申请数据库变更','change.review':'审批数据库变更','change.execute':'执行已批变更','change.reconcile':'核实变更结果','task.page':'待办中心入口',
 'space.page':'空间管理入口','space.members.view':'查看空间成员','space.members.manage':'管理空间成员','space.members.exit':'冻结成员与资产托管','space.menus.view':'查看菜单配置','space.menus.manage':'配置菜单显示','space.roles.view':'查看角色','space.roles.manage':'定义与复核角色','space.assign.view':'查看角色分配','space.assign.manage':'分配角色与治理委派','space.resources.view':'查看资源授权','space.resources.request':'申请资源授权','space.resources.revoke':'撤销操作许可','space.policy.view':'查看审批发布策略','space.policy.manage':'管理审批发布策略','audit.page':'查看操作审计','audit.export':'导出审计记录',
}
export const actionLabel=(action:string)=>actionLabels[action]??`${action}（后续阶段）`
export const changeLabels:Record<string,string>={action_release:'限期解除禁止操作',bundle_activate:'隔离权限配置启用',role_upsert:'角色定义',assignment_add:'角色分配',group_members_add:'增加组成员',delegation_add:'治理委派'}
export const stateLabels:Record<string,string>={pending:'待复核',applied:'已生效',rejected:'已退回',withdrawn:'已撤回',expired:'已过期',conflict:'基线冲突'}
export const governanceLabels:Record<string,string>={'role.propose':'提交角色定义','role.review':'复核角色定义','assignment.propose':'提交分配或解除禁止','assignment.review':'复核分配或解除禁止','group.propose':'提交组成员增加','group.review':'复核组成员增加','delegation.propose':'提交治理委派','delegation.review':'复核治理委派','menu.configure':'配置菜单显示','member.freeze':'冻结成员','restriction.revoke':'收紧许可和授权','bundle.prepare':'准备迁移包','bundle.activate':'启用已验收包','bundle.rollback':'回退权限配置'}

export const changeActions=(patch:Change):string[]=>patch.kind==='action_release'&&patch.action_id?[patch.action_id]:patch.actions||[]
