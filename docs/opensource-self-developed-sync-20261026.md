# Web/API 自研替换私有同步 — 2026-10-26

本次从主仓库 `internal source repository` PR #319 的提交
`redacted`，向个人私有仓库 `private candidate repository`
的 PR #1 分支同步 Web/API 所需变更。个人输入为
`redacted`。仅导入相关文件，不导入主仓库 Git 历史、
桌面产品目录、Rust 图或桌面发布产物。

同步包括四个 Apache-2.0 自研兼容包、完整 LICENSE/NOTICE、Web/PI 本地依赖与锁文件、
Svelte CommonJS / Rollup 嵌入实现替换、Docker / 开发安装接线，以及绑定源码摘要的许可附件校验。
自研源码登记与主仓库原实现相同。保留个人分支的 **Vite 6.4.5**、`@redocly/ajv` 8.18.3、
已有来源审查元数据和退役上游包的历史复核记录。

## 许可与来源

重新生成的源码清单为 97 个 Go 条目、512 个 npm 条目、16 项素材，Rust 为 0；
Web/API 许可清单为 **592 个组件，publisher_reference_only 为空**。
四项此前缺口通过替换实现移出当前锁图，不声称原发布者缺失的原文已取得。
所有源码摘要、原文、素材和锁文件仍须通过离线校验，`redistributionApproved` 保持 false。

旧 Claude SDK sidecar 已从当前公开候选树移除。默认 API 与 Claude 原生 CLI 适配器不安装或执行 Agent SDK；相关私有备份和历史审计记录不属于本次候选发布。

## 本次实际验证

- Web `npm ci` 和 PI `npm ci --omit=dev --ignore-scripts` 均通过，安装后自研别名与嵌入实现校验通过。
- 自研工具及实际 Svelte / Rollup / Vitest / HTTP 代理调用方集成：16 项通过。
- Web 类型检查：0 errors、0 warnings。
- Web 全量测试：258 个文件通过，1,347 项通过、1 项跳过；生产构建通过。
- 根级许可、来源及导出测试：55 项通过。生产附件测试：4 项通过，10 项 POSIX-only 测试在 Windows 跳过。
- 两份来源清单和 592 组件许可证据检查通过；开发脚本 PowerShell / Bash 语法检查通过。

机器可读回执见[本次同步验证](../compliance/self-developed-sync-validation-20261026.json)。首次双工作进程测试运行因耗时被主动中止；上述通过结果来自随后按标准 `npm run test` 完整执行的运行，没有改变测试断言或验收条件。

本次实际重跑官方 npm registry 审计，结果仍为 Web 2 low / 1 moderate；PI 1 moderate / 1 high；
audio 为 0；旧 Claude sidecar 已移出当前候选，因此不再计入本次锁图审计。当前锁图摘要和完整公告在
[`compliance/dependency-audit.json`](../compliance/dependency-audit.json)。这是受影响依赖包计数，
包含开发和间接依赖，不据此宣称每项均在默认生产路径可利用。

设计基线 rev8 仍有 10 处对排除的桌面树的链接/引用错误，没有放宽验证器或删除契约。
已有 Linux 后端回执的 `main.go` 803/800 行结构门禁失败保持记录；本次不修改 Go 业务源码，
也未重跑完整 Linux 后端。Docker daemon 不可用，未做容器冷启动、生产部署、真实企业 KDC 或签名客户端验证。

本次 GitNexus `list_repos` 没有当前工作树索引，`detect_changes` 返回仓库未索引；
使用当前 Git tree、差异、源码和实际测试复核。保留 ORR-UI-01、ORR-RUNTIME-01、ORR-TRACE-01、
ORR-DEPLOY-01、ORR-REPAIR-01，未变更身份、权限、QA、Agent 原文或渠道投递合同。

仓库保持私有，Actions 保持关闭。本次同步不合并 PR、不变更默认分支、不发布 Release 或执行部署。
