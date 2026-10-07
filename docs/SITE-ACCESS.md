# 站点访问管理

国家访问限制默认关闭。管理员在系统设置中开启后，选择禁止访问的国家或地区，并填写访客能看到的原因；成员不能修改规则。启用前先使用「检测当前访问」确认自己当前 IP 的国家判断，保留 SSH 管理入口。

## 判断方式与影响范围

国家查询使用随面板发布的本地数据库，不向第三方发送访客 IP。规则按请求的实际连接地址判断，不接受 `?ip=` 等访客自报地址。关闭时普通访问状态接口不做国家查询；管理员主动检测仍可查看自己的判断结果。

受限地区的浏览器面板 API 返回 `403`，包含 `access_denied`、当前 IP、国家、规则原因与数据库版本，页面据此显示访问提示。登录、注册、已登录成员与管理员的业务 API 都受规则约束，客户端下载中继也包含在内。静态页面、脚本和样式继续可加载，以便显示完整拒绝页面。

以下路径保留其原有认证与校验，不应用国家限制：

- 面板健康检查 `/api/health` 与访问提示 `/api/access-status`。
- 订阅 `/sub/…`、`/public-sub/…`，以及 VLESS、Hysteria2 等实际代理流量。
- 站点机器接口 `/api/business/…`、`/api/fleet-gateway`。
- 支付服务回调 `/api/payments/webhook/…`。

国家限制用于面板访问管理，不是代理出口限制，也不代替登录认证。代理、VPN 或移动运营商出口可能使判断国家与人员所在地不同。私网、未分配及数据库无法定位的地址保持国家未知，不猜测归属、不匹配国家黑名单。读取规则或数据库失败时，受保护 API 返回 `503`，不会静默当作规则已关闭。

## Nginx 与地址信任

标准部署由本机 Nginx 转发至 `127.0.0.1:19100`，使用 `proxy_set_header X-Real-IP $remote_addr` **覆盖**客户端传入的同名头。面板只有在直接 TCP 对端为 IPv4 / IPv6 loopback 时才信任有效的 `X-Real-IP`；公网直连请求不能用该头伪造国家。`X-Forwarded-For` 不参与此判断。

现有 Nginx stream / PROXY protocol 配置先恢复真实对端地址，再写入该头。不要把公网来源声明为 loopback 信任代理，也不要把面板内部端口公开。额外 CDN、远程反向代理或 Docker 网络需自行正确配置可信代理链；当前规则不会自动信任任意网段。若接入 CDN 未正确还原地址，规则看到的可能是 CDN 出口国家。

## 误封后的 SSH 恢复

国家规则不会阻止 SSH。标准安装可以登录服务器，在本地 loopback 调用面板 API，使用现有管理员账号关闭开关；该路径仍要求管理员认证。无需恢复旧数据库或修改其他设置。

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

该方式适用于标准内部端口。自定义监听地址应使用自己实际的本机地址。若管理员密码遗失，应先按账号恢复流程处理；不要为绕过登录而公开内部端口或信任外部伪造地址。

## 数据来源与许可

[IP Geolocation by DB-IP](https://db-ip.com/)：本版本采用 [DB-IP IP to Country Lite](https://db-ip.com/db/download/ip-to-country-lite) `2026-10` 数据，许可为 [Creative Commons Attribution 4.0 International](https://creativecommons.org/licenses/by/4.0/)。原始 MMDB 仅解压与嵌入，记录未经修改。

源码目录 `backend/internal/geoip/` 保存 `country.mmdb`、来源与 SHA256 清单、完整 `LICENSE.txt` 及 `NOTICE.txt`。安装包的 `licenses/DB-IP-COUNTRY-LICENSE.txt`、`DB-IP-COUNTRY-NOTICE.txt`、`DB-IP-COUNTRY-SOURCE.json` 和 SPDX SBOM 记录相同署名、许可、版本及下载来源。数据随面板版本更新，不在每次访客请求时下载。
