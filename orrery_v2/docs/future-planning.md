# Orrery 后续规划

本文记录 2026-07-25 客户端与 Runtime Bridge 决策后的执行顺序。此前把 Web、thin desktop、Tauri local desktop 全部迁入 `orrery_v2/clients/` 的目录方案已经被替代，不再作为待执行任务。

## 当前产品根

```text
repository/
├─ orrery_v2/       Go control plane + SaaS Web + Server Runtime Bridge
└─ orrery-desktop/  AionUI + Electron/React + local AionCore + Device Connector
```

- `orrery_v2/` 是服务端协议、授权、设备状态、WorkOrder、审计和 Runtime contract 的事实源。
- `orrery-desktop/` 是本地路径映射、Workspace Grant、本地审批、设备凭据和 AionCore 生命周期的事实源。
- `orrery_v2/src-tauri/` 仅 maintenance-only，不迁移成新客户端，也不继续扩建。
- 两个项目由仓库根同一个 Git worktree 跟踪；客户端不得创建嵌套 `.git`。

## 当前实施依据

- [Desktop 客户端方案](../../orrery-desktop/docs/orrery-desktop-client.md)
- [Server Runtime Bridge 方案](./runtime/runtime-bridge.md)

## 建设顺序

### 1. 固定客户端事实

先完成客户端计划 Phase 0：固定 AionUI commit、AionCore 版本与 digest、许可证、真实本地 API、构建工具链和上游同步方式。Gate 未通过前，不导入大批源码、不改 Rust，也不承诺发布安装包。

### 2. 冻结 Device Protocol

先定义并测试 pairing、device connection、heartbeat、Workspace Grant descriptor、WorkOrder、receipt、Frame、ack/replay 和 terminal。协议只接受封装好的 typed method，禁止任意 method、shell、argv、env、绝对路径和云端创建 Grant。

### 3. 服务端落地 Runtime Bridge v2

在 Go 内引入 `Session + Command + Frame` contract 和 conformance suite，通过 compatibility adapter 渐进接入现有 PI、Codex、Claude 等 Provider。只在官方 SDK 强制要求时保留语言 sidecar，不增加全局 gRPC 层，不引入 Rust。

### 4. 客户端与服务端分别实现连接面

服务端实现 Device API、Device Gateway、WorkOrder Store 和 `DeviceRuntimeAdapter`；客户端实现凭据、主动 WSS、断线恢复、Workspace Grant、LocalAgentFacade、bounded spool 和本地审批。两端以版本化 schema 和 golden fixtures 联调，不共享业务代码。

### 5. 完成首条纵向链路

```text
一次配对
-> 客户端上线与心跳
-> 本地创建 Workspace Grant
-> 云端投递 agent_turn WorkOrder
-> AionCore 在本地执行
-> 本地审批暂停/恢复
-> Frame 与唯一 terminal 回传
-> 仅上传本地 policy 允许的 artifact
-> 断线重连后不重复执行
```

### 6. 再做安全加固与扩展

首期先实现权限控制、最小 capability、撤销、审计、幂等和 kill switch。OS 沙箱是后续独立加固层；在没有沙箱时，远程 Profile 默认不得获得 shell/network，高权限 Profile 只能用于可信用户和非敏感工作区。

## 当前不做

- 不把云端工作区做成本地文件系统的镜像或 WebShell。
- 不允许云端下发原始命令、任意 RPC、AionCore route、provider config 或本地路径。
- 不把本地 Agent Core 或服务端 Runtime Bridge 重写成统一 Rust 服务。
- 不为了统一外观给所有 Provider 增加一层 gRPC。
- 不把 AionUI 源码塞进 `orrery_v2/web/` 或 `src-tauri/`。
- 不在首期同步 Team、Cron、Channel、MCP/Skill 配置或提供云端文件浏览器。

## 完成判断

只有当双端 contract、负向安全测试、断线重放、唯一 terminal、撤销、secret redaction、Windows/macOS packaged smoke 和服务端 Runtime conformance 全部通过，客户端远程执行才能进入受控试点。
