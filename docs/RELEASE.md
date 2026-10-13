# 发布准备与仓库管理

项目名称：广月面板 / Guangyue Panel。仓库 slug：`guangyue-panel`。当前发布版本：`0.41.0`。项目定位：新一代跨境电商企业级解决方案，提供 Lite 独立/子站版和 Pro 主站管理版，支持 Docker 与原生 systemd 部署。

## 公开发布策略

仓库在完成实现、数据库与部署测试、秘密扫描后公开发布。版本 tag 必须指向通过完整 CI 的精确主线提交。发布工作流读取该提交同一次成功 CI 的原生与 Docker 产物；先上传草稿，重新下载全部八项资产并核验 SHA256 后正式发布。工作流不修改仓库 visibility。

原生产工作区和历史部署档案不进入此仓库。源码使用独立整理的历史；真实凭证、订阅、数据库、服务器专属资料、个人路径和私钥禁止提交。示例全部使用合成数据。

## 发布产物

正式 Release 必须包含以下八项非空资产，名称中的 `<version>` 为不带 `v` 的版本号：

1. `guangyue-panel-lite-<version>-linux-amd64.tar.gz`：Lite 完整运行包，默认 SQLite 独立站，也可通过配对令牌作为托管子站。
2. `guangyue-panel-pro-<version>-linux-amd64.tar.gz`：Pro 完整运行包，默认主站，使用专用 PostgreSQL/Redis。
3. `guangyue-installer-<version>.tar.gz`：版本锁定的一键安装器，包含 Docker 与原生部署入口。
4. `install.sh`：README 使用的启动脚本；选择完整正式 Release，并校验安装器和运行组件。
5. `guangyue-panel-docker-<version>-linux-amd64.tar.gz`：可直接加载的应用、PostgreSQL 和 Redis 镜像归档；安装无需镜像仓库账号。
6. `DOCKER_IMAGES.json`：记录精确版本、平台、归档 SHA256、不可变镜像 ID，以及数据库镜像的官方 digest 来源。
7. `SHA256SUMS`：统一校验以上运行包、安装器、启动脚本、镜像归档、元数据及 SBOM；完整运行包内还有逐文件校验清单。
8. `SBOM.spdx.json`：固定 Go/npm 依赖清单；Docker 组件来源、版本和许可证另随镜像内的 `DOCKER_COMPONENTS.json` 及许可文件提供，不声称该 SBOM 覆盖镜像全部操作系统包。

Lite/Pro 两包均内置面板、前端、独立 443 网关、自定义 HY2、Xray 和 Mihomo，以及安装/升级脚本、文档和受校验的 `EDITION` 标记。安装直接使用随包核心，不依赖现场从上游下载代理核心。

仓库 tag 对应源码，GitHub 自动提供的 source archive 不计入上述八项资产。运行包保留 Hysteria/Xray 固定上游 commit、修改补丁、Xray 修改源码及 Mihomo 对应源码与许可证；Docker 镜像也保留随包源码与组件许可材料。

本版本产物的发布来源由仓库/tag和构建流程追踪；尚未配置独立离线签名密钥。不要声称 SHA256 提供发布者身份认证。

## 构建和草稿 Release

### 主分支自动发布

`.github/workflows/auto-release.yml` 在完整 CI 成功后继续发布，兼容受保护的 `main`，不绕过分支保护：

1. 普通功能合并提交通过完整 CI 后，递增版本并创建版本 PR。已直接准备版本的合并提交须使用 `release: v<version> [skip release]` 标题前缀，进入发布阶段而不再次递增；`0.41.0` 使用此路径。
2. 显式调度版本分支 CI，检查前端、Go、自定义核心及全部安装矩阵；成功后通过正常 PR 合并。
3. 显式调度合并提交的完整 CI；只有其精确 SHA 仍为当前 `main` 且 CI 整体成功后才打标签并调度 `Publish version`。完整 CI 包括构建、漏洞与秘密扫描、原有安装矩阵、原生一键安装和 Docker Lite/Pro 安装验收。
4. 版本合并提交带有 `[skip release]`，不重复递增；已发布版本保持不可变。落后于当前 main 的任务跳过，避免覆盖较新的提交。

Actions 设置需要允许 GitHub Actions 创建 PR。工作流使用 `contents`、`pull-requests`、`actions` 写权限完成上述操作，无需保存个人访问令牌。因为默认 `GITHUB_TOKEN` 的写入不触发下一轮 push/PR 工作流，所以每个 CI 和发布转换都显式调用 `workflow_dispatch`。额外人工审批规则仍然生效，无法合并时工作流明确失败并保留版本 PR。

1. 手动发布仍可更新 VERSION、后端常量、前端 package/lock、文档与 CHANGELOG。
2. 运行 `scripts/check.sh`、HY2/Xray 补丁测试、许可证收集与完整运行包打包；执行 `gitleaks dir` 及提交历史扫描。Docker 构建使用校验后的同版本 Pro 包，并生成镜像归档和元数据。
3. 运行完整部署集成 CI，检查安装、既有 Nginx 保留、协议连接、证书轮换、升级、回退与中断恢复。服务器系统的实际验证范围以该版本 CI 记录为准。
4. 等待精确主线提交的完整 CI 成功，再将版本 tag 指向此 SHA，触发 `Publish version`；也可手动指定已存在且通过相同检查的 tag。
5. 发布工作流从同一次成功 CI 下载 `linux-amd64` 和 `docker-linux-amd64` 两个 artifact，由 `scripts/merge-release-assets.py` 验证五项原生产物与两项 Docker 产物，生成第八项统一清单。
6. 核对草稿资产名称、SHA256 与实际下载；`scripts/publish-release.sh` 重新下载恰好八项资产、比对统一清单并逐项校验，全部通过后才转正式并标记 latest。私有阶段 Release 内容继续仅授权账号可见。

工作流不会更改仓库公开状态。`Publish version` 要求 tag 的精确提交已有成功的完整 CI，且该次 CI 的两个 artifact 仍可下载；当前保留期为七天。已过期时重新运行同一提交的完整 CI，不混用其他提交的产物。发布完成后应确认八项资产齐全、校验通过，且 README 的 `releases/latest/download/install.sh` 能匿名下载。

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
