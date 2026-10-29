# SaaS 客户端、服务端与协议边界

状态：Current

生效日期：2026-07-25

本文定义当前仓库的产品根和所有权边界。此前把 Web、thin desktop 和 Tauri local desktop 一起迁入 `orrery_v2/clients/` 的方案已被替代，不再执行。

## 仓库边界

```text
repository/
├─ orrery_v2/
│  ├─ cmd/server/       Go API / worker wiring
│  ├─ internal/         domain, application, platform, transport
│  ├─ runtime/          bundled server runtime assets
│  ├─ web/              SvelteKit SaaS Web
│  ├─ docs/             server/product/protocol source documents
│  └─ src-tauri/        legacy packaging, maintenance-only
│
└─ orrery-desktop/
   ├─ docs/             accepted desktop design and implementation plan
   └─ ...               AionUI source imported after Phase 0 gate
```

仓库根只允许这两个正式产品代码根。历史实现和临时参考 checkout 不再留在工作区，也不得作为构建、测试或运行时依赖。

## 所有权矩阵

| 能力 | 事实源 | 说明 |
| --- | --- | --- |
| 用户、租户、角色、授权 | `orrery_v2` | 服务端强制执行，客户端显示不是安全边界 |
| Mission / Task / Discussion / Evidence | `orrery_v2` | SaaS 业务状态 |
| SaaS Web | `orrery_v2/web` | 浏览器客户端，不接触本地路径或设备 secret |
| Runtime Session contract | `orrery_v2` | Go `Session + Command + Frame` 模型 |
| PI/Codex/Claude 等云端 adapter | `orrery_v2` | 保留 Provider 原生协议与语义 |
| Device API / Gateway | `orrery_v2` | 配对、在线、心跳、可靠投递、ack/replay |
| WorkOrder 持久化和审计 | `orrery_v2` | WebSocket 连接不是状态事实源 |
| 本地个人会话 | `orrery-desktop` | 云端不可读取或接管默认本地会话 |
| 本地 Agent UI 与文件预览 | `orrery-desktop` | 继承并收敛 AionUI 能力 |
| AionCore 生命周期 | `orrery-desktop` | 固定版本供应商二进制；首期不改 Rust |
| 设备私钥与 AionCore token | `orrery-desktop` | OS vault/main process，禁止进入 renderer/cloud/log |
| Workspace Grant 路径映射 | `orrery-desktop` | 只能本地创建、扩权和撤销 |
| 本地审批与本地 policy | `orrery-desktop` | 云端输入不能伪造批准 |
| Outbound spool / inbound dedupe | `orrery-desktop` | 断线恢复、幂等和唯一 terminal |

## 协议事实源

Device Control Protocol 的 source schema 位于服务端：

```text
orrery_v2/docs/api/device-control/v1/
```

该目录将在客户端实施计划 Phase 1 创建，包含 envelope、hello、WorkOrder、Runtime Frame、artifact manifest 和 golden examples。服务端 Go DTO 与客户端 TypeScript types 都必须对同一版本 schema 做验证；客户端 CI 校验 schema digest，不能维护一份手写的第二协议。

普通管理 API 继续以：

```text
orrery_v2/docs/api/mission-control.openapi.yaml
```

为源文件。Device Gateway 的长连接 envelope 不塞进普通 REST handler，也不直接复用内部 Go struct 作为 wire DTO。

## Runtime Bridge 与 Device Adapter

服务端业务只依赖统一的 Runtime Session contract：

```text
Application / Agent / Discussion / Worker
                    |
                    v
          Session + Command + Frame
             /       |        \
            v        v         v
          PI       Codex     DeviceRuntimeAdapter
                                |
                                v
                         Device Gateway / WSS
                                |
                                v
                         orrery-desktop
```

`DeviceRuntimeAdapter` 是连接点，不是任意 RPC 转发器。它只能把 canonical typed command 映射成协议允许的 WorkOrder，并把设备 Frame 映射回 canonical Frame。它不接收本地路径、shell、argv、env、provider config 或 AionCore method name。

完整服务端方案见：

- [Server Runtime Bridge 方案](../runtime/runtime-bridge.md)

## Desktop 边界

Desktop 以固定 AionUI 基线衍生，保留 Electron、React、本地 AionCore、会话、Workspace、Preview 和 Approval。Orrery 专有逻辑集中在 Electron main process 的 connector、窄 IPC、协议类型、product mode 和少量 renderer 页面。

客户端和服务端目录由同一个仓库根 Git worktree 跟踪；`orrery-desktop/` 不创建嵌套 `.git`。AionUI 必须通过可追溯的 vendor/subtree 导入提交和固定 commit 账本同步，不能粘贴来源不明的源码快照。

完整客户端方案见：

- [Orrery Desktop 方案](../../../orrery-desktop/docs/orrery-desktop-client.md)

## 安全边界

- 所有连接由客户端主动出站建立；服务端不能主动连接用户机器。
- 一次配对产生设备绑定凭据，支持撤销、轮换和 credential revision 失效。
- 云端只发送版本化、长度受限、可审计的 WorkOrder；未知字段和未知 method 默认拒绝。
- 服务端保存 `grant_id` 和脱敏 capability descriptor，不保存或选择本地绝对路径。
- WorkOrder 的能力是 cloud policy、Agent profile、设备声明和本地 Grant 的交集，任一方不能单独扩权。
- Artifact 默认不上传；上传范围必须同时满足云端请求和本地 policy，并经过 manifest、大小/MIME、secret scan 与 digest 检查。
- 本地批准只由本地用户或本地持久 policy 产生，云端消息和模型文本不能视为 approval。
- 权限控制不等于 OS 沙箱。首期高权限 remote profile 只允许可信用户和非敏感工作区。

## 构建与发布边界

- `orrery_v2` 使用 Go、SvelteKit 和现有 server CI，生产部署仍由根级 Compose/部署脚本管理。
- `orrery-desktop` 使用 AionUI 实测确认的 Node/Bun/Electron/AionCore 工具链，按目录触发独立客户端 CI 和签名发布。
- AionCore 的 Rust 构建复杂度只留在客户端供应链，不进入 Go 服务端。
- 任一 Device Protocol major 变化必须提供双端兼容窗口；不能靠同时部署假设维持正确性。

## 不允许的依赖

```text
orrery-desktop -> import orrery_v2/internal
orrery_v2      -> import desktop TypeScript/AionCore types
SaaS Web       -> receive device secret or local absolute path
Cloud          -> arbitrary shell/method/argv/env dispatch
Device Gateway -> use live WebSocket as durable WorkOrder store
```

## 近期执行顺序

1. 客户端 Phase 0 固定 AionUI/AionCore、许可证、真实 API 和导入方式。
2. 服务端创建 Device Protocol v1 schema 和 golden fixtures。
3. 服务端分别落地 Runtime Bridge contract 与 Device identity/Gateway/WorkOrder store。
4. 客户端落地 credential、WSS、Grant、LocalAgentFacade、spool 和本地审批。
5. 在 `DeviceRuntimeAdapter` 完成首条真实纵向链路与断线恢复。
6. 通过负向安全测试和 packaged smoke 后，仅进入受控试点。

不在上述链路完成前迁移 `src-tauri/`、重排 `orrery_v2` 大目录、增加全局 gRPC、修改 AionCore Rust 或建设通用远程文件/命令入口。
