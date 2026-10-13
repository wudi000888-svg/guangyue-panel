# 一键安装

0.41.0起的新安装默认Docker，也可在同一向导选择原生systemd。程序、代理核心、证书与Pro数据库自动准备，无需填写证书路径或数据库密码。旧Nginx方法见[旧部署指南](LEGACY_INSTALL.md)，已有站点继续使用[更新与回退](UPDATES.md)，不要重新安装覆盖数据。

## 安装前

1. 准备有root/sudo权限的Linux amd64完整服务器，公网TCP **443** 与UDP **443**均空闲。安装器不会停掉其他程序、迁移已有HTTPS站点或改用其他公网端口。
2. 将一个域名解析到本机，也可分别设置面板域名和节点域名。Cloudflare等服务使用**仅DNS**，关闭CDN/橙云代理。Docker当前仅IPv4公网，域名只能有A记录；需要AAAA/IPv6时选择原生双栈，并修正不可达的AAAA。当前面板网关支持最长63字符的域名，这不是DNS的通用上限。
3. 在云安全组和主机防火墙放行TCP443、UDP443，保留SSH端口。自动证书使用TLS-ALPN-01，经TCP443申请和续期，不接管HTTP80。

空白服务器、已有Nginx HTTP80且443空闲的服务器属于本次一键路径。已有Nginx配置声明443、停机Docker容器仍映射443，也会被检测；安装器不会删除配置或停止这些项目。

| 项目 | 范围 |
| --- | --- |
| 系统预检查白名单 | Debian12/13、Ubuntu22.04/24.04，systemd完整服务器 |
| 架构 | amd64/x86_64；当前不提供ARM64安装资产 |
| Docker内核 | Linux5.4或更新，Docker Engine与Compose插件 |
| 公网IP | Docker仅IPv4；原生支持IPv4/IPv6双栈 |
| 内存建议 | Lite1核1GiB；Pro2核2GiB，另为其他程序留余量 |
| 可用空间预检查 | 原生至少2GiB；Docker至少4GiB，另留数据、镜像、日志与备份空间 |
| 测试边界 | 发布CI包含Ubuntu真实VM安装与回退验证；实际结果看该Release关联CI。Debian白名单不等同完成Debian原生VM验收 |

## 运行向导

将脚本下载到当前用户的私有临时文件，再执行。以下整段可复制到服务器终端：

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

向导询问Lite/Pro、面板域名、节点域名（回车同域）、部署方式（回车Docker），以及是否同意Let’s Encrypt条款。检查系统、资源、时间、APT锁、443、Nginx/Docker配置、DNS和下载通道后展示计划，确认后才准备面板服务。

bootstrap缺少完整Python3或CA信任根时，只补齐缺失启动依赖；APT模拟发现需升级或移除既有包则停止。原生后端只安装必要依赖，不全量升级系统。新安装默认不改宿主BBR、qdisc或UDP sysctl，保留已有Nginx配置和HTTP80响应。

版本只在开始时选择一次，要求正式Release八项资产完整，不选择draft、预发布或未上传完整版本。验证SHA256、限制官方HTTPS重定向与解包路径；Docker镜像也从本次Release归档加载并固定镜像ID，不需要GitHub或镜像仓库账号。SHA256检测损坏，不替代对发布账号的信任，也不声称有独立离线签名。

## 固定版本与续装

下载同一个脚本后可使用：

```bash
sudo bash install.sh --version 0.41.0
sudo bash install.sh --version 0.41.0 --check
sudo bash install.sh --resume
```

`--check`只做检查，不安装依赖、创建面板配置或准备服务；缺Python/CA时提示后退出。`--resume`读取root私有回执，继续原计划、原版本，保留已经生成的主密钥、初始账号和数据。外部改动的unit/Compose及其他程序占端口时会拒绝覆盖。

无人值守入口为`--plan /root/plan.json --yes`，JSON需符合当前schema，明确`acme_agreed:true`，使用新UUID，版别/后端仅允许`lite|pro`与`docker|native`。它没有跳过DNS、端口、证书或下载校验的生产参数；普通部署推荐交互向导。

## 首次登录与节点检查

安装完成会显示HTTPS地址和初始账号文件。只在自己的终端读取，不贴到公开工单或截图：

```bash
sudo cat /var/lib/guangyue/initial-owner.json
```

用户名`owner`，密码随机生成；修改密码后初始凭据文件被删除。新站不自动开放注册、销售、邮件或支付，按管理员设置启用。Lite默认独立站，Pro默认主站；安装后可生成子站令牌并导入主站，不需要注册文件。

本机健康只证明本机服务和证书可用，不能单靠`ss`证明云安全组允许UDP。登录后获取管理员套餐的默认本地VLESS/HY2节点，分别验证客户端TCP连接、DNS、UDP业务、出口IP与流量计量，再交给成员使用。普通CDN不转发Reality/HY2。

## 更新、备份与排错

新原生和Docker站点沿自己的维护适配器更新/兼容回退，保留数据、密钥、地址历史与当前证书。回退不能低于安装基线；数据库不兼容时会被阻止。旧Nginx站点保留既有高级原生升级路径，无需迁移入口。详见[更新说明](UPDATES.md)及[Docker运行](DOCKER.md)。

更新事务未提交时保持维护冻结，只放行健康检查；root持久标记`/var/lib/guangyue-maintenance/pending`会跨重启保留。恢复器验证当前应运行版本健康后才重新放行用户操作与后台任务，不应手动删除标记跳过恢复。

| 路径 | 内容 |
| --- | --- |
| `/var/lib/guangyue-installer/receipt.json` | root0600安装计划和阶段 |
| `/var/lib/guangyue-installer/installer.log` | 脱敏阶段日志，不输出凭据或子进程完整输出 |
| `/var/lib/guangyue` | 账户、主密钥、钱包加密数据和TLS，需整体备份 |
| `/etc/guangyue-personal.json` | 受保护配置，可能含数据库凭据 |
| `/etc/guangyue-deployment.json` | root私有后端身份，不应手动更换backend |
| `/root/guangyue-backups` | 私有更新快照与回退记录 |

失败仅停止本次拥有的资源，保留私有现场供`--resume`。不要删除状态目录“重试”、关闭HTTPS校验或注释端口检查。原生可查`journalctl -u guangyue-gateway -u guangyue -u guangyue-hy2`；Docker按[Docker指南](DOCKER.md)检查已登记项目。不要公开完整配置、数据库、钱包备份或未检查的日志。

当前没有一键清空数据的生产参数。停用/移除程序前先保存完整备份，仅停止本项目并保留状态、配置、证书和备份。不卸载共享Docker/数据库包，不对其他项目运行全局`docker prune`。
