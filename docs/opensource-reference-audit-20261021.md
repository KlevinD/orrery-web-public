> Historical preparation record. The current 2026-10-26 source synchronization,
> validation results and remaining license gaps are recorded in
> [the current synchronization report](opensource-sync-20261026.md).

# Orrery Web 开源引用复核（2026-10-22）

本次复核以 `MinviaOrg/orrery-web` 的 `origin/main` 提交
`35f6b095c9e05a5cb1caca6622aa8c2fdec041b1` 为公开仓库基线；当前复核提交为
`e472d4b90573cecffb2ba058e8975a2a336f2d00`。公开仓库是 Web/API 源码快照，
不包含私有桌面目录、Tauri 维护目录或 PageIndex 源码。

## 当前实际使用的第三方项目

| 范围 | 当前内容 | 公开引用位置 |
| --- | --- | --- |
| Go/API | 97 个 `go.sum` 模块；Lark、DingTalk Stream、Centrifuge 的修改源码随仓库 vendored | `compliance/web-api-source-inventory.json`、`orrery_v2/third_party/*/LICENSE`、`THIRD_PARTY.md` |
| Web npm | 392 个锁定包（含构建和测试工具）；`@redocly/ajv` 固定为 8.18.3，使用 `fast-uri` | `orrery_v2/web/package-lock.json`、`THIRD_PARTY.md` |
| PI runtime | 118 个锁定包，入口为 `@earendil-works/pi-coding-agent` 1.3.0 | `orrery_v2/runtime/pi-coding-agent/package-lock.json`、`compliance/licenses/pi-coding-agent-1.3.0-MIT.txt` |
| 音频网关 | `scripts/audio-codec` 使用 `silk-wasm` 3.7.1；其 MIT 包许可和嵌入 SILK SDK 的 BSD/Skype notices 均已记录 | `orrery_v2/scripts/audio-codec/package-lock.json`、`compliance/licenses/silk-wasm-3.7.1-*.txt` |
| 可选桌面桥接 | Web 仍保留 `@tauri-apps/api` 2.11.0 与 `@tauri-apps/cli` 2.11.2；仅用于可选桥接/构建，不包含 Tauri Rust 源码或原生二进制 | `orrery_v2/web/package-lock.json`、`compliance/licenses/tauri-*-*.txt`、`compliance/npm-license-evidence.json` |
| 字体及 vendored 资源 | 10 个 Web 字体、Lark 文档图片及对应 OFL/资源证据 | `compliance/web-asset-rights.json`、`orrery_v2/web/static/fonts/*` |
| 历史来源适配 | AionCore `aionui-session` 的 Go 适配代码，保留 Apache-2.0 许可和来源清单 | `compliance/source-adaptations.json`、`compliance/licenses/aionui-session-76f5554-Apache-2.0.txt` |
| 外部服务 | PageIndex、Claude Code、Ory、OpenFGA、PostgreSQL 等只通过接口或部署配置接入 | `THIRD_PARTY.md`、`docs/third-party-integrations.md` |

PageIndex 按产品边界只保留上游支持链接和 HTTP 集成说明，不将 PageIndex 源码、样例 PDF
或独立镜像混入 Orrery Web 仓库。PageIndex 服务自身的 Python 依赖和镜像许可仍由其发布方负责。

## 本轮已补齐

1. Web 锁文件与当前生产依赖同步，移除 `uri-js-replace@1.0.1`，固定
   `@redocly/ajv@8.18.3` 和 `fast-uri@3.1.8`。
2. `opensource-inventory.py` 按当前树选择实际存在的锁文件；公开仓库不再强制读取缺失的
   `src-tauri/Cargo.lock`、`orrery-desktop/bun.lock`、桌面 SBOM，同时纳入音频锁文件。
3. `web-api-source-inventory.py`、两个 SBOM 和许可证证据纳入 `silk-wasm`，并清除原来从
   私有单仓库残留的桌面/Rust 记录。
4. `NOTICE`、`THIRD_PARTY.md` 删除当前公开树不存在的桌面引用，补充 SILK codec 的许可文本、
   版本、路径和哈希。
5. 重新生成 `source-inventory.json`、`web-api-source-inventory.json`、`sbom.cdx.json`、
   `web-api-sbom.cdx.json`、`assets.json` 和 `dependency-audit.json`。
6. 移除旧提交生成的 `.source-release/manifest.json`，并忽略导出器生成目录，确保按文档
   运行源码候选包导出时不会把过期元数据当成仓库源码。
7. 将 Web Vite 更新到 `6.4.5`，修复 `<=6.4.3` 的 launch-editor advisories；Web、PI、
   audio 的 npm audit 均为 0。

