# 发布准备与仓库管理

项目名称：广月面板 / Guangyue Panel。仓库 slug：`guangyue-panel`。双版本发布：`0.22.0`。项目定位：新一代跨境电商企业级解决方案，提供 Lite 单机版和 Pro 群站管理版。

## 公开发布策略

仓库在完成实现、数据库与部署测试、秘密扫描后公开发布。版本 tag 发布工作流复用同一提交完整 CI（含安装测试）通过的产物；先上传草稿，重新下载全部资产并核验 SHA256 后正式发布。工作流不修改仓库 visibility。

原生产工作区和历史部署档案不进入此仓库。源码使用独立整理的历史；真实凭证、订阅、数据库、服务器专属资料、个人路径和私钥禁止提交。示例全部使用合成数据。

## 发布产物

- `guangyue-panel-lite-<version>-linux-amd64.tar.gz`：默认单机 SQLite 安装。
- `guangyue-panel-pro-<version>-linux-amd64.tar.gz`：默认主控安装，支持 `--role business` 安装轻量业务站。
- 两包均包含面板、前端、自定义 HY2 与 Xray、安装/升级脚本、文档及受校验的 `EDITION` 标记。
- `SHA256SUMS`：外层压缩包与 SBOM 校验；压缩包内还有逐文件校验。
- `SBOM.spdx.json`：固定 Go/npm 依赖清单。
- 仓库 tag 对应源码；GitHub 自动提供 source archive，另保留 Hysteria/Xray 原始 commit、完整修改补丁和 Xray 修改源码包。
- Xray 会话计数扩展和源码随包提供；Mihomo 由脚本从上游下载并验证。

本版本产物的发布来源由仓库/tag和构建流程追踪；尚未配置独立离线签名密钥。不要声称 SHA256 提供发布者身份认证。

## 构建和草稿 Release

### 主分支自动发布

`.github/workflows/auto-release.yml` 在每次提交进入 `main` 后自动执行：

1. 等待源提交完整 CI（含安装、业务站与升级测试）成功，再将 `VERSION` 的补丁号递增，并同步 Go、Vue、README 和变更记录。
2. 运行完整检查、前端生产构建、Go 构建及 HY2/Xray 核心测试构建。
3. 生成 Lite/Pro Linux amd64 安装包、`SHA256SUMS` 与 `SBOM.spdx.json`。
4. 提交版本更新、创建对应 `vX.Y.Z` tag，上传并校验完整资产后正式发布 GitHub Release。

版本提交带有 `[skip ci] [skip release]`，不会再次触发发布循环。任一检查或构建失败时不会创建 tag 或 Release。功能分支提交只运行 CI，合并到 `main` 后才生成版本和安装包。

1. 手动发布仍可更新 VERSION、后端常量、前端 package/lock、文档与 CHANGELOG。
2. 运行 `scripts/check.sh`、HY2 补丁测试、许可证收集与打包；执行 `gitleaks dir` 及提交历史扫描。
3. 运行 Ubuntu 部署集成 CI，检查安装、Nginx/服务、证书、升级与回滚结果；网络协议回归需单独验证真实客户端。
4. 提交并等待 CI 成功，再推送版本 tag 自动触发 `Publish version`，也可手动指定现有 tag。
5. 核对草稿 asset 名称、SHA256 与实际下载；私有阶段 Release 内容继续仅授权账号可见。

工作流不会更改仓库公开状态。`Publish version` 要求同一提交已有成功的完整 CI，再读取其四项资产并发布。

## 转公开时的最终检查

在 GitHub Settings → General → Danger Zone 执行 visibility 变更之前，维护者应确认：

- 源码和完整提交历史已通过秘密扫描，无生产样例、账号、订阅和私钥。
- 安装/使用说明、LGPL-3.0 项目许可和 MIT 上游归属、第三方许可、范围与限制准确。
- 当前默认分支 CI 为成功状态，版本 tag 与资产一致。
- README、issue模板、安全报告入口可以使用；对账号套餐不支持的分支保护/秘密扫描功能作明确记录。
- 开启可用的依赖告警、私密漏洞报告和主分支保护；不把不支持的设置写成已开启。
- 公开项目名称、定位、协议和第三方分发方式已确认，草稿 Release 是否正式发布由维护者决定。

**从 Private 改成 Public 会公开整个仓库内容和历史。此动作不会由安装器、CI 或发布脚本自动执行。** 转公开后可视需要设置社交预览图、项目主页和公开演示；演示只能使用合成数据。

## 验收记录

当前版本的实际验证证据见 [验收记录](VALIDATION.md)。本文件是长期操作流程，不把某次通过结果延伸为未来版本保证。
