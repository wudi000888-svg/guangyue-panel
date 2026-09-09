# 来源与第三方许可证

| 组件 | 固定版本/来源 | 许可 | 分发方式 |
| --- | --- | --- | --- |
| 广月面板 | 本仓库 | LGPL-3.0-only | 源码与应用二进制，保留 fake-ui 上游版权 |
| Xray-core | [v26.3.27](https://github.com/XTLS/Xray-core/tree/v26.3.27) | MPL-2.0 | 获取脚本直接从上游下载，未捆绑在本项目应用包 |
| Hysteria | app/v2.9.2 / `c3a806b5cbbb20fe72099529573da26b1a2e9f22` | MIT | 应用包含节点路由补丁版；补丁和构建说明在 core-patches |
| Mihomo | [v1.19.30](https://github.com/MetaCubeX/mihomo/tree/v1.19.30) | GPL-3.0 | 获取脚本直接从上游下载，未捆绑在本项目应用包 |
| Vue | 以 package-lock.json 为准 | MIT | 编译进入前端 |
| Lucide | 以 package-lock.json 为准 | ISC | 编译进入前端 |
| QRCode | 以 package-lock.json 为准 | MIT | 编译进入前端 |
| Go / SQLite 生态 | 以 go.mod/go.sum 为准 | 各自许可 | 编译进入应用 |

`python3 scripts/third-party.py` 从已锁定和下载的 Go/npm 依赖收集许可证文本，生成 `build/notices/` 及 SPDX JSON 依赖清单，随应用包提供。无法自动判断的 SPDX license 字段为 `NOASSERTION`，许可证原文单独保留，不声称自动扫描是法律审核。

面板 LGPL-3.0-only 许可不替代第三方组件。如果你自行制作包含 Mihomo 的镜像、二进制集合或修改版本，需要自行满足 GPL 对相应源码、构建材料和许可的要求，不能把整个集合标成单一面板许可。Xray 的分发和修改同样遵循 MPL。官方源码地址、tag、校验和与构建脚本为可审计来源。

品牌 banner/logo 为本项目原创 SVG，适用项目 LGPL-3.0-only。国家标记使用 Unicode regional indicator 字符，由设备字体渲染，不再分发来源不明的旗帜位图。

本项目架构和文档布局参考 Sub2API（评估 commit `98d86915becae9fe9491a91ffc6defd5235c8d2b`），应用实现未复制其业务源码、品牌、截图或文案；通用 LGPL 许可文本保持原文。原项目 [fake-ui](https://github.com/wudi000888-svg/fake-ui) 的 MIT 版权见 `licenses/guangyue-legacy-MIT.txt`。

Vue Router、Pinia、pgx 与 go-redis 的锁定版本及原始许可文本随 SBOM/DEPENDENCY-LICENSES 分发。项目 LGPL 的附加条款须和 `licenses/GPL-3.0.txt` 一起阅读。旧版本已按 MIT 授出的权利不撤回；第三方原有许可继续适用于其各自部分。
