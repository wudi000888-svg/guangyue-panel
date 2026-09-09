# Nginx 与现有服务共存

## 443 的实际分工

TCP 443 由 Nginx `stream` 独占，读取 TLS ClientHello 的 SNI 后分流；不在这一层终止 TLS。面板与现有网站由回环 HTTPS 监听处理，Reality 由 Xray 处理。UDP 443 由 Hysteria 2 独占，因此现有网站的 HTTP/3/QUIC 不能再占用同一地址的 UDP 443。

| 入口/目标 | 地址 | 说明 |
| --- | --- | --- |
| TCP TLS 分流 | `0.0.0.0:443`、`[::]:443` | Nginx stream |
| 网站 HTTPS | `127.0.0.1:10443` | Nginx http，必须接受 PROXY protocol |
| 默认 Reality | `127.0.0.1:18443` | Xray，接受 PROXY protocol |
| 自定义 Reality | `/run/guangyue-reality/<sni>.sock` | 仅受限 Nginx worker 可连接 |
| HY2 | UDP 443 | 独立 QUIC 核心，使用节点域名证书 |

不要把普通 `location /` 反向代理当作 Reality 入口。不要把 Hysteria 2 误称为网站 HTTP/2。

## 已有 Nginx 网站的迁移

先备份 `/etc/nginx`，记录每个站点的 `server_name`、证书、监听地址和其他 TCP/UDP 服务。安排一次短维护窗口。

1. 每个需要保留的 HTTPS `server` 将 `listen 443 ssl ...;` / `listen [::]:443 ssl ...;` 改成 `listen 127.0.0.1:10443 ssl http2 proxy_protocol;`，去掉旧的 443 监听。保留站点的证书、root、location、upstream 等内容。
2. 配置正确的客户端来源：仅信任回环传入的 PROXY 信息。
3. 如果网站用了 UDP 443 HTTP/3，需要关闭该 QUIC 监听及相关 `Alt-Svc` 广告，或改用另一公网 IP。TCP 端网站仍可使用 HTTP/2。
4. 所有 10443 的虚拟主机都必须一致启用 `proxy_protocol`。该端口只能监听回环，不得暴露公网。
5. 安装时为每个需要保留的网站重复传入 `--web-domain`。未知 SNI 默认尝试受控 Reality socket，遗漏的站点不能正常访问。

示例已有站点：

```nginx
server {
    listen 127.0.0.1:10443 ssl http2 proxy_protocol;
    server_name shop.example.com;
    ssl_certificate /etc/letsencrypt/live/shop.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/shop.example.com/privkey.pem;
    set_real_ip_from 127.0.0.1;
    real_ip_header proxy_protocol;
    # 保留现有 root、location、反向代理、访问策略等。
}
```

使用 `nginx -t` 校验后 reload，释放 TCP 443。此时外部 HTTPS 会暂时中断，随后立即运行预检查和安装；安装参数添加 `--web-domain shop.example.com`。安装成功后分别检查所有站点、VLESS、HY2。

安装前可先生成并审阅完整分流片段：

```bash
python3 deploy/render.py --panel-domain panel.example.com \
  --node-domain node.example.com --web-domain shop.example.com \
  --output ./nginx-preview
```

`nginx-stream.conf` 的 `stream {}` 必须在 `nginx.conf` **顶层**引入；`nginx-panel.conf` 必须在 `http {}` 内引入。不能把 stream 放进通常属于 http 上下文的 `conf.d/*.conf`。

## 已经有 stream 块

安装器会拒绝自动叠加第二个 stream 块。管理员应合并生成文件里的 `map` 与 `server` 到既有 stream，确认没有另一个 TCP 443 监听。复杂的已有 stream、非发行版目录、自定义 worker 或 Caddy/Apache 接管场景按生成配置进行人工集成；不属于自动安装器的一键路径。

可以先在空闲测试 VPS 按标准安装验证，再将服务和片段逐项集成。不要通过注释预检查来强制覆盖；原有 stream 可能承载其他业务。

## 自定义 Reality SNI

面板保存新 SNI 时会校验名称、解析结果、本站地址与 TLS 能力，并固定通过校验的目标 IP。相同 SNI 的节点共享一个 Xray 入站，节点和用户权限仍独立。

Nginx 的受限域名正则将自定义 SNI 映射到 `/run/guangyue-reality/` 的 socket；未知域名没有 socket，会连接失败，不会让 Nginx任意解析网络目标。目录权限 `2750 guangyue:www-data`，socket `0660`。不要把数据库或密钥目录设为组可读来解决 socket 问题。

## 变更与排错

- 修改站点后先 `nginx -t` 再 reload，不直接重启或清空其他配置。
- `unknown directive stream`：检查 `libnginx-mod-stream` 和 `modules-enabled`。
- `duplicate stream`：只能有一个顶层 stream，合并内容。
- `broken header while reading PROXY protocol`：10443 收到了未经 stream 转发的 TLS，或分流两端 PROXY 设置不一致。
- HTTP 正常、VLESS 失败：核对客户端 Reality SNI、公钥、short ID、Vision、UUID；SNI 不能填面板域名。
- VLESS 正常、HY2 失败：检查 UDP 443、防火墙/安全组、证书域名、客户端是否支持 HY2。
- 新增网站打不开：将其域名显式加进 SNI map，目标 `127.0.0.1:10443`，并验证该站点接受 PROXY protocol。
