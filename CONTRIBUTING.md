# 参与开发 / Contributing

广月面板面向自部署的代理服务与网络资源管理，覆盖套餐权益、IP 出口、独立子站及运营工具。贡献应解决明确的用户问题，并保留站点管理员对本地资源的控制。请先阅读 [README](README.md)、[架构](docs/ARCHITECTURE.md)和[安全政策](SECURITY.md)。

报告问题请提供版本、Lite/Pro、Docker/原生、站点角色，以及使用合成数据的复现步骤。不要上传真实配置、订阅、钱包恢复材料、服务器或客户资料。安全漏洞走私密报告渠道。

## 本地开发

需要 Go 1.26.9、Node 24 LTS、npm、Python 3、Git、curl、unzip 和 OpenSSL 1.1.1+（推荐 3.x）。前端依赖由锁文件固定，使用 `npm ci`。Go 不在 PATH 时设置 `GY_GO=/path/to/go`。

```bash
python3 scripts/dev.py
# 另一个终端：
cd frontend
npm ci --ignore-scripts
npm run dev
```

开发器只监听回环，在 `.cache/dev` 创建独立配置、数据库和随机初始凭证。开发模式模拟核心同步，不启动真实代理，因此不能证明节点可用。按输出的本地地址访问，停止时使用 Ctrl+C；需要重置时，先停止再删除 `.cache/dev`。不要向公网开放开发模式。

构建中间文件位于 `.cache/` 和 `build/`，不得提交。访问上游需要代理时，使用自己配置的标准 `HTTPS_PROXY`，不要将代理凭据写入仓库。

## 目录与职责

| 目录 | 内容 |
| --- | --- |
| `backend/` | Go API、业务规则、计量、核心与群站控制，以及行为测试 |
| `frontend/` | Vue 3 界面、中文/English 文案和前端单元测试 |
| `deploy/` | Docker/原生安装、升级、证书、维护恢复和部署测试 |
| `core-patches/` | 固定代理核心的补丁、源码说明及上游许可 |
| `scripts/` | 构建、依赖核验、秘密/版本/链接检查和打包 |
| `docs/` | 用户任务、安装、配置、运维、架构与验收记录 |

## 按变更范围验证

| 变更 | 必要验证 |
| --- | --- |
| 纯文档、GitHub 文案 | 核对描述与实现、版本和相对链接；运行 `python3 scripts/release-check.py`；检查引用来源、归属和敏感信息 |
| 前端行为或样式 | 运行前端测试和 `npm run build`，覆盖语言、权限、亮/暗主题和手机布局；复杂交互另做真实浏览器验证 |
| 后端业务 | 运行相关回归及 `go test -race ./...`、`go vet ./...`；覆盖权限、重复请求、失败和恢复 |
| Pro 持久化或队列 | 设置 `GY_TEST_POSTGRES_DSN`、`GY_TEST_REDIS_URL`，运行相关集成测试；只使用可丢弃的专用数据库和账号 |
| 安装、升级、证书或维护流程 | 运行部署行为测试及隔离 Linux 安装/失败恢复/回退验证，检查其他服务是否受到影响 |
| 代理协议、挂载或授权 | 用真实客户端验证 TCP/UDP、计量、用户隔离、出口与撤销；HTTP 健康响应不能代替验收 |
| 钱包、收款、退款 | 使用隔离环境和无价值测试数据，覆盖事件去重、权益队列、费用上限、重试及不确定状态；测试通过不等于真实主网资金验收 |

完整项目检查：

```bash
bash scripts/check.sh
```

构建与原生包准备：

```bash
bash scripts/build.sh
bash scripts/build-hy2-core.sh --test
bash scripts/build-xray-core.sh --test
python3 scripts/third-party.py
python3 scripts/package.py
```

Hysteria/Xray 补丁变更必须运行相应真实协议集成测试。PostgreSQL 测试会创建和删除测试 schema，不能使用生产数据库权限。macOS 自带 LibreSSL 不支持部分证书检查，部署测试应使用 OpenSSL 3 或 Linux/CI，不得通过忽略证书校验获得通过结果。

新增依赖需记录来源与许可证，更新锁文件、第三方 notices 和相关 SBOM。所有示例使用合成账号、保留示例网段或受控网络响应，不使用生产订阅。不要将未验证的第三方检测结果写成产品保证。

## 提交与 PR

保持改动集中，先说明用户遇到的具体问题和改动后的行为，再给出相关测试与限制。涉及套餐、账本、数据库、核心重启或安装路径时，说明数据影响、恢复方式和兼容边界。文案修改应同步中文与 English 入口，避免把开发计划、API 配置预留或测试模式写成可直接使用的功能。

安全修复遵循 [SECURITY.md](SECURITY.md)。提交前检查暂存差异和秘密扫描，不提交真实凭据、配置、备份或未检查的日志。不要清理与自己的修改无关的工作区文件。

## 版本与发布

准备版本时同步 `VERSION`、Go 版本常量、前端 package/lock 版本和 CHANGELOG。README 的发布徽章从 GitHub 读取最新正式版，避免手写版本不同步；文档中的固定版本命令应准确说明适用版本。

正式版须来自完整 CI 成功的精确主线提交。发布流程先建立草稿，上传并重新下载校验全部 **八项资产** 后转为正式版：Lite 包、Pro 包、安装器、`install.sh`、Docker 镜像归档、`DOCKER_IMAGES.json`、`SHA256SUMS` 和 `SBOM.spdx.json`。完整流程、自动递增规则及对应源码分发见[发布准备](docs/RELEASE.md)。不要将仅有 tag、草稿或不完整资产的版本称为已发布。

提交代码表示你有权以项目 LGPL-3.0-only 许可证贡献该代码。第三方代码需保留原始许可、归属及分发义务；项目原有 MIT 归属也应保留。

## English summary

Include a concrete user problem, the resulting behavior, relevant tests and limitations in each PR. Use synthetic data, keep changes focused and follow the verification table above. Documentation-only changes need accurate implementation references, valid links and secret checks; runtime, deployment and financial changes require their corresponding behavior and integration tests. Use disposable PostgreSQL/Redis services. Report vulnerabilities privately, and never contribute live configurations or wallet material.
