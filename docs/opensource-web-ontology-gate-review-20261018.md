范围更正（2026-10-18）：用户确认 People 为独立系统，原清单中其他业务接入事项属于外部 MCP，均不属于此次 Orrery Web/API 开源要求。此前把 25 项过期豁免列为开源阻塞的结论已撤回。默认覆盖门禁现按 Web/API 范围通过，外部完整集成另行验收。最新范围和验证见[首发范围说明](opensource-web-mcp-scope-20261018.md)；下文为此前阶段快照。

# Web/API Ontology 手动门禁核验（2026-10-18）

本轮核验对象是私有 Web/API 候选工作树，不是线上服务。Ontology 是本仓库的业务建模与 Context Graph 覆盖检查，不是新增第三方开源依赖；以下缺口也不是许可证侵权结论。

已修复两个检查问题：PowerShell 脚本此前在 Go 门禁失败时仍返回进程退出码 0，现在明确传回 Go 退出码；审计报告此前只列有效豁免并要求其数量大于 0，现在分别保留有效、过期及缺少截止日期的项，输出每项责任人、原因和日期及完整 JSON。实际发布硬门禁仍使用当前 UTC 时间。

修复后完整 Ontology 包为 **408 项通过、1 项失败、0 跳过**，退出码 1。失败仍是 `TestProductionCoverageGateRequiresEveryDefaultMappingRuleToBeClassified`，包含 **25 个 `expired_exemption`**。从 2 项失败降为 1 项是报告测试修正的结果，未减少业务缺口。129 条覆盖记录仍为：104 partial_connected、22 seeded_only、3 not_connected、0 production_connected；有效豁免 0，过期豁免 25，缺少截止日期 0。这是候选源码清单的分类，不能推断线上部署状态。

## 当前阻塞清单

下表由本次实际 Go 测试输出的审计 JSON 和失败明细逐项核对，统一责任人仍为清单原值 `platform-ontology`。`seeded_only` 表示已有预置映射但没有符合门禁的生产事件发布证据；`not_connected` 还缺少映射。到期不等于相关业务功能全部不可用。

| 域 | 事件 | 当前分类 | 原截止日期（UTC） | 后续处理 |
|---|---|---|---|---|
| development | `development.issue.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| development | `development.pull_request.opened` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| development | `development.release.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| development | `development.repository.registered` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| discussion | `discussion.message.created` | not_connected | 2026-09-15 | 先完成事件语义与映射，再接入业务发布、授权边界和投影测试。 |
| knowledge | `knowledge.base.created` | seeded_only | 2026-09-30 | 补知识库创建事件的生产发布，不能以现有文档/分块投影替代。 |
| legal | `legal.approval_case.opened` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| legal | `legal.contract.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| legal | `legal.entity.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| legal | `legal.policy.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| memory | `memory.profile.created` | seeded_only | 2026-09-30 | 补记忆 profile 创建事件的生产发布，不能以记忆项投影替代。 |
| operations | `people.membership.linked` | seeded_only | 2026-09-30 | 核对 People v2 替代关系、历史数据与消费端后再正式退役旧映射；不能只修改状态。 |
| operations | `people.team.created` | seeded_only | 2026-09-30 | 核对 People v2 替代关系、历史数据与消费端后再正式退役旧映射；不能只修改状态。 |
| permission | `permission.binding.updated` | not_connected | 2026-09-15 | 先完成事件语义与映射，再接入业务发布、授权边界和投影测试。 |
| planning | `planning.decision.recorded` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| planning | `planning.design_artifact.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| planning | `planning.requirement.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| resource_admin | `resource.mcp_server.registered` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| resource_admin | `resource.skill.registered` | seeded_only | 2026-09-30 | 补资源注册到 Ontology 的生产发布及投影测试。 |
| resource_admin | `resource.tool.registered` | seeded_only | 2026-09-30 | 核对直接图投影的产品范围；保留 Ontology 事件要求时补发布、映射投影及失败/重放/租户测试。 |
| todo | `todo.created` | not_connected | 2026-09-15 | 先完成事件语义与映射，再接入业务发布、授权边界和投影测试。 |
| value | `value.experiment.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| value | `value.initiative.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| value | `value.metric.recorded` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |
| value | `value.objective.created` | seeded_only | 2026-09-30 | 补业务事件发布、映射治理、图投影及成功/失败/重放/租户隔离测试。 |

生产能力已改走直接 Context Graph 投影或 People v2 的遗留项，需要先核实消费者、历史数据和产品范围，再决定正式退役旧事件或补接入。其他项需要实际业务链和覆盖证据。延长截止日期、删除失败测试或把未接入项直接标记为已接入不能完成这些工作。

## 手动验证

在 `orrery_v2` 下执行：

```sh
go test ./internal/app/ontology -count=1
bash ./scripts/check-context-graph-production-gate.sh
```

Windows PowerShell：

```powershell
& ./scripts/check-context-graph-production-gate.ps1
$LASTEXITCODE
```

当前上述完整包及两种门禁脚本都应返回 1。报告可单独提取：

```sh
go test ./internal/app/ontology -run '^TestProductionCoverageExemptionReport$' -v -count=1
python -B ./scripts/test_context_graph_production_gate.py -v
```

报告测试自身通过不代表发布通过；实际发布必须保留完整门禁。新脚本回归验证 PowerShell/Bash 的成功和失败退出码以及 PowerShell 缺少 Go 的失败路径；Windows 上 Bash 用例需设置 `ORRERY_GATE_TEST_BASH` 为原生 Git Bash 的绝对路径。测试的临时文件和 Go 缓存应按根目录 AGENTS.md 指向 D 盘。

本次手动验证还包括 5 项脚本进程测试、4 项 server 构建/部署门禁结构测试及 12 项源码导出测试，均通过。无新依赖、无数据库/OpenAPI/前端或业务运行时接入变更；未启动 Docker、CI、部署或发布。审计完整包的失败输出与其余通过项均保留在私有 D 盘证据目录。

当前素材门禁仍为 16 项已核验、0 待核验，24 项退役素材的历史来源仍保留未解决状态。镜像/原生库分发审核、其余业务验收与生产启用回执仍待完成。参见[当前发布准备](opensource-web-release-readiness-20261017.md)和[素材整理](opensource-web-documentation-brand-assets-20261018.md)。
