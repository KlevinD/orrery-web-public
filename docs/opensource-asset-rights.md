# WEB/API 资源来源与许可核验

2026-10-18 最新更新：[文档与品牌图片整理](opensource-web-documentation-brand-assets-20261018.md)已移除剩余 9 张历史文档 PNG 和 2 份 Minvia 图片，用源码 Mermaid 图、验证说明和现有品牌文字保留信息。当前 WEB/API 候选保留 16 项图片/字体文件，16 已核验、0 待核验；24 项移除记录的原 SHA-256、历史来源和未解决权利状态保留在 `retiredAssets`，未改标为已授权。此前[默认头像替换](opensource-web-default-avatar-replacement-20261018.md)与[运行时素材替换](opensource-web-runtime-asset-replacement-20261018.md)分别移除了 8、5 张旧图。严格素材门禁现在可以通过，但不代表镜像、原生库、历史版本、商标或完整发布审查已完成。以下保留 2026-10-17 的 40 项历史快照。

固定输入为 `16fa7fe2471dffe79d512116380d1bab57f5c60f` 的源码候选。
保留的 40 项资源中，16 项已建立原始文件、完整许可证和本地摘要的对应；
24 项仍需来源或授权证据。记录在 [web-asset-rights.json](../compliance/web-asset-rights.json)，
完整仓库的 [assets.json](../compliance/assets.json) 同步引用该记录。
已核验资源保留各自许可证，根目录 Apache-2.0 不替代它们。

## 已核验的 16 项

| 资源 | 数量 | 实际证据 |
| --- | --- | --- |
| DM Sans WOFF2 | 4 | 与 Google Fonts 官方分发文件逐字节一致；完整 OFL-1.1 文本保留。字体内记录版本 4.004 |
| JetBrains Mono WOFF2 | 6 | 与 Google Fonts 官方分发文件逐字节一致；完整 OFL-1.1 文本保留。字体内记录版本 2.211 |
| 飞书 Go SDK 文档图片 | 6 | 与 v3.9.9 固定提交 `ff207b774541a195f0a98c5bfda1507905e45431` 原始文件逐字节一致，完整上游 MIT 文本保留 |

DM Sans 的本地许可文本与官方完整文本仅有换行和第 21 行行尾一个空格差异；
JetBrains Mono 文本归一化换行后完全一致。原有许可文件保持不变，不把空格差异说成逐字节一致。
清单分别记录本地完整许可摘要、官方原文摘要，以及字体/图片原始来源 URL 和文件摘要。

OFL 允许按其条件随软件分发字体；继续附带版权和完整许可，不将字体改标为 Apache-2.0，
也不将字体单独售卖。使用这些字体不要求把 Orrery 代码改成 OFL。
官方依据见 [SIL OFL FAQ](https://openfontlicense.org/ofl-faq/)、
[Google Fonts DM Sans 许可](https://github.com/google/fonts/blob/main/ofl/dmsans/OFL.txt) 和
[JetBrains Mono 许可](https://github.com/google/fonts/blob/main/ofl/jetbrainsmono/OFL.txt)。
本轮未修改、重新生成或替换字体和图片。

## 尚待核验的 24 项

| 类别 | 数量 | 缺口与处理方向 |
| --- | --- | --- |
| 架构图、截图等文档图片 | 9 | 关联原始生成/截图记录，核对画面内容和公开范围 |
| 默认 Agent 头像 | 8 | 目录有生成头像的历史提交，但尚未恢复每张图的原始生成及权利记录；文件名不能证明真人照片或授权 |
| Minvia 品牌图的两份副本 | 2 | 两份字节一致，仍需关联原始提供记录和品牌分发范围 |
| 备案图标 | 1 | 设计记录为用户提供；尚无适用于其他运营者的开放分发依据 |
| 飞书、钉钉登录图标 | 2 | 固定原始来源和适用的品牌使用/分发条款 |
| 登录静物图、选中态背景图 | 2 | 找到作者、来源与许可，或用原创资源替换 |

这些资源保持待核验状态，不据此认定侵权，也不把 Git 提交、现有入库记录或“生成”字样当成完整授权。
涉及替换时应保持头像映射、业务身份、登录路径和权限行为。

## 历史来源补充核验

24 项待核验资源均已固定到字节一致的历史 Git 文件，清单中的 `repositoryCorrespondence`
记录提交、原始路径和 SHA-256。这能定位文件版本，不能代替作者或分发授权证明。

- 8 张现用头像均与 `bf9859a1` 的生成头像种子文件一致。`marlon-brando.jpg`
  在 `01a1689f` 中由 `legal-knowledge-agent.jpg` 完整重命名；文件名不证明它是演员照片。
- 两份 Minvia 图片均与 `719c9168`（2026-06-21）的 `ideado_v2/minvia.png` 一致。
  6 月 13 日较早版本的图片不同，不能拿该版本的来源代替当前图片来源。
- 两张图片设置页截图与仓库 Playwright 测试的视口和示例数据相符。
  实际画面中，两张都包含待核验的备案图标，1440px 截图还包含选中态背景图；
  即使截图由自己的测试生成，也需要处理这些嵌入素材的公开使用范围。
- Codex 历史保留了营销材料审批截图入库，以及备案图标与用户提供文件比较的记录。
  本地迁移档案从 8 月开始，服务器还保留 5—7 月档案；已检索其中相关记录，
  尚未恢复上述头像、装饰图、品牌图的完整原始生成/下载及授权证据。

本轮只补充来源链，不改变图片、业务身份、许可核验状态或严格检查门槛。

## 实际候选包检查

```powershell
python scripts/opensource-source-export.py verify --archive D:/Orrery/artifacts/web-api-source-candidate.zip --repo . --require-asset-rights
```

严格检查读取候选包内的来源清单，核对资源字节、来源摘要和包内完整许可摘要。
有待核验资源、新增未登记资源、过期摘要或缺少许可文本时拒绝通过。
普通检查仍可用于私有候选包，结果明确列出待核验和未登记路径；两种检查都不批准整个发布。
当前 24 项待核验，因此严格检查应拒绝，不能通过删除清单条目或忽略规则掩盖。

`scripts/opensource-inventory.py --check` 同时拒绝过期的资源和许可摘要。
它仍描述完整私有仓库；客户端资源不在本轮 WEB/API 权利结论内。
