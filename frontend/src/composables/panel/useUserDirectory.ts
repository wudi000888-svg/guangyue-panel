import { computed, ref, type Ref } from "vue";
import { quotaUsed } from "../../lib/quota";
import { t } from "../../i18n";
import type { State, User } from "../../types";

/** Member list filters and per-user status helpers derived from /state. */
export function useUserDirectory(state: Ref<State | null>) {
const userSearch = ref(""),
  userFilter = ref("all"),
  userRole = ref("all");
const expiringUsers = computed(
  () =>
    (state.value?.users || []).filter(
      (u) =>
        !u.mount_access && u.expires > Date.now() / 1000 &&
        u.expires < Date.now() / 1000 + 7 * 86400,
    ).length,
);
const active = (u: User) =>
  u.enabled &&
  (!u.expires || u.expires > Date.now() / 1000) &&
  (!u.quota || quotaUsed(u) < u.quota) && !u.meter?.pending_reset && (!u.meter?.end || u.meter.end > Date.now()/1000);
const userStatus = (u: User) =>
 u.archived ? t("已归档") :
 u.meter?.pending_reset || (u.meter?.end && u.meter.end<=Date.now()/1000) ? t("周期切换中") :
  !u.enabled
    ? t("已停用")
    : u.expires && u.expires <= Date.now() / 1000
      ? t("已到期")
      : u.quota && quotaUsed(u) >= u.quota
        ? t("额度用尽")
        : t("正常");
const users = computed(() =>
  (state.value?.users || []).filter(
    (u) =>
      !u.mount_access && u.username.toLowerCase().includes(userSearch.value.toLowerCase()) &&
      (userRole.value === "all" || u.role === userRole.value) &&
      (userFilter.value === "all" ||
        (userFilter.value === "active"
          ? active(u)
          : userFilter.value === "disabled"
            ? !u.enabled
            : userFilter.value === "expired"
              ? !!u.expires && u.expires <= Date.now() / 1000
              : !!u.quota && quotaUsed(u) >= u.quota)),
  ),
);
const usage = (u: User) =>
  u.quota ? Math.min(100, (quotaUsed(u) / u.quota) * 100) : 0;

return { userSearch, userFilter, userRole, expiringUsers, active, userStatus, users, usage };
}
