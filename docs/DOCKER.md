# Docker 部署与运行

0.41.0的新安装向导默认Docker，也可选择原生；共享固定端口、域名预检、版本校验和安装回执。操作见[一键安装](INSTALL.md)。

## 入口与系统范围

公网TCP443由本项目网关负责HTTPS、TLS-ALPN证书与Reality分流，UDP443用于HY2。端口固定，已有程序或停机容器声明443时会停止安装，不迁站、不关程序、不改其他公网端口。已有Nginx的HTTP80保留；域名必须指向本机并关闭CDN代理。

本次Docker公网仅绑定IPv4 `0.0.0.0:443`，域名必须仅A记录，检测到AAAA会要求改DNS或选原生双栈。Docker的IPv6 bridge/userland代理可能遮蔽真实IPv6来源，影响国家访问限制，不能仅凭TLS能通就声称来源准确；需要公网IPv6时选择原生。

预检查允许Debian12/13、Ubuntu22.04/24.04的amd64完整systemd服务器，Docker路径要求Linux5.4或更新；不提供ARM64资产。准确验证范围以该Release的CI为准，不把Debian容器启动测试当作Debian原生VM验收。

## Release 与镜像

正式版本发布八项资产：

| 资产 | 用途 |
| --- | --- |
| `guangyue-panel-lite-VERSION-linux-amd64.tar.gz` | 完整Lite应用、前端、网关和代理核心 |
| `guangyue-panel-pro-VERSION-linux-amd64.tar.gz` | 完整Pro组件 |
| `guangyue-installer-VERSION.tar.gz` | 固定版本Python安装器 |
| `install.sh` | 小型交互bootstrap |
| `guangyue-panel-docker-VERSION-linux-amd64.tar.gz` | 本版本应用及PostgreSQL/Redis镜像归档 |
| `DOCKER_IMAGES.json` | 版本、平台、归档摘要与不可变镜像ID |
| `SHA256SUMS` | 同版资产校验清单 |
| `SBOM.spdx.json` | 软件物料清单 |

归档从公开Release匿名下载，校验SHA256与镜像元数据后`docker load`，不需要GitHub/GHCR或镜像仓库账号。Compose固定到登记的镜像ID，启动不构建、不自动pull可变`latest`。普通面板更新不暗中更换PostgreSQL/Redis镜像版本，依赖改变需独立迁移。

## 隔离与宿主边界

每次安装使用UUID派生项目名和`org.guangyue.install-id`标签。维护器校验私有回执、项目、容器ID、标签、镜像ID及Compose摘要；失败只操作自己的容器，外部修改会阻止覆盖。

应用使用受限supervisor管理panel/Xray/HY2，网关独立运行；Pro另有私有PostgreSQL/Redis，不发布宿主数据库端口。面板容器不挂Docker socket，不使用privileged或宿主PID namespace；更新与回退经独立宿主维护执行器执行。数据库、主密钥与证书保留专属权限和持久数据目录。

安装器不更换默认路由、不改现有daemon配置、不重启已有Docker、不清空防火墙或改FORWARD策略。Docker bridge/NAT和端口发布仍依赖宿主网络支持，容器隔离不等于宿主网络零变化。环境要求现有策略不得变动，或需要额外全局调整才可用时，应选原生；不会自动改全局网关“修复”。

## 受管路径

| 路径 | 内容 |
| --- | --- |
| `/opt/guangyue-personal` | 已校验应用组件和版本 |
| `/opt/guangyue-docker/compose.json` | root0600 Compose定义 |
| `/opt/guangyue-docker/DOCKER_IMAGES.json` | 当前镜像元数据 |
| `/opt/guangyue-docker/postgres` | Pro数据库持久数据 |
| `/var/lib/guangyue` | 账户、SQLite/状态、Vault主密钥、钱包加密数据、TLS |
| `/etc/guangyue-personal.json` | 受保护应用配置 |
| `/etc/guangyue-deployment.json` | 后端、项目、镜像和安装基线身份 |
| `/var/lib/guangyue-installer/docker.json` | 私有安装现场与拥有权 |
| `/var/lib/guangyue-maintenance/update.sock` | 受限本机维护 socket，目录持久保存 |
| `/var/lib/guangyue-maintenance/pending` | 未提交版本的写入冻结，服务器重启后继续生效 |
| `/root/guangyue-backups` | 更新快照与回退记录 |

主密钥和数据必须一起备份；不要只复制钱包密文。镜像替换不等于数据库迁移，Redis是可重建缓存，PostgreSQL和面板状态需要持久备份。

## 管理与恢复

日常更新和兼容回退使用面板版本管理，安装失败使用同一脚本`--resume`。不会生成替代账号、钱包或Reality密钥。本机健康不代表公网UDP已验证，仍需真实客户端连接。

版本事务未提交时，网关和应用只放行健康检查，暂停用户操作、新连接授权与数据任务。维护冻结使用持久标记，重启后仍保留；恢复器验证原版本或已提交的新版本健康后才解除冻结。不要手动删除该标记来绕过恢复。

高级只读状态检查可以调用已校验的维护适配器，不输出配置凭据：

```bash
sudo python3 -I - <<'PY'
import sys
sys.path.insert(0, '/opt/guangyue-updater')
import docker_deploy
print(docker_deploy.compose('ps'))
PY
```

不要直接编辑受管Compose、手动改为`latest`或运行全局`docker system prune`；摘要改变时维护器会拒绝自动覆盖，避免把管理员新配置当作故障恢复。更换版别、数据库或后端需独立备份与迁移验证。

停用前保存完整备份，停止已登记项目及自己的维护入口，默认保留配置、数据、证书和回退记录。当前没有清空所有数据的生产参数，也不会卸载宿主共享Docker。
