# 来源与第三方许可证

| 组件 | 固定版本/来源 | 许可 | 分发方式 |
| --- | --- | --- | --- |
| 广月面板 | 本仓库 | LGPL-3.0-only | 源码与应用二进制，保留 fake-ui 上游版权 |
| Xray-core | [v26.3.27](https://github.com/XTLS/Xray-core/tree/v26.3.27) + guangyue-sessions1 | MPL-2.0 | 包内提供会话计数扩展版、完整修改源码、补丁与原始许可；见 core-patches |
| Hysteria | app/v2.9.2 / `c3a806b5cbbb20fe72099529573da26b1a2e9f22` | MIT | 应用包含节点路由补丁版；补丁和构建说明在 core-patches |
| Mihomo | [v1.19.30](https://github.com/MetaCubeX/mihomo/tree/v1.19.30) | GPL-3.0 | 获取脚本直接从上游下载，未捆绑在本项目应用包 |
| Vue | 以 package-lock.json 为准 | MIT | 编译进入前端 |
| Lucide | 以 package-lock.json 为准 | ISC | 编译进入前端 |
| QRCode | 以 package-lock.json 为准 | MIT | 编译进入前端 |
| Trust Wallet Core | [4.8.4 / d40d24a6](https://github.com/trustwallet/wallet-core/tree/d40d24a63d92619167903369308bf0e2f7eb3a59)，官方 `@trustwallet/wallet-core@4.8.4` | 上游 Core Apache-2.0；npm 元数据声明 MIT；其内第三方组件各自许可 | 官方未修改 WASM 与 JavaScript glue 作为浏览器资产；原始许可及上游第三方声明随包提供，不需要生产 Node 服务 |
| go-ethereum | go.mod/go.sum 锁定的 v1.17.7 | 链接的库代码 LGPL-3.0-or-later；原始其他声明保留 | 交易编码、签名与哈希链接进入应用；固定完整库源码、原始 COPYING/COPYING.LESSER 和重链接说明随安装包提供，见 [构建来源](docs/CRYPTO_BUILD.md) |
| Go / SQLite 生态 | 以 go.mod/go.sum 为准 | 各自许可 | 编译进入应用 |
| DB-IP IP to Country Lite | [2026-10](https://db-ip.com/db/download/ip-to-country-lite) | CC-BY-4.0 | MMDB 原始记录嵌入应用；[来源与署名](docs/SITE-ACCESS.md#数据来源与许可)，完整许可随安装包提供 |

`python3 scripts/third-party.py` 从已锁定和下载的 Go/npm 依赖及固定 DB-IP 数据集收集许可证文本，生成 `build/notices/` 及 SPDX JSON 依赖清单，随应用包提供。无法自动判断的 SPDX license 字段为 `NOASSERTION`，许可证原文单独保留，不声称自动扫描是法律审核。

Trust Wallet Core 的 npm 包不含许可证文件，且 npm 的 MIT 声明不能覆盖所包含 Core 的 Apache-2.0 及其他第三方许可。`licenses/trust-wallet-core-source.json` 固定上游 commit、npm integrity、WASM/glue 和原始许可 SHA-256；打包会校验这些来源并保留 `trust-wallet-core-4.8.4-LICENSE.txt`、`trust-wallet-core-4.8.4-LICENSE-3RD-PARTY.txt`。SBOM 分别记录 npm 包与上游 Core，并建立包含关系；完整上游源码和构建资料见固定 commit。

[IP Geolocation by DB-IP](https://db-ip.com/)：国家判断使用 DB-IP 的 CC-BY-4.0 数据，不修改上游记录。该数据集保留自身许可，面板 LGPL 许可不替代它；来源、完整许可及「按现状提供」条款见对应署名和许可证文件。

面板 LGPL-3.0-only 许可不替代第三方组件。如果你自行制作包含 Mihomo 的镜像、二进制集合或修改版本，需要自行满足 GPL 对相应源码、构建材料和许可的要求，不能把整个集合标成单一面板许可。Xray 的分发和修改同样遵循 MPL。官方源码地址、tag、校验和与构建脚本为可审计来源。

品牌 banner/logo 为本项目原创 SVG，适用项目 LGPL-3.0-only。国家标记使用 Unicode regional indicator 字符，由设备字体渲染，不再分发来源不明的旗帜位图。

本项目架构和文档布局参考 Sub2API（评估 commit `98d86915becae9fe9491a91ffc6defd5235c8d2b`），应用实现未复制其业务源码、品牌、截图或文案；通用 LGPL 许可文本保持原文。原项目 [fake-ui](https://github.com/wudi000888-svg/fake-ui) 的 MIT 版权见 `licenses/guangyue-legacy-MIT.txt`。

Vue Router、Pinia、pgx 与 go-redis 的锁定版本及原始许可文本随 SBOM/DEPENDENCY-LICENSES 分发。项目 LGPL 的附加条款须和 `licenses/GPL-3.0.txt` 一起阅读。旧版本已按 MIT 授出的权利不撤回；第三方原有许可继续适用于其各自部分。
