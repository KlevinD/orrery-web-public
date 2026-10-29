# 四项 JavaScript 自研替换验收（2026-10-26）

> 主仓库 PR #319 的原始验收记录。个人 Web/API 仓库仅同步相关源码，不继承下列桌面验收或另一提交的通过结果；本次实际验证见[私有同步记录](opensource-self-developed-sync-20261026.md)。

基点为主仓库 `aaaac0cf3c63f7df6590eea8bf000212faa7dad8`，修改覆盖 Web、PI runtime 和桌面。
四个兼容包为 `@orrery/ast-reference`、`@orrery/source-location`、`@orrery/error-callsite`、
`@orrery/proxy-negotiate`，均为本仓库私有本地依赖，附完整 Apache-2.0 LICENSE 和 Minvia NOTICE。
实现、安装接线与升级规则见 [orrery-js README](../orrery_v2/runtime/orrery-js/README.md)。

## 已验证

工具链：Node 22.23.0、Bun 1.3.14。所有验证在本地进行，Actions 保持关闭。

| 检查 | 结果 |
| --- | --- |
| 自研包及实际调用方集成 | 16 项通过；涵盖 Svelte ESM/CJS 编译、Rollup 引用/错误位置/应用 sourcemap、Vitest 真实 CallSite、普通/Basic/Negotiate 代理 |
| Web 类型检查 | 0 errors、0 warnings |
| Web 测试 | 258 个文件，1347 项通过、1 项跳过 |
| Web 生产构建 | 通过 |
| 桌面 TypeScript 检查与 Bun package 构建 | 通过 |
| 桌面所选回归 | 54 个文件，532 项通过、9 项失败、1 项未处理错误；见下文基点对照 |
| 根级许可、清单、源码导出测试 | 54 项通过 |
| 生产许可附件测试（Windows） | 新增源码证据检查 4 项通过；10 项 POSIX 链接/锁测试跳过 |
| 干净安装 | Web `npm ci`、PI 生产依赖安装、桌面 Bun frozen install 通过；同时验证 Docker 所用的相对目录布局 |
| 许可原文与来源摘要 | Web/API 592 个组件通过离线核验，`publisher_reference_only` 为空；自研源码、完整许可、接线文件均绑定 SHA-256 |

桌面失败集中于 `browserController`、`browserIpc`、`cloudPanel`、`deviceConnector` 四组测试。
在独立工作树、未经修改的同一主分支基点及原依赖上复现了相同 9 项失败和 1 项未处理错误；
没有把这些既有失败记为通过，也没有为本次依赖替换改动产品验收条件。

本次清单刷新还更新了既有来源记录中三个已过期的本地 Go 文件摘要：Claude `session_connection.go`、
Codex `session_backend.go` 和 `session_connection.go`。它们的源码已由基点包含的其他变更修改；
此次仅更新摘要，保留原上游归属、提交与许可，不修改这三个 Go 文件。

## 验证边界

本机 Docker daemon 不可用，未构建完整容器镜像。代理回归使用本地真实 HTTP 代理和原生 Kerberos
客户端替身；真实企业 KDC、票据、TLS 代理及平台签名安装包未验证。Windows 不支持的 POSIX 附件
链接/部署锁测试仍需在 Linux 执行。源码证据检查与附件构造检查不等于生产部署成功。

受影响的行为契约为 ORR-UI-01、ORR-RUNTIME-01、ORR-TRACE-01、ORR-DEPLOY-01；
附件校验同时涉及 ORR-REPAIR-01。设计基线的静态校验不代替上述运行回归。
当前 GitNexus 没有此工作树及提交的索引，定位与影响核验使用 Git tree、源码和相关测试。

本次仅关闭上述四个依赖的许可缺口，不代表整个桌面分发及全部资源的授权审查完成。
未公开仓库、未合并主分支、未部署、未发布制品，也未改变身份、审批、Agent 正文或渠道投递行为。
