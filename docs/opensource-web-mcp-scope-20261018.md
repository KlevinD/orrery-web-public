# Orrery Web/API 首发范围与 MCP 覆盖检查（2026-10-18）

用户已明确：People 是独立系统，此前列出的其余业务事项属于外部 MCP，均不属于此次 Orrery Web/API 开源范围。此前把 25 项过期覆盖豁免直接列为 Orrery 开源阻塞，范围判断错误；该结论现已撤回。

## 为什么此前构建会失败

根目录 `deploy.sh` 调用了 `orrery_v2/scripts/check-context-graph-production-gate.sh`，API Dockerfile 也运行同一组覆盖测试。旧测试对全部预置业务事件进行严格检查，里面混有独立 People 和外部 MCP 的接入规划；它们到期后就让 Orrery 默认构建/发布检查失败。这是检查范围耦合导致的结果。

## 当前处理

- 默认 Web/API 门禁通过 `ValidateWebAPIProductionCoverageGate` 检查当前首发范围，保留证据、租户覆盖要求和当前 UTC 截止日期校验。
- 38 条外部集成记录移出默认发布要求：独立 People 的 15 条事件，以及其余外部 MCP 的 23 条事件。其中包含此前全部 25 项过期豁免；另外 13 条是 People 已接入事件。
- 排除策略按事件完整名称列举于 `orrery_v2/internal/app/ontology/production_coverage_scope.go`；新事件仍须分类，不按域名或“已过期”自动忽略。
- 原始 129 条清单、映射、发布者、截止日期和严格验证逻辑保留。MCP 集成是否完整由单独的验收检查报告。
- Orrery 的业务运行时、外部适配实现、权限判断、租户隔离和敏感数据保护未作修改。

当前默认 Ontology 包 **416 项通过、0 失败、0 跳过**；实际 PowerShell/Git Bash 门禁均应返回 0。Web/API 覆盖报告含 91 条现有 partial_connected 记录、8 个要求模块、0 条过期豁免。partial_connected 仍表示各记录存在明确的覆盖边界，门禁通过不代表所有规划功能已经完成。

外部完整集成检查仍报告原有 25 项过期豁免，退出码 1。这份报告供外部系统接入验收使用，已不作为此次 Web/API 源码发布的必要条件。

## 手动验证

在 `orrery_v2` 下执行默认检查：

```sh
go test ./internal/app/ontology -count=1
bash ./scripts/check-context-graph-production-gate.sh
```

Windows PowerShell：

```powershell
& ./scripts/check-context-graph-production-gate.ps1
$LASTEXITCODE
```

仅在验收完整外部 MCP 集成时执行：

```sh
go test -tags external_mcp_acceptance ./internal/app/ontology -run '^TestExternalMCPProductionCoverageGate$' -v -count=1
```

该命令当前预期返回 1；默认 API Docker 构建和部署门禁不会开启该验收标签。没有运行 Docker 构建、CI、部署或公开发布。其余手动检查包括 5 项脚本进程测试、4 项 server 门禁结构测试、12 项源码导出测试与依赖清单核验。

## 开源准备应关注的内容

后续审查围绕实际首发内容：导出源码的许可证/NOTICE/来源记录与敏感数据，实际分发镜像和原生库的清单与许可义务，以及 Orrery 自身功能、构建和安全验证。People 和外部 MCP 的业务完善单独跟踪；此前其他功能规划也应根据实际首发承诺安排。

当前源码素材核验为 16 项全部通过，24 项退役旧素材的历史来源记录继续保留未解决状态。客户端、IRIS 和独立 PageIndex 服务继续遵循既定首发边界。参考[准备入口](opensource-preparation.md)、[素材核验](opensource-web-documentation-brand-assets-20261018.md)及[此前完整覆盖审计](opensource-web-ontology-gate-review-20261018.md)。
