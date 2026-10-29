# 四项许可原文缺口的上游复核

> 历史补证记录：下文描述替换前的原包，不代表当前依赖清单。四项实现已通过[自研替换同步](opensource-self-developed-sync-20261026.md)移出当前 Web/API 依赖图；原发布者缺失的许可原文并未因此补齐。

复核日期：2026-10-26。源码基点为 `a6da83e4c5645a1aa5728c213900d1fe24f2c44d`，
对应个人私有仓库 Web/API 准备分支。本次不改变依赖版本、运行行为或发布状态。

## 结果

四项完整包级 MIT 原文缺口仍然存在，本次关闭数量为 **0**。
新增的是发布包、源码身份及上游处理状态的证据，不是新的许可授权。
`compliance/license-texts.json` 和补充登记继续保留 `originalLicenseFileSupplied: false`、
参考文本标记和 `redistributionApproved: false`。

机器可读记录见 [上游复核清单](../compliance/license-upstream-review-20261026.json)。
现有原始声明、SPDX 参考文本和 `stackback` 内嵌 V8 BSD 声明均保留。

## 实际核对范围

- 从 npm 官方 registry 读取四个包的全部版本记录，下载并核对全部 **26** 个发布包：
  `is-reference` 14 个、`locate-character` 8 个、`proxy-agent-negotiate` 2 个、`stackback` 2 个。
  有 SRI 的按 SRI 校验；历史记录仅有 `dist.shasum` 的按 SHA-1 校验，逐包另记 SHA-256。
  检查所有归档成员的许可类文件名及 MIT 授权正文特征，没有找到可关闭缺口的完整包级原文。
- 核对 `is-reference` 49 个、`locate-character` 19 个、`node-stackback` 10 个可达历史提交，
  搜索许可文件和授权正文变化。没有找到此前遗漏的完整 MIT 原文。
- 核对 `proxy-agents` 当前完整 tree、`packages/negotiate` 的两个路径历史提交，
  以及 npm 发布 provenance 指向的固定提交。仓库其他包的 LICENSE 不作为本包的授权证明。
- 检查官方 Rollup 发布包中这两个 Rich Harris 依赖的条目；它们也只有 MIT 名称与作者记录，
  相邻组件的完整许可不能移作这两个包的原文。

## 逐项证据与阻塞

| 包 | 本次增加的核实 | 上游状态 |
| --- | --- | --- |
| `is-reference@3.0.3` | npm `gitHead` 为 `8bb053129bfabe2f6a7d7ed050159d67ebe82829`；发布包源码、package.json、README 三个 Git blob 相符。 | [issue #12](https://github.com/Rich-Harris/is-reference/issues/12) 尚无维护者提供原文；[PR #6](https://github.com/Rich-Harris/is-reference/pull/6) 已关闭且未合并，不能把第三方提交的版权文字认定为作者确认。 |
| `locate-character@3.0.0` | 再次确认 GitHub 中的 npm `gitHead` `4f08a59ec248121f7002abd02ee7b94e8eda06bc`，四个发布包源文件 blob 一致。 | [issue #1](https://github.com/Rich-Harris/locate-character/issues/1) 尚无维护者提供原文；旧 GitLab 地址不作为源码不存在的证据。 |
| `proxy-agent-negotiate@1.1.0` | 官方 registry provenance 的包 SHA-512 与锁定归档一致，指向 `b7e5f7ccce1a3ac5b339cc4c587974e8989cbc16`；发布前后 package.json 的差别仅为三个开发依赖的 workspace/catalog 版本展开。 | [issue #479](https://github.com/TooTallNate/proxy-agents/issues/479) 已有版本特定补证请求，尚无维护者确认；相邻包许可证不自动适用。 |
| `stackback@0.0.2` | 找到提交 `1fe7f988c5bac51e942d6c55230895a55fab4504`，六个发布包成员与源码 blob 一致，包括入口和 V8 声明。 | [issue #1](https://github.com/defunctzombie/node-stackback/issues/1) 已关闭；维护者说明不再维护。已有 V8 BSD 原文仍不能代替包级 MIT 原文。 |

`proxy-agent-negotiate` 的 provenance 核查使用官方 HTTPS 响应、解码后的 SLSA payload
和包摘要比对；本次没有独立验证 Sigstore 签名、证书链或透明日志，不能把这项记录描述为完整签名验证。
这些来源核对也不等于许可适用性的法律认定。

## 补齐所需的外部材料

每项需要维护者提供适用于锁定版本的完整许可与准确版权声明，或明确指定适用的既有原文。
取得材料后，固定来源提交或维护者答复、保留原字节、加入补充登记及组件清单，并重新运行
许可、锁图、资源、源码导出和附件校验，再关闭相应缺口。

待发送的四份请求见 [补证请求草稿](license-upstream-requests-20261026.md)。
本轮只准备草稿，没有新建外部 issue、评论或联系维护者。已有公开请求由其他账号提交，
其存在不构成本地用户对任何外发操作的授权。

如果维护者无法提供材料，下一条路径是逐项评估保持功能的依赖替换。
这需要单独核实 Svelte 编译行为、测试诊断和 Negotiate/Kerberos 代理认证兼容性；
本轮没有通过移除测试、停用代理、修改许可类型或重写版权声明来消除缺口。

## 验证边界

业务契约和运行源码保持不变。本轮验证许可清单、补充登记、源代码清单与归档；
没有重新声明先前的 Web/Go 全量测试结果，也没有进行生产部署。
GitNexus 没有本分支对应索引，使用锁定 Git tree、原始归档和现有校验器核验。

本次实际运行：许可附件测试 28 项、源码导出测试 13 项，共 41 项通过；591 组件许可清单、
两套源码依赖清单检查通过。附件同时携带复核清单和两份跟进文档，逐文件哈希复核通过，
离线打开跟进链接所需的材料完整保留。上述检查仍明确报告四项未解决缺口。

仓库保持私有，Actions 保持关闭；此记录不批准公开、合并或发布。
