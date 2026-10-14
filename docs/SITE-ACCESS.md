# 按国家或地区管理面板访问

国家访问限制用于管理浏览器对面板的访问，**默认关闭**。受限访客看到拒绝页面、访问 IP、识别国家和管理员填写的原因。它不限制代理流量，也不代替账号登录。

## 启用步骤

1. 管理员打开 **系统设置 → 下载与访问**。
2. 先点击 **检测当前访问**，核对自己正在使用的 IP 和识别国家，保留 SSH 管理入口。
3. 选择禁止国家或地区，填写对访客可见的原因，再启用并保存。
4. 从允许和禁止地区分别测试登录与成员页面。成员不能修改规则。

## 判断方式与影响范围

国家查询使用随面板发布的本地数据库，不向第三方发送访客 IP。规则按请求的实际连接地址判断，不接受 `?ip=` 等访客自报地址。关闭时普通访问状态接口不做国家查询；管理员主动检测仍可查看自己的判断结果。

受限地区的浏览器面板 API 返回 `403`，包含 `access_denied`、当前 IP、国家、规则原因与数据库版本，页面据此显示访问提示。登录、注册、已登录成员与管理员的业务 API 都受规则约束，客户端下载中继也包含在内。静态页面、脚本和样式继续可加载，以便显示完整拒绝页面。

以下路径保留其原有认证与校验，不应用国家限制：

- 面板健康检查 `/api/health` 与访问提示 `/api/access-status`。
- 订阅 `/sub/…`、`/public-sub/…`，以及 VLESS、Hysteria2 等实际代理流量。
- 站点机器接口 `/api/business/…`、`/api/fleet-gateway`。
- 支付服务回调 `/api/payments/webhook/…`。

国家限制用于面板访问管理，不是代理出口限制，也不代替登录认证。代理、VPN 或移动运营商出口可能使判断国家与人员所在地不同。私网、未分配及数据库无法定位的地址保持国家未知，不猜测归属、不匹配国家黑名单。读取规则或数据库失败时，受保护 API 返回 `503`，不会静默当作规则已关闭。

## 网关与真实访问 IP

当前一键安装由专用网关转发面板请求，清除访客传入的地址头，并根据实际 TCP 对端写入 `X-Real-IP`。原生网关通过本机 loopback 访问面板；Docker 中面板与网关共享受管网络命名空间，内部接口不发布宿主端口。Docker 当前公网入口仅使用 IPv4，域名检测到 AAAA 时应改为 IPv4 DNS 或选择原生双栈，见[部署范围](DOCKER.md)。

旧版 Nginx 原生部署继续使用 `proxy_set_header X-Real-IP $remote_addr` 覆盖访客头；若前面有 stream / PROXY protocol，应先恢复真实对端。面板仅在直接 TCP 对端为 IPv4/IPv6 loopback 时信任有效 `X-Real-IP`，不使用 `X-Forwarded-For` 判断国家。

域名须直接解析到服务器，关闭 CDN/橙云代理。当前规则不自动信任任意网段或远程反向代理；地址链配置错误时可能识别到代理服务器国家。不要为改变判断而公开内部端口或将公网来源伪装为可信 loopback。

## 误封后的 SSH 恢复

国家规则不限制 SSH。以下脚本适用于 **原生安装和旧版 Nginx 原生部署**：在服务器本地 loopback 调用 API，使用现有管理员账号仅关闭访问开关，不恢复数据库、不替换其他设置。

**Docker 安装不适用宿主 `127.0.0.1:19100`**，也没有 `guangyue` 应用 systemd 单元。该接口位于容器网络命名空间；不要将其临时映射到公网。启用规则前核对当前来源并保留允许地区的管理连接，Docker 状态与受管维护方式见[Docker 运维](DOCKER.md#管理与恢复)。

先确认服务运行：

```bash
sudo systemctl is-active guangyue
curl -fsS http://127.0.0.1:19100/api/health
```

在服务器的交互式 SSH 终端执行以下命令。账号和密码从终端读取，Cookie 仅保存在脚本内存，不写入命令行或文件；程序只修改国家访问开关，并保留国家列表、原因及其他配置。

```bash
sudo python3 - <<'PY'
import getpass
import json
from http.cookies import SimpleCookie
from urllib.error import HTTPError
from urllib.request import ProxyHandler, Request, build_opener

opener = build_opener(ProxyHandler({}))
base = 'http://127.0.0.1:19100'
token = ''

def call(endpoint, method='GET', payload=None):
    headers = {'Content-Type': 'application/json', 'X-Requested-With': 'guangyue'}
    if token:
        headers['Cookie'] = 'gy_session=' + token
    data = None if payload is None else json.dumps(payload).encode()
    with opener.open(Request(base + endpoint, data=data, headers=headers, method=method), timeout=15) as response:
        return json.load(response), response.headers

try:
    with open('/dev/tty', 'r+') as terminal:
        terminal.write('管理员账号 [owner]：')
        terminal.flush()
        username = terminal.readline().strip() or 'owner'
    password = getpass.getpass('管理员密码：')
    _, headers = call('/api/login', 'POST', {'username': username, 'password': password})
    password = ''
    cookies = SimpleCookie()
    for value in headers.get_all('Set-Cookie', []):
        cookies.load(value)
    token = cookies['gy_session'].value
    settings, _ = call('/api/settings')
    settings['country_access_enabled'] = False
    result, _ = call('/api/settings', 'PUT', settings)
    if result.get('country_access_enabled') is not False:
        raise RuntimeError('未确认关闭，请检查返回状态')
    print('国家访问限制已关闭，可以重新打开面板。')
except HTTPError as error:
    raise SystemExit('恢复请求未成功，HTTP ' + str(error.code) + '；请确认管理员账号与服务状态。')
finally:
    if token:
        try:
            call('/api/logout', 'POST', {})
        except Exception:
            pass
PY
```

该脚本适用于原生默认内部端口。自定义监听地址应使用实际的本机地址。若管理员密码遗失，应先按账号恢复流程处理；不要为绕过登录而公开内部端口或信任外部伪造地址。

## 数据来源与许可

[IP Geolocation by DB-IP](https://db-ip.com/)：本版本采用 [DB-IP IP to Country Lite](https://db-ip.com/db/download/ip-to-country-lite) `2026-10` 数据，许可为 [Creative Commons Attribution 4.0 International](https://creativecommons.org/licenses/by/4.0/)。原始 MMDB 仅解压与嵌入，记录未经修改。

源码目录 `backend/internal/geoip/` 保存 `country.mmdb`、来源与 SHA256 清单、完整 `LICENSE.txt` 及 `NOTICE.txt`。安装包的 `licenses/DB-IP-COUNTRY-LICENSE.txt`、`DB-IP-COUNTRY-NOTICE.txt`、`DB-IP-COUNTRY-SOURCE.json` 和 SPDX SBOM 记录相同署名、许可、版本及下载来源。数据随面板版本更新，不在每次访客请求时下载。
