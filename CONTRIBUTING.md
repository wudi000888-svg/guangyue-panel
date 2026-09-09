# 参与开发

请先阅读 README、架构和安全政策。报告问题或提交补丁前，删除所有真实服务器、订阅和用户信息。功能应继续服务单 VPS、小型跨境团队的明确需求。

## 本地构建

需要 Go 1.26.0+、Node 24 LTS、npm、Python 3、Git、curl、unzip、OpenSSL 1.1.1+（推荐 3.x）。运行：

```bash
bash scripts/check.sh
bash scripts/build.sh
bash scripts/build-hy2-core.sh --test
python3 scripts/third-party.py
python3 scripts/package.py
```

Go 不在 PATH 时设置 `GY_GO=/path/to/go`。前端依赖由 lockfile 固定，使用 `npm ci`。构建中间文件在 `.cache/` 和 `build/`，不提交。若访问 GitHub 需要代理，使用你自己配置的标准 `HTTPS_PROXY`，不要把代理凭证写进仓库。

## 本地 UI 开发

```bash
python3 scripts/dev.py
# 另一个终端：
cd frontend
npm ci --ignore-scripts
npm run dev
```

开发器只监听回环，在 `.cache/dev` 创建独立配置、数据库和随机初始凭证。开发模式模拟核心同步，不启动真实代理，不证明节点连通。按脚本输出的本地地址访问；Vite 的 API 代理见其配置。停止时 Ctrl+C，恢复测试基线可在停止后手动删除 `.cache/dev`。不要把开发模式暴露到公网。

## 目录

- `backend/`：Go 服务及行为测试。
- `frontend/`：Vue 3 UI、中文与 English 文案、前端单元测试。
- `deploy/`：systemd、安装/升级、证书与 Nginx 渲染，以及部署测试。
- `core-patches/`：固定 Hysteria 补丁、上游许可证。
- `scripts/`：构建、依赖获取、秘密/版本/链接检查、打包。
- `docs/`：安装、使用、配置、运维、架构、发布与验收。

macOS 自带 LibreSSL 不支持部署测试使用的 `x509 -checkhost`；完整部署测试应使用 OpenSSL 3 或在 Linux/CI 上运行，不应忽略证书校验。

## 验证要求

后端修改运行 `go test -race ./...` 和 `go vet ./...`；前端运行测试、i18n、类型与生产构建；部署变更运行部署行为测试和隔离环境验证。影响代理协议时，额外验证真实 TCP/UDP、用户隔离、出口、撤销；不能只用 HTTP health 作为验收。

Hysteria 补丁变更必须运行其真实 QUIC/TCP/UDP 集成测试。测试使用合成用户、保留示例网段或注入的网络响应，不使用生产订阅。新增依赖记录许可证并重新生成 notices/SBOM。

## PR 与版本

保持改动集中，描述用户可见行为、触发条件、测试与限制。修复安全问题遵循 SECURITY.md；不要在公开 issue 粘贴利用凭证。同步更新 `VERSION`、Go 版本常量、前端 package/lock 版本、文档与变更记录。发布流程只创建草稿，不自动转公开仓库。

提交代码表示你有权以项目 MIT 许可证贡献该代码；第三方代码需保留其原始许可与归属。
