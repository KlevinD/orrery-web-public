范围更正（2026-10-18）：用户确认 People 为独立系统，原清单中其他业务接入事项属于外部 MCP，均不属于此次 Orrery Web/API 开源要求。此前把 25 项过期豁免列为开源阻塞的结论已撤回。默认覆盖门禁现按 Web/API 范围通过，外部完整集成另行验收。最新范围和验证见[首发范围说明](opensource-web-mcp-scope-20261018.md)；下文为此前阶段快照。

# WEB/API 默认 Agent 头像替换（2026-10-18）

8 张缺少原始分发依据的内置 JPEG 已从当前源码树移除，默认头像改用项目内编写的几何机器人图标。`workforce_avatar_artwork.go` 只使用 Go 标准库的图形和 PNG 编码功能，按固定颜色和坐标生成 512×512 图片；没有读取旧图片，没有引入外部图片、字体、模型输出或新依赖。生成结果一次缓存，每次读取返回独立字节副本，ETag 由实际 PNG 内容计算。

默认头像目录的 8 个 ID、旧 ID 别名和 API 路径保持兼容。默认选择保存的是 ID，因此旧记录即使含 JPEG 文件名、类型和 ETag，读取时也会取得新 PNG 与新 ETag；更新时间继续使用原记录。用户上传头像仍优先，删除上传头像后恢复对应默认图。生成器只接受固定文件键，不读取请求指定的文件路径。HTTP、权限和租户边界沿用原实现；没有修改 API schema、Web 类型或授权配置。

移除项为 `coordinator-agent.jpg`、`lead-agent.jpg`、`marlon-brando.jpg`、`qa-check-agent.jpg`、`qa-plan-agent.jpg`、`website-knowledge-curator.jpg`、`website-receptionist-customer.jpg` 和 `website-receptionist-public.jpg`。原 SHA-256、历史对应关系及待核实状态保留于 `compliance/web-asset-rights.json` 的 `retiredAssets`，旧图原件仅保留在私有审计目录。移除源码文件不解决历史版本和其他部署副本的权利问题。

当前保留的图片/字体文件从 35 项降到 27 项，16 项已核验，11 项仍待核验：

| 类别 | 数量 | 后续工作 |
| --- | --- | --- |
| 文档架构图和截图 | 9 | 核对原始来源与嵌入素材；必要时重新绘制或截图 |
| Minvia 图片副本 | 2 | 核对原始提供记录和分发范围，或替换 |

新头像由纳入源码审阅的程序生成，不是给旧图补发授权。源码导出、素材和 Ontology 门禁均未放宽；严格素材核验仍应拒绝这 11 项。完整镜像/原生库分发审查和既有业务验收缺口仍待完成。

手工验证覆盖头像目录与 PNG 解码、旧 ID/旧选择记录兼容、上传优先与重置、缓存副本隔离、内容哈希、权限/API 契约，以及前端头像显示和选择组件。Linux 验证运行完整 HTTP/server 包；具体命令、最终输入哈希、测试计数、头像预览和源码候选记录保留在私有 `default-avatar-replacement-review-20261018` 审计目录。GitNexus 未索引该工作树，已尝试 context/impact 并使用源码和测试核验影响。无真实外部服务、模型或消息调用。

本阶段为私有草稿准备，不代表公开发布或线上版本已更新。
