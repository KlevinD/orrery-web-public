# 手动验证与部署

2026-10-19 用户决定停用 Orrery 的 GitHub CI/CD，后续全部手动部署。
仓库级 GitHub Actions 已关闭，5 个已登记的工作流已禁用，主分支移除工作流定义。
push、PR、tag、schedule 和 workflow_dispatch 不再承担测试、构建或发布。
旧自动部署 cron 安装脚本保留为拒绝入口，不会安装任务。

## 手动验证

在专用分支固定提交，检查实际差异，按影响范围运行既有检查并记录退出码、失败、跳过项和制品校验和。
无 CI 状态不表示测试通过。涉及全部后端时运行完整 Go 回归；局部变更使用相关包及必要的接线回归。

```sh
cd orrery_v2
go test ./...
node scripts/operations/design-baseline/verify.mjs --changed-ref <本次基点>
```

Web 变更按 `web/package.json` 手动运行 `npm ci`、`npm run generate:api`、
生成类型差异检查、`npm run check`、`npm run test` 和 `npm run build`。
保留 `scripts/check-context-graph-production-gate.sh` 等本地验证入口。

Windows 的 workspace、worktree、产物、临时目录和缓存均使用 D 盘。
在构建前设置 TEMP/TMP/GOTMPDIR/GOCACHE；安装依赖的缓存也放在 `D:/Orrery/.cache`。
源码、构建参数、实际运行测试、二进制 hash 与提交应能对应，不能把其他提交的测试当作当前验证。

每次 Web/API 手动发布同时执行[第三方许可附件流程](../../../docs/third-party-license-bundle.md)。
原始许可证文本、根声明、字体许可、源码归属和清单需与实际发布锁图一致，并随制品提供。
许可附件单独更新时保留运行提交、旧源码归档和原始构建回执，分别记录附件来源及校验和。

## 手动生产发布

1. 核对当前 API、Web、独立 worker 的版本、配置和原业务合同，保留并行更新。
2. 在发布锁外完成测试、构建和不可变制品准备。写明目标提交、组件、源码/二进制 hash、实际验证与回滚目标。
3. 经已授权的手动发布命令，使用[生产发布互斥入口](./repair-deployment-concurrency.md)完成最终激活。
   获取既有发布锁后重新核对 current；版本变化时重新组合验证，不覆盖新发布。
4. 最终激活只暂停新维修派发，保持 worker 的扫描、回收、超时检查、Agent 报告及 heartbeat。
   API 重启前等待在途 Agent 收敛；超时退出，不强制结束业务，不借发布扩大权限或改变预算。
5. 核验实际运行版本、hash、健康、鉴权及受影响入口，确认派发恢复；失败时只回滚本次替换组件。

现有不可变生产环境使用 `production_release.py` 及审阅后的组件激活脚本。
根目录 `deploy.sh` 是另一种手动 Compose 环境入口，应仅用于明确选择的 Compose 部署，
不能用它替代当前生产实例的不可变版本和并发发布约束。

GitHub 仍用于源码、PR 审阅、合并及人工上传已验证产物；合并许可与部署许可按用户授权执行。
测试和技术恢复不能替代原业务验收、Agent 公开正文或真实渠道送达。

## 桌面候选

桌面同样手动构建和分发，不因 tag 自动打包。使用锁定的 Bun/Node 工具链，
在目标 OS 执行格式、lint、类型、测试、协议/合规检查和真实进程契约，
再执行[桌面发布门禁](../../../orrery-desktop/docs/desktop-release-gate.md)。
许可/商标审批、目标二进制校验、打包烟测、Windows 签名及 macOS 公证继续保留。
发布证据不全时不能把开发包当作正式签名版本，原自动更新渠道的禁用状态不变。

## 后续维护

未经用户新的明确指令，不重新启用 GitHub Actions，不恢复 workflow、不配置外部自动 CI/CD，
也不安装自动发布 cron。若旧机器存在带 `# orrery-auto-deploy` 的历史发布任务，
先核对归属再定向移除，不能删除普通用户业务定时任务或维修后台。
旧 workflow 在 Git 历史中可恢复，不作为当前操作入口。
