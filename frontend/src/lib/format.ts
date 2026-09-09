import { t, locale } from "../i18n";
export function bytes(n = 0) {
  if (n < 1024) return n + " B";
  const p = Math.min(4, Math.floor(Math.log(n) / Math.log(1024)));
  return (
    (n / 1024 ** p).toFixed(p > 1 ? 2 : 0) +
    " " +
    ["B", "KB", "MB", "GB", "TB"][p]
  );
}
export function date(n: number, time = false) {
  return n
    ? new Date(n * 1000).toLocaleString(
        locale.value,
        time
          ? {
              month: "2-digit",
              day: "2-digit",
              hour: "2-digit",
              minute: "2-digit",
            }
          : { year: "numeric", month: "2-digit", day: "2-digit" },
      )
    : t("不限");
}
export function duration(n: number) {
  return n >= 86400
    ? Math.floor(n / 86400) + t(" 天")
    : n >= 3600
      ? Math.floor(n / 3600) + t(" 小时")
      : Math.floor(n / 60) + t(" 分钟");
}
export function exitName(v: string) {
  return (
    (
      {
        direct: t("直连"),
        http: "HTTP",
        socks5: "SOCKS5",
        subscription: t("机场节点"),
      } as Record<string, string>
    )[v] || v
  );
}
