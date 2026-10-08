# 加密钱包与签名构建来源

钱包创建使用固定版本的官方 Trust Wallet Core 浏览器 WASM。来源、原始文件哈希和许可证见 `licenses/trust-wallet-core-source.json`；安装包内同源提供官方原始 JS/WASM，不需要运行独立 Node 服务。后台的 BIP39/BIP32 校验和派生使用 go.mod 锁定的开源库。

EVM 交易编码、EIP-155 签名和交易哈希使用固定的 `github.com/ethereum/go-ethereum` 库，版本以该发行版的 go.mod/go.sum 为准。应用不运行 geth 节点进程。库源码按上游原文分发，其 LGPL-3.0-or-later 与包含的其他来源许可继续适用。

Lite、Pro 安装包的 `licenses/go-ethereum-source.tar.gz` 提供该构建所用的完整上游模块源码，`GO-ETHEREUM-SOURCE.json` 记录版本、Go 模块校验和、上游地址和归档 SHA-256。`DEPENDENCY-LICENSES.txt` 包含上游 COPYING/COPYING.LESSER 与其他已锁定依赖的原始声明，SPDX 清单记录实际版本。

面板对应应用源码、前端锁文件和构建脚本可从安装包 VERSION 对应的 GitHub tag 取得：

```text
https://github.com/wudi000888-svg/guangyue-panel/tree/v<VERSION>
```

按 README 的开发构建要求准备该版本的 Go、Node 和 Python。在源码根目录运行：

```sh
bash scripts/build.sh
```

替换或修改链接库时，先将包内的 `licenses/go-ethereum-source.tar.gz` 解压到源码目录之外，然后在面板 backend 目录指定本地替换。例如归档版本为 v1.17.7 时：

```sh
go mod edit -replace github.com/ethereum/go-ethereum=../../go-ethereum-v1.17.7
go mod tidy
```

在替换库中完成接口兼容修改后，回到面板源码根目录重新运行构建脚本。`build/bin/guangyue-linux-amd64` 是重链接后的应用。按安装与升级说明替换自己的应用并保留配置、数据库和 master.key；不需要发行方签名才能运行自己重建的版本。

官方构建禁止在固定版本标签中悄悄替换依赖；生成 SBOM、来源和安装包校验和的步骤见 `scripts/third-party.py`、`scripts/package.py`。自行修改后的构建应重新生成其真实来源清单，不能沿用官方二进制的校验和。
