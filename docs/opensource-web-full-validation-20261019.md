# Web/API 开源准备最终验证（2026-10-19）

本文是整合前候选的历史验证记录。PR #269 当前主线与生产整合版的结果以 [当前整合报告](opensource-pr269-current-integration-20261019.md) 和 [验证摘要](opensource-pr269-validation-20261019.json) 为准；本文的全量通过计数和镜像/冷启动证据不代表当前整合版已完成相同验收。

已补齐上一阶段的两项工程缺口：完整 PostgreSQL/API/Web 镜像构建和冷启动、Linux Go 全量回归。默认 Linux Web 镜像构建还发现 npm 10 对锁文件的真实兼容问题，已修复后重建、安装并完成全量验证。本轮继续只准备 Web/API 及所需共享源码，公开发布方式另行讨论。

## 修复内容

Node 22.23.3 / npm 10.9.9 的 `npm ci` 发现 Tailwind WASM 包缺少内置 `@emnapi/core`、`@emnapi/runtime` 两项锁记录。由实际 npm 10 重新生成锁文件，补回包内自带的 1.10.0 MIT 记录，删除两项无用的顶层记录并规范 peer/dev 标记。已有包的版本、下载地址和完整性校验值均保持不变。同步更新依赖清单、SBOM 和 npm 审计记录。

贡献指南已与实际 Web/API 范围和验证工具链对齐，不再要求解包用户安装客户端 Bun 工具链。LICENSE、NOTICE、第三方声明、SECURITY、环境变量模板和手动部署说明均已保留。

## 实际结果

| 检查 | 结果 |
| --- | --- |
| API / Web 完整镜像 | 两项均构建成功；API 镜像内 Node 22.23.3、PI 1.3.0 实际可执行。 |
| 全新 PostgreSQL/API/Web | 三项服务全部 healthy，Web HTML 和反向代理 API 正常，未认证请求返回 401。 |
| 登录与持久化 | bootstrap 管理员登录、任务创建和读取通过；停止并重新启动整套服务后，重新登录读取到相同任务。 |
| Linux Go 全量 | `go test -json -count=1 -timeout=30m -p=2 ./...` 退出 0；170 个包通过，7271 个顶层测试通过，计入子测试共 9583 项通过，0 失败。 |
| Linux Web 全量 | 1290 项通过，0 失败、0 跳过；Svelte 检查 0 错误、0 警告，生产静态构建通过。 |
| PostgreSQL / Node / PI 补充契约 | 隔离 PostgreSQL 及实际 Node/PI 环境下 106 项通过；AgentCore PostgreSQL journal 单独 1 项通过。 |
| Windows 空目录安装 | 最终锁文件 `npm ci --ignore-scripts` 通过，锁文件未改变。 |
| 发布支持检查 | 15 项源码导出/Compose 测试通过；完整及 Web/API 专用清单、Bash 语法和 Git diff 检查通过。 |
| npm 审计 | 最终 Web、PI 两份锁文件对官方 registry 审计均为 0 漏洞；这不是 Go/操作系统漏洞扫描结果。 |

Linux Go 全量中有 57 项跳过（含子测试）；其中未提供数据库或 Node 的相关项目已用隔离 PostgreSQL、Node/PI 补测。其余需要真实外部模型、账号、外部 MCP、People 或特定后端契约条件的场景保留明确跳过原因，不视为本次源码发布的业务验收。不能把 Go 退出 0 解读为所有可选外部集成都已验收。

## 源码、依赖与权利边界

本轮 Go / Web 应用实现没有改变，依赖修复为 Web 锁文件；另修复两处 PostgreSQL 专用测试夹具：生产 handler 测试按现有规则将 DSN 密码移入环境变量，任务并发提交测试使用与任务匹配的 MissionID。验证脚本分别创建 stall、inference、RSI 要求的隔离 schema；初次未配置 schema 和旧夹具导致的失败记录保留。随后两项用例因共享已升级 schema、以及明确检查 public schema 而失败，分别在全新且符合用例要求的环境重跑通过；原失败和重跑记录均保留，最终补测计数按用例去重，以最后一次结果为准。

原固定提交为 `062866c1bf766f02b818025fcbf3cbe06b2dd2c1`。Go 全量运行在这两处 opt-in 夹具修复前完成，当时它们明确跳过；修复后的最终内容已由 PostgreSQL 补测实际执行，其余 Go 文件逐项保持一致。验证用归档与最终候选逐项比对，测试输入文件 SHA-256 单独保存；旧候选与旧验证记录继续保留，不覆盖。

当前专用清单记录 97 条 Go 校验和版本、510 条 Web/PI npm 记录，SBOM 按包和版本去重为 590 项。它们包含构建和测试依赖，不是线上进程依赖数量。16 项保留图片/字体已核验，24 项旧图仍排除并保留历史来源记录；Apache-2.0 自有代码与所有保留第三方许可证、NOTICE、历史署名分别适用。

镜像为本地构建验证产物，不作为此次交付物公开分发。使用原 Dockerfile 和默认三服务拓扑；构建时复用已校验 Go 模块缓存，并从官方 ECR 入口取得基础镜像以绕过本机失效的 Docker Hub 镜像代理。API Debian 基础镜像以 digest 固定。冷启动仅按部署文档将隔离生产配置的 `auth.local.enabled` 设为 true，并生成独立随机凭据；未降低权限、修改数据库实现或启动 PageIndex。测试容器、数据卷和内部网络在结束后清理，未改动其他系统的数据。

仍沿用设计基线修订 7；本轮涉及 `ORR-CONFIG-01` 的首次部署验证及 `ORR-RUNTIME-01` 的实际 PI 运行环境验证，不更改任务、审批、权限、Agent 正文、知识归属或投递业务行为。GitNexus 缺少此工作树索引，query/impact/detect_changes 的限制及源码/测试替代证据分别保留。

本轮使用手动验证，不运行 CI，不合并、不部署线上、不上传公开仓库。客户端、IRIS、People 独立系统、外部 MCP 业务及 PageIndex 独立服务不属于本次交付。开源方式在准备确认后再讨论。
