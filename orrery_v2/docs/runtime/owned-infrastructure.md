# 自有基础设施与 AgentCore 接口

本文取代早期的第三方源码迁入计划。实现依据当前 Orrery 的业务契约、Go 标准库与明确记录许可的依赖；历史设计不再要求复制其他产品的基础设施代码。

## 消息与执行

Web 讨论应用服务通过 `internal/agent/core.SessionRuntime` 执行 Agent，通过 `SessionRuntimeRecordReader` 读取统一的 `SessionEnvelope` 事件。`internal/app/discussionsession/runtime_canonical_progress.go` 使用同一事件流投影实时消息和历史消息。运行时负责发送、取消、追加输入、工具调用、权限请求和最终结果；provider 负责协议适配。

讨论业务继续负责租户、群组成员、可见性、回复线程、收件箱、未读、幂等和事务。运行时内容块不是目标授权凭据。`internal/mission/discussion/mention_links.go` 按 Orrery 既有链接格式扫描五类目标：`agent`、`role`、`user`、`team`、`all`；保持 Markdown 链接、括号标签、裸链接、去重和未知类型警告兼容。服务端校验权限并在事务提交后触发 Agent。

`cmd/desktop-core` 是独立的本地宿主。Web 复用共享核心接口，继续使用服务端持久化和授权，不依赖桌面进程或其本地 SQLite 数据库。

## 文件存储

`internal/platform/filestore.Store` 保留调用契约，业务侧继续保存 `ArtifactRef`。Local 默认启用，S3 按配置选择；没有新增第三方存储服务依赖。

- Local 使用 `os.Root` 限制读取、写入、删除、递归删除及 HTTP 下载的目录边界。拒绝绝对路径、相对穿越、元数据侧车作为对象和非普通文件。
- 数据继续保存在原对象 key 下，侧车仍为 `<key>.meta.json`，字段为 `filename`、`content_type`。既有文件可直接读取，不需要迁移历史数据。
- 上传先暂存数据与元数据，再在写锁内发布。失败时恢复原对象和元数据；同一个 Store 的读者等待发布完成。
- 下载通过 `http.ServeContent` 支持 HEAD/Range，使用标准 MIME 格式化文件名，元数据读取也受目录边界约束。
- `/uploads/`、`/artifacts/` 和 `artifact://` 的既有引用保持兼容；这些 URL 不暴露本地绝对路径。
- S3 直接使用官方 AWS Go SDK，保留 bucket、region、CDN、自定义 endpoint、路径式 URL、分页删除和错误传播。分页不前进时失败，列表返回范围外的 key 时不删除该对象。

`GetFilePath` 仅为兼容定位接口；存储读写不信任该路径并重新验证 key。存储根目录本身应由部署者控制，不应被不可信本地进程替换。

## 工程约束

旧的源码复制计划、对应参考材料和生成副本已退役；私有 Git 历史和审查记录保留来源证据。引入其他依赖仍需保留其实际许可证和声明。此次替换不表示 PageIndex 独立服务、资源图片或其他尚未闭环的开源准备事项已经完成。
