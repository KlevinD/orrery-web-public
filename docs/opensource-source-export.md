# WEB/API 源码候选导出

首发范围为 WEB/API。按 2026-10-19 用户决定，PageIndex 作为独立外部服务，
仅声明支持连接并提供[项目链接](https://github.com/VectifyAI/PageIndex)，其服务源码、
依赖和镜像不纳入此次交付。使用固定提交的 Git blob
生成候选包；不导出 Git 历史，不读取未提交文件，不改动原始上游快照。
候选包生成成功不代表已批准公开，也不代表整个仓库可改标为 Apache-2.0。
原始许可、NOTICE、迁入源码声明和资源许可继续适用。

## 导出与核验

在主仓库根目录手动运行，输出保存在 D 盘：

```powershell
$env:TEMP='D:/Orrery/.tmp'
$env:TMP='D:/Orrery/.tmp'
$env:PYTHONDONTWRITEBYTECODE='1'
python scripts/opensource-source-export.py build --repo . --profile web-api --output D:/Orrery/artifacts/web-api-source-candidate.zip
python scripts/opensource-source-export.py verify --archive D:/Orrery/artifacts/web-api-source-candidate.zip --repo . --require-asset-rights --require-license-texts
python -m unittest discover -s scripts -p test_opensource_source_export.py -v
```

`--revision` 可显式指定已审阅提交，默认使用 HEAD。工具拒绝覆盖已有输出。
每个包内的 `.source-release/manifest.json` 记录提交、文件模式、Git blob、SHA-256
和排除原因。带 `--repo` 的核验还会与完整已提交树比较，防止清单和内容一起被删改。
脱离 Git 的核验可检查包内一致性；交付时还应通过可信渠道核对整个 ZIP 的 SHA-256。

## 导出边界

| 范围 | 处理 |
| --- | --- |
| `orrery-desktop/`、`orrery_v2/clients/`、`orrery_v2/client/`、`orrery_v2/src-tauri/` | WEB/API 包排除客户端源码及附带文件 |
| `orrery_v2/cmd/desktop-core/`、服务端共享协议与 fixture | 保留原始源码，不能按名称误删服务端共享依赖 |
| PDF、可执行文件、原生库、压缩归档、数据库、Python 字节码 | 两种包均排除；未分类的可执行文件/归档/PDF 魔数会使导出失败 |
| `.env` 与非示例 `.env.*`、私钥文件名、本地依赖/缓存目录 | 排除；文件名过滤不代替内容凭据扫描 |
| `.github/workflows/` | 候选包排除，按当前要求手动验证 |
| 上游源码、许可证和声明、锁文件、WEB 静态资源 | 保留原字节；仍需各自的来源及许可审查 |

2026-10-17 固定树检查发现，`orrery_v2/clients/desktop-local/src-tauri/binaries/`
还跟踪一个 66,452,992 字节的 Windows 服务程序，不能只排除顶层桌面目录。
PageIndex 的 `upstream_pageindex/examples/documents/` 跟踪 9 份 PDF，其中包括
`PRML.pdf`。Docker 的样例排除不能覆盖普通 Git 源码归档；本工具在导出边界排除这些文档。
这不裁定文档侵权，也不授予其再分发权利。

## 仍须完成的审查

对实际候选包解包后执行脱敏凭据扫描，逐项审阅真实命中；保留已有代码质量失败记录，
完成首发所需回归。静态图片、字体、notebook 和文档引用也需要授权证据；本工具不对其自动背书。
当前保留的 16 项资源均已完成文件/许可对应；24 项旧图片已移出当前源码树，历史来源未解决的记录继续保留，见[资源来源核验](opensource-asset-rights.md)。
使用 `verify --require-asset-rights` 对实际候选包执行严格资源检查；当前 16 项均已核验，可通过。
同时使用 `--require-license-texts` 对包内锁图、组件覆盖与许可证据原文逐项校验；
结果单列上游仅提供协议引用的组件，不将它们表述为原始 LICENSE 已补齐。
根目录 `scripts/opensource-inventory.py` 描述完整私有仓库，包含桌面清单，不能当作
WEB/API 专用导出或导出后目录的自包含检查。首发专用清单使用 `python scripts/web-api-source-inventory.py --check`，可从解包目录运行。

本轮固定输入扫描分别命中 WEB/API 39 条、PageIndex 1 条。WEB 的全部命中行段与
`b0eadd7d03` 已审阅内容一致；PageIndex 命中为来源归档的 SHA-256 字段。
[逐项复核](../compliance/source-export-secret-review-20261017.json)保留原始扫描退出码 1，
未使用新增忽略规则将其变成零命中。后续候选需重新扫描并核对实际字节。

仅发布 Orrery 源码时，构建工具链的二进制分发义务需与源码包中实际包含的第三方文件区分。
分发容器或 Canvas 原生模块时，继续核对实际包含的库、MPL 文件对应源码、LGPL 链接条件、
GCC Runtime Library Exception 及声明交付；移除样例或生成源码包都不能替代这些工作。
[Mozilla MPL FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/) 和
[LGPL-2.1 原文](https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html) 是相关条款依据。

公开仓库、正式发行、合并与部署仍按[开源准备](opensource-preparation.md)单独处理。