## 仍需单独处理的事项

下面是当前仍缺的发布内容，按阻断级别排列；它们不属于本次 Web/API 源码引用修复已经关闭的范围。

### P0：正式公开前必须完成

- GitHub 当前显示 `MinviaOrg/orrery-web` 的可见性为 **PRIVATE**。需要仓库所有者决定改为公开仓库或发布公开镜像；本轮没有擅自改可见性。
- 需要建立正式 release/tag 和发布清单，绑定源代码提交、源码 ZIP SHA-256、SBOM、依赖审计结果、资源核验结果和可见性。当前只有 PR 分支和本地候选包，没有正式 release 附件。
- 发布说明必须再次明确边界：当前公开包是 Web/API 源码；桌面/Tauri Rust、原生二进制、PageIndex 源码和 PageIndex 镜像都不是该包的授权对象。

### P1：分发镜像或独立服务前必须完成

- `docker-compose.yml` 使用 `postgres:17-bookworm` 和变量化 API/Web 镜像，PageIndex overlay 使用变量化 `PAGEINDEX_IMAGE`；需要按实际发布版本记录不可变镜像 digest、基础镜像（Go/Debian/Node/nginx/Python）及操作系统包的 SBOM、许可证和 notice。
- Ory Kratos/Hydra/OpenFGA 的具体镜像版本、digest 和镜像内依赖声明仍没有 release-specific 记录。源码引用链接不能替代镜像分发 notice。
- `MinviaOrg/Orrery-pageindex` 是独立发布单元：需要锁定 Python 依赖并生成 SBOM；明确 PyMuPDF 的 AGPLv3/商业许可路径；从镜像排除未核实授权的 `examples/documents/*.pdf`；补充该仓库自有代码的 LICENSE/NOTICE。主仓库只保留支持链接和 HTTP 契约，不能用主仓库声明覆盖这些缺口。
- 当前 Go/npm 清单主要是源码依赖元数据：97 个 Go 记录仍为 `license-text-review`，npm 记录仍为 `metadata-only`；只有明确捕获的 Playwright、PI、SILK 和 Tauri 许可证文本具备文件级证据。若发布二进制/镜像，必须补齐实际分发图的完整 notices 和许可证原文。

### P2：公开审查前应整理

- `docs/opensource-verification.md`、`docs/opensource-preparation.md` 及 `docs/superpowers/` 中保留历史阶段的桌面、旧测试和 sidecar 文字；它们应在发布入口处显式标为历史快照，避免被误读为当前 Web/API 门禁结果。
- `compliance/secret-scan.json` 的完整历史扫描仍记录了 promisor blob/网络中断限制。正式公开提交前，应对固定 release tree 再做一次完整源码扫描，并把范围、提交和结果写入 release 清单。
- 24 项被移出当前树的旧图片仍有未解决历史来源记录。它们不进入当前源码包，但若发布历史 tag、截图或镜像，需要单独处理权利和商标范围。

已关闭的源码级内容包括 Web/PI/audio 锁文件、Tauri/Playwright/PI/SILK 直接许可证证据、Go/npm 清单、资源清单、PageIndex 主仓库边界和源码导出校验。上述 P0/P1/P2 完成前，不能把当前 PR 描述为“所有发行形态均已开源合规”。

## 复核限制

当前 GitNexus 没有与该公开仓库提交匹配的索引；已有 Orrery 索引也落后于当前工作树。因此本轮
以源码、Git tree、锁文件、生成脚本和实际校验结果为准，并使用 GitNexus 结果作为不可用/过期的
限制记录，没有把空查询结果当成“项目不存在”的证据。

## 已执行的校验

```text
python scripts/opensource-inventory.py --check                 PASS
python scripts/web-api-source-inventory.py --check             PASS
python scripts/check-web-dependencies.py                       PASS
python -m unittest scripts/test_opensource_source_export.py    PASS (12 tests)
npm ci --ignore-scripts --no-audit --no-fund                   PASS (web/PI/audio)
npm run check                                                   PASS (0 errors, 0 warnings)
npm run build                                                   PASS
```

完整 Web Vitest 在当前 Node 环境下有 5 个 5 秒超时失败，结果为 1,284 passed、1 skipped、5
failed；失败分布在设置、讨论、任务详情和通知异步测试，未修改这些测试。将这 5 个失败文件
单独设置 30 秒超时后，29/29 个测试通过，表明失败来自当前环境下的启动/执行耗时。`compliance/
dependency-audit.json` 记录 Web、PI 和 audio audit 均为 0。
