<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, busy, userSearch, userFilter, userRole, expiringUsers, active, userStatus, users, usage, bytes, date, editUser, toggleUser, showSub } = usePanelContext();
import { Pencil, Plus, Power, QrCode, Search } from "lucide-vue-next";
import { t } from "../i18n";
import { usePagination } from "../composables/usePagination";
import ListTable from "../components/ListTable.vue";
const {page:listPage,pages:listPages,rows:listRows}=usePagination(users);
</script>
<template>
<section v-if="state">
          <div class="page-heading">
            <div>
              <div class="eyebrow">ACCESS</div>
              <h1>{{ t("用户管理") }}<span class="count">{{ state.users.length }}</span>
              </h1>
            </div>
            <button class="primary" @click="editUser()">
              <Plus :size="17" />{{ t("添加成员") }}</button>
          </div>
          <div class="user-stat-grid">
            <div>
              <span>{{ t("全部用户") }}</span><strong>{{ state.users.length }}</strong>
            </div>
            <div>
              <span>{{ t("当前可用") }}</span><strong>{{ state.totals.active }}</strong>
            </div>
            <div>
              <span>{{ t("7 天内到期") }}</span><strong>{{ expiringUsers }}</strong>
            </div>
            <div>
              <span>{{ t("累计流量") }}</span
              ><strong>{{
                bytes(state.totals.upload + state.totals.download)
              }}</strong>
            </div>
          </div>
          <div class="toolbar user-toolbar">
            <div class="search">
              <Search :size="17" /><input
                v-model="userSearch"
                :placeholder="t('搜索成员账号')"
                :aria-label="t('搜索成员')"
              />
            </div>
            <select v-model="userFilter" :aria-label="t('账号状态')">
              <option value="all">{{ t("全部状态") }}</option>
              <option value="active">{{ t("正常") }}</option>
              <option value="disabled">{{ t("已停用") }}</option>
              <option value="expired">{{ t("已到期") }}</option>
              <option value="exhausted">{{ t("额度用尽") }}</option></select
            ><select v-model="userRole" :aria-label="t('用户角色')">
              <option value="all">{{ t("全部角色") }}</option>
              <option value="owner">{{ t("管理员") }}</option>
              <option value="user">{{ t("企业成员") }}</option></select
            ><span class="spacer"></span
            ><span class="muted">{{ users.length }}{{ t("位成员") }}</span>
          </div>
          <ListTable :total="users.length" v-model:page="listPage" :pages="listPages">
            <table class="adaptive-table">
              <thead>
                <tr>
                  <th>{{ t("成员") }}</th>
                  <th>{{ t("状态") }}</th>
                  <th>{{ t("协议") }}</th>
                  <th class="usage-cell">{{ t("流量使用") }}</th>
                  <th>{{ t("到期时间") }}</th>
                  <th>{{ t("最近订阅") }}</th>
                  <th class="right">{{ t("操作") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="u in listRows" :key="u.id">
                  <td :data-label="t('成员')">
                    <div class="user-cell">
                      <span class="avatar small">{{
                        u.username.slice(0, 1).toUpperCase()
                      }}</span>
                      <div>
                        <strong>{{ u.username }}</strong
                        ><small>{{
                          u.role === "owner" ? t("管理员") : t("成员 #") + u.id
                        }}</small>
                      </div>
                    </div>
                  </td>
                  <td :data-label="t('状态')">
                    <span
                      :class="['badge', active(u) ? 'success' : 'danger']"
                      >{{ userStatus(u) }}</span
                    >
                  </td>
                  <td :data-label="t('协议')">
                    <div class="protocol-tags">
                      <span v-if="u.vless" class="tag vless">VLESS</span
                      ><span v-if="u.hy2" class="tag hy2">HY2</span
                      ><span v-if="!u.vless && !u.hy2" class="muted">{{ t("无") }}</span>
                    </div>
                  </td>
                  <td :data-label="t('流量使用')">
                    <div class="usage-text">
                      {{ bytes(u.upload + u.download)
                      }}<span>/ {{ u.quota ? bytes(u.quota) : t("不限") }}</span>
                    </div>
                    <div class="progress">
                      <span
                        :style="{ width: usage(u) + '%' }"
                        :class="{ exhausted: usage(u) >= 100 }"
                      />
                    </div>
                  </td>
                  <td :data-label="t('到期时间')">{{ date(u.expires) }}</td>
                  <td :data-label="t('最近订阅')">
                    {{ u.last_sub ? date(u.last_sub, true) : t("尚未获取") }}
                  </td>
                  <td :data-label="t('操作')">
                    <div class="row-actions">
                      <button class="icon" :title="t('查看订阅')" @click="showSub(u)">
                        <QrCode :size="17" /></button
                      ><button
                        class="icon"
                        :title="t('编辑成员')"
                        @click="editUser(u)"
                      >
                        <Pencil :size="16" /></button
                      ><button
                        class="icon"
                        :title="u.enabled ? t('停用成员') : t('启用成员')"
                        :disabled="u.role === 'owner' || busy"
                        @click="toggleUser(u)"
                      >
                        <Power :size="17" />
                      </button>
                    </div>
                  </td>
                </tr>
                <tr v-if="!users.length">
                  <td colspan="7" class="empty">{{ t("没有匹配的成员") }}</td>
                </tr>
              </tbody>
            </table>
          </ListTable>
        </section>
</template>
