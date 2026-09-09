# 发布验收记录

## 0.14.0 历史基线

本版本由独立源码树构建，没有上传生产状态。以下记录具体检查与边界。

| 检查 | 已验证结果 |
| --- | --- |
| Go | 158 个测试函数；完整 race 测试与 vet 通过 |
| Vue / TypeScript | 10 个前端测试通过；1,427 项英文文案覆盖、类型检查、Vite 生产构建通过 |
| Hysteria 补丁 | 从固定上游 commit 构建，配置、未知身份拒绝和真实 QUIC/TCP/UDP 路由隔离测试通过 |
| Linux 部署行为 | 包校验/篡改拒绝、域名注入防护、证书匹配与成对切换已验证；包括互斥锁与符号链接拒绝，共 10 项部署用例通过 |
| Ubuntu 22.04 / 24.04 | 实际 systemd 安装、Nginx HTTPS、目录权限、私有/公共订阅分离、重复安装拒绝、证书轮换、正常升级和故障回滚通过 |
| 秘密扫描 | Gitleaks 扫描源码和完整新历史；仅对已验证的公开 Xray SHA256 做精确误报例外 |
| 发布目录检查 | 版本一致、相对文档链接、禁止文件和私人路径检查通过 |
| 本地 UI | 使用合成账号/流量检查中文/English、简易/专业导航与工作台；没有使用生产截图 |

完整安装基线：[CI 34372630967](https://github.com/wudi000888-svg/guangyue-panel/actions/runs/34372630967)。最终提交的最新验证结果请查看[仓库 Actions](https://github.com/wudi000888-svg/guangyue-panel/actions)。

## 部署和验证边界

- 支持标准 Debian 12/13、Ubuntu 22.04/24.04 amd64 部署路径；完整 systemd 安装矩阵实际覆盖 Ubuntu 22.04/24.04。Debian 容器验证了 Python/OpenSSL 部署用例，未声称完成 Debian 全套 systemd 安装。
- 核心 QUIC/TCP/UDP 测试与真实 Nginx HTTPS 已执行；本次没有改动现有生产服务器，也没有将开发模式 health 或模拟流量当作公网吞吐实测。
- 恢复备份相关的 Go 回归在本版本通过；跨文件恢复不是断电原子事务。
- 全部模块图对应的锁定依赖记录与许可证汇总由构建脚本生成，数量以 Release 的 SBOM 为准；SPDX 未自动识别项保留 NOASSERTION，不冒充人工法律审计。
- 当前发布未配置独立离线签名；产物有 SHA256 与版本来源记录。

## Go 构建与依赖漏洞扫描

CI 与源码构建固定使用 Go 1.26.8，发布包包含 Go BSD-3-Clause 许可证。

`govulncheck v1.1.4`（Go 1.26.8）未发现被本面板调用的漏洞，也未发现被导入包的漏洞。模块级别提示 GO-2026-5932：`golang.org/x/crypto/openpgp` 已停止维护；本面板不导入该包，使用同模块的 bcrypt 等维护包。此结果不等于所有第三方代理核心都经过完整安全审计。

## 0.14.0 私有准备阶段的 GitHub 设置（历史记录）

仓库创建为 Private。已配置依赖更新与告警；私密漏洞报告 API 在私有阶段返回不可用（404），不能记录成已启用。主分支保护 API 明确要求 GitHub Pro 或公开仓库，当前未开启。转公开时按 RELEASE.md 再启用可用的安全与分支保护功能。不会自动更改 visibility。


## 0.15.0 双版本验证（2026-09-10）

- Lite 全量 Go race 测试、vet 通过；迁移校验、任务恢复/取消、站点令牌和资源隔离包含错误路径。
- 使用独立测试 PostgreSQL/Redis：账户鉴权、订阅轮换、来源级联、共享归属、私有/公共隔离、默认节点保护、多 HY2、过期响应拒绝通过；跨数据库快照、主键序列、站点独占、Pro 备份恢复与注入文件失败回滚通过。
- 前端 13 个单元测试、递归英文文案检查、类型检查与生产构建通过。
- 实际 Chromium：Lite 登录与 13 个页面导航、桌面/手机截图检查；两个 Pro 测试控制器的接入、远程管理切换、返回本站与任务页面通过，无页面异常。
- Hysteria 2.9.2 补丁构建、应用节点路由测试和 TCP/UDP 集成测试通过。
- 当前待发布源码与 Git 历史 gitleaks 扫描无泄露。测试数据与下载的上游测试密钥仅在被排除的开发缓存中，不在发布树。
- govulncheck 实际调用路径无已知漏洞。模块级另标记未被本项目使用的 `golang.org/x/crypto/openpgp`（GO-2026-5932，无修复版本）；本项目不导入该包。

### 安装、迁移与真实网络

- [双版本安装 CI 34391028327](https://github.com/wudi000888-svg/guangyue-panel/actions/runs/34391028327) 全部成功：Ubuntu 22.04 Lite 安装、Lite→Pro 迁移与升级；Ubuntu 24.04 Pro 安装与升级；两条路径均验证注入故障后的回滚。
- Debian 13.2 amd64 现有站点已完成 SQLite→PostgreSQL Pro 升级，使用专属 PostgreSQL 17 schema 与回环 Redis 缓存，设置数据库、缓存与控制器内存上限。迁移前后原用户身份、密码哈希、订阅凭据（解密后比较）、主密钥、节点和出口资源保持一致；受限离线备份保留。
- 使用升级前导出的同一份真实客户端配置，从 macOS 分别完成 VLESS Reality/Vision 和 HY2 的 HTTPS 出口核验、SOCKS5 UDP DNS 查询与 1 MiB 下载。两种协议均成功，出口符合预期；没有把短下载样本当作持续带宽性能承诺。
- 本机现有 TUN 路径导致 VLESS 收到伪装站证书；SSH 转发隔离和实际网卡绑定对照成功。最终 VLESS 使用 Xray 绑定物理网卡直连完成验收，未修改服务端 Reality 密钥或 SNI。
- 线上 Chromium 使用原管理员登录，验证 14 个页面、Pro 品牌、PostgreSQL/Redis 状态、无控制器错误，以及 390px 手机宽度无横向溢出。生产截图、凭据与服务器配置没有进入源码或发布包。
- 修复迁移站点的历史证书域名配置，仅保留该站点实际解析到本机的域名；真实证书重新签发成功，Certbot 自动续期模拟与部署钩子验证通过。
- 线上流量采样揭示 PostgreSQL `ON CONFLICT` 中累加字段必须限定表名，已修复。补充真实 PostgreSQL 的计费检查点、计数器重置、配额和普通/无日志模式切换回归，SQLite 与 PostgreSQL 均通过。

最终 tag 对应的全量测试、安装与发布包来源，以[仓库 Actions](https://github.com/wudi000888-svg/guangyue-panel/actions)和 [v0.15.0 Release](https://github.com/wudi000888-svg/guangyue-panel/releases/tag/v0.15.0) 为准。上述结果仅适用于本次改动，不能代替未来版本验证。
