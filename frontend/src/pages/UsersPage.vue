<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, busy, refresh,quotaUsed, userSearch, userFilter, userRole, expiringUsers, active, userStatus, users, usage, bytes, date, editUser, toggleUser, showSub } = usePanelContext();
import {computed,ref} from "vue";
import type {User} from "../types";
import EntitlementDialog from "../components/EntitlementDialog.vue";
import { Pencil, Plus, Power, QrCode, Search,Package } from "lucide-vue-next";
import { t } from "../i18n";
import { usePagination } from "../composables/usePagination";
import ListTable from "../components/ListTable.vue";
const selected=ref<number[]>([]),entitlementUsers=ref<User[]>([]),planFilter=ref('all');
const planOptions=computed(()=>[...new Map((state.value?.users||[]).filter(u=>u.entitlement).map(u=>[u.entitlement!.plan_id,u.entitlement!.name])).entries()]);
const visibleUsers=computed(()=>users.value.filter(u=>planFilter.value==='all'||(planFilter.value==='independent'?!u.entitlement:u.entitlement?.plan_id===planFilter.value)));
const {page:listPage,pages:listPages,rows:listRows}=usePagination(visibleUsers);
const allSelected=computed(()=>!!visibleUsers.value.length&&visibleUsers.value.every(u=>selected.value.includes(u.id)));
function toggleAll(){selected.value=allSelected.value?[]:visibleUsers.value.filter(u=>!u.archived).map(u=>u.id);}
function editEntitlements(){entitlementUsers.value=(state.value?.users||[]).filter(u=>!u.archived&&selected.value.includes(u.id));}

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
            ><select v-model="planFilter" :aria-label="t('套餐筛选')"><option value="all">{{t("全部套餐")}}</option><option value="independent">{{t("独立配置")}}</option><option v-for="[id,name] in planOptions" :key="id" :value="id">{{name}}</option></select><span class="spacer"></span
            ><span class="muted">{{ visibleUsers.length }}{{ t("位成员") }}</span>
          </div>
          <div v-if="selected.length" class="batch-toolbar"><strong>{{t("已选择")}} {{selected.length}}</strong><button class="primary" :disabled="busy" @click="editEntitlements"><Package :size="15"/>{{t("批量设置权益")}}</button><button @click="selected=[]">{{t("清空选择")}}</button></div>
 <ListTable :total="visibleUsers.length" v-model:page="listPage" :pages="listPages">
            <table class="adaptive-table">
              <thead>
                <tr>
                  <th class="select-cell"><input type="checkbox" :checked="allSelected" :aria-label="t('选择全部当前结果')" @change="toggleAll"/></th><th>{{ t("成员") }}</th>
 <th>{{t("当前套餐")}}</th>
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
                  <td class="select-cell" :data-label="t('选择')"><input type="checkbox" v-model="selected" :value="u.id" :aria-label="t('选择')+' '+u.username"/></td><td :data-label="t('成员')">
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
                  <td :data-label="t('当前套餐')"><strong>{{u.entitlement?.name||t("独立配置")}}</strong><small v-if="u.entitlement">v{{u.entitlement.version}}</small><small v-if="u.meter?.end">{{t("下次重置")}} · {{date(u.meter.end)}}</small></td>
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
                      {{ bytes(quotaUsed(u))
                      }}<span>/ {{ u.quota ? bytes(u.quota) : t("不限") }}</span>
                    </div>
                    <small>{{t("实际累计流量")}} · {{bytes(u.upload+u.download)}}</small><div class="progress">
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
                    <span v-if="u.archived" class="muted">{{t("历史记录只读")}}</span><div v-else class="row-actions">
 <button class="icon" :title="t('套餐与权益')" @click="entitlementUsers=[u]"><Package :size="17"/></button><button class="icon" :title="t('查看订阅')" @click="showSub(u)">
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
                <tr v-if="!visibleUsers.length">
                  <td colspan="9" class="empty">{{ t("没有匹配的成员") }}</td>
                </tr>
              </tbody>
            </table>
          </ListTable>
        <EntitlementDialog v-if="entitlementUsers.length" :users="entitlementUsers" @close="entitlementUsers=[]" @updated="selected=[];refresh()"/></section>
</template>
