# Orrery JavaScript utilities

四个独立实现的兼容包，版权为 Minvia，采用 Apache-2.0，每包附完整 LICENSE 和 NOTICE。
实现依据公开 API、调用方需求及黑盒行为比较编写，未复制四个上游包的实现或 V8 的旧格式化文件。

| 原导入名（兼容别名） | 本地实现 | 使用位置 |
| --- | --- | --- |
| `is-reference` | `@orrery/ast-reference` | Web 的 Svelte、Web/桌面的 Rollup 作用域分析 |
| `locate-character` | `@orrery/source-location` | Svelte 和 Rollup 的 UTF-16 源位置定位 |
| `stackback` | `@orrery/error-callsite` | Web/桌面 Vitest 的挂起资源诊断 |
| `proxy-agent-negotiate` | `@orrery/proxy-negotiate` | PI 代理 Agent 的可选 Negotiate 回调 |

版本均为 `1.0.0`。npm 使用根级直接 file 依赖、`$` override 和 `install-links=true`；
Bun 使用既有 resolutions，保留所有安全版本约束。包名仍作为兼容别名出现，包内容与身份为 Orrery 实现。
不需要 npm 发布或外部 fork。安装所需目录位于同一仓库；复制单独 Web/PI 目录时必须一并保留本目录。

## 安装与升级

Web 与 PI 使用 `npm ci`，桌面使用固定 Bun 1.3.14。安装脚本还处理 registry 包的嵌入副本：

- Svelte 5.57.3 的 CommonJS 编译器入口改为同步加载同包 ESM 源码，从而与 ESM 入口共享自研依赖。
  要求 Node >=22.19.0；验证工具链为 Node 22.23.0。
- Rollup 4.61.0（Web）和 4.59.0（桌面）的四个 CJS/ESM 文件只替换选定的两类上游函数。
  magic-string 自己的一基行号定位器保留。替换前后均固定完整文件哈希，升级未知版本会失败。
  受影响的旧内部 sourcemap 被移除，避免留下旧实现或错误映射；应用自身输出 sourcemap 保留。
- 安装包保留 Rollup 原声明，并增加 `ORRERY-UTILITIES-LICENSE`、`ORRERY-UTILITIES-NOTICE`。
  写入通过替换文件断开 Bun cache 硬链接，不修改其他工作树或下载缓存。

不能通过 `--ignore-scripts` 后直接交付制品。若安装时明确跳过脚本，必须显式执行下面的安装步骤。
`--check` 只验证，不修补；Web 的构建/类型检查/测试和桌面的验证入口会核查替换结果。
两个 Dockerfile 在安装前复制共享源码，API 的 `--ignore-scripts` 安装后显式验证本地实现。
`dev.sh`/`dev.ps1` 会检查已有 PI 安装，发现仍为旧实现时按锁图重新安装。

```powershell
node orrery_v2/runtime/orrery-js/install.mjs web
node orrery_v2/runtime/orrery-js/install.mjs pi
node orrery_v2/runtime/orrery-js/install.mjs desktop
node orrery_v2/runtime/orrery-js/install.mjs web --check
node --test orrery_v2/runtime/orrery-js/test/*.test.mjs
```

更新实现后，先对锁图对应、经过 SRI 校验的 Svelte/Rollup 原包执行 `prepare-bundles.mjs <解压目录...>`，
再运行 `python scripts/orrery-js-sources.py` 刷新自研源码登记，重新安装、验证并重建许可清单和 SBOM。
不要把未知 file 依赖当成自研包跳过：离线校验只接受登记目录、确切包身份、完整文件集和源码/许可哈希。
许可附件的 `sourceInputs` 同时绑定本地包、源码登记及接线文件；手工激活会分别核对 API 与独立 Web
源码快照，不能仅凭相同 lock 文件继承不同实现的附件。独立 Web 源码归档需包含
`orrery_v2/runtime/orrery-js/` 和 `compliance/orrery-js-sources.json`，以及原有 Web 源码。

## 保留行为与验证边界

保留 Svelte 的 ESM/CJS 编译、源位置和应用 sourcemap、Vitest 的真实 CallSite 诊断，以及普通/Basic/可选
Negotiate 代理能力。Kerberos 仍由可选原生 `kerberos@^2` peer 实现；本包只做挑战与令牌的适配。
保留现有 `HTTP@proxy` 服务名和上层 Agent 的重试边界，不声称实现了完整 Kerberos 协议或多轮任意握手。
当前 PI 调用方未传 `negotiate: true`；安装本包不表示已启用企业认证。

回归覆盖原生库替身、本地代理的 407→认证→隧道、认证失败、空令牌、缺 peer、普通与 Basic 代理。
真实企业 KDC、票据和 TLS 代理环境另行验证。此次不部署、不发布，不改变仓库私有状态。
涉及 ORR-UI-01、ORR-RUNTIME-01、ORR-TRACE-01 和 ORR-DEPLOY-01；用户身份、权限、QA、Agent 正文和投递契约保持。
