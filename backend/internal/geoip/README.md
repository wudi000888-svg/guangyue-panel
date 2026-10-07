# 本地国家数据库

[IP Geolocation by DB-IP](https://db-ip.com/)：使用 [DB-IP IP to Country Lite](https://db-ip.com/db/download/ip-to-country-lite) 2026 年 10 月版，许可为 [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)。完整许可见 [LICENSE.txt](LICENSE.txt)，署名、来源及修改说明见 [NOTICE.txt](NOTICE.txt)。

`country.mmdb` 来自固定版本上游 gzip 的解压结果，记录未经修改，由 Go 嵌入应用。查询在站点本地完成，不向第三方发送访客 IP；来源与校验清单保存在 [source.json](source.json)。IP 所属国家可能与用户所在地不同，Lite 数据也可能滞后；访问控制行为见 [站点访问管理](../../../docs/SITE-ACCESS.md)。

| 项目 | 固定值 |
| --- | --- |
| 版本 | `2026-10` |
| MMDB 大小 | 8,348,509 bytes |
| 原始 gzip SHA256 | `4cd44536ddfd40fec7eca94a233f7d19f38d470e6522e653cb8d27001f5fe177` |
| MMDB SHA256 | `dbd70ccfa2a13627eaf4913d19920a1a229f1b3c5439ac01c509995b6364202a` |

更新时先取得对应月份的官方文件，核对 gzip 和解压后 MMDB 校验值，再同时更新数据库、`source.json`、本 README / NOTICE 和 `country.go` 的版本标识。运行国家数据库测试、站点访问测试及 `scripts/third-party.py`；许可文本、数据集版本和归属会一起进入安装包的第三方清单与 SPDX SBOM。
