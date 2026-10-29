# 文档证据范围

`marketing-material-approval.png` 是历史隔离界面检查的截图，原始截图输入和全部可见素材的公开分发依据尚未闭环。2026-10-18 已从当前源码树移除，原件和哈希保留在私有审计目录；未解决来源记录保留于根目录 `compliance/web-asset-rights.json` 的 `retiredAssets`。不将其作为当前生产审批或发布验收证据。

审批功能的可审阅依据仍在源码中：

- [HTTP 审批路由与权限测试](../../internal/transport/http/marketing_wechat_approval_routes_test.go)
- [版本化请求测试](../../internal/transport/http/marketing_wechat_versioned_request_test.go)
- [生产审批装配测试](../../cmd/server/production_marketing_wechat_approval_test.go)
- [HIL 审批服务测试](../../internal/app/hil/marketing_wechat_approval_test.go)

这些文件描述源码契约；它们不等同于线上部署回执，也不表示本阶段重新运行了审批测试。当前手工验证、候选提交和发布门禁见根目录 [发布状态](../../../docs/opensource-web-release-readiness-20261017.md)。新截图、日志、trace 和验证输出存入 `D:/Orrery/artifacts`，不放回源码证据目录。
