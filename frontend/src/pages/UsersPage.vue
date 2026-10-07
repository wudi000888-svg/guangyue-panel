<script setup lang="ts">
import PageHeading from "../components/PageHeading.vue";
import "../styles/admin-ui.css";
import { usePanelContext } from "../composables/panelContext";
const { state, busy, refresh,nodeGroups,groupMembers,loadEntitlements,quotaUsed, userSearch, userFilter, userRole, expiringUsers, active, userStatus, users, usage, bytes, date, editUser, toggleUser, showSub } = usePanelContext();
import {computed,nextTick,onMounted,ref} from "vue";
import {userNodeGroups} from "../lib/nodeGroups";
import type {User} from "../types";
import {useModalFocus} from "../composables/useModalFocus";
import EntitlementDialog from "../components/EntitlementDialog.vue";
import { Pencil, Plus, Power, QrCode, Search,Package,X,QrCode as SubscriptionIcon } from "lucide-vue-next";
import { t } from "../i18n";
import { usePagination } from "../composables/usePagination";
import ListTable from "../components/ListTable.vue";
const localUsers=computed(()=>(state.value?.users||[]).filter(u=>!u.mount_access));
onMounted(()=>loadEntitlements());
const groupLabel=(u:User)=>{const groups=userNodeGroups(u).map(id=>nodeGroups.value.find(g=>g.id===id)?.name||id);const nodes=(u.entitlement?.node_ids||[]).map(id=>Object.values(groupMembers.value).flat().find(m=>m.selection_key===id)?.name||state.value?.nodes.find(n=>n.id===id)?.name||t('节点已移除'));return [...groups,...nodes].join('、')||t('无节点权限');};
const profileID=ref<number|null>(null),profileDialog=ref<HTMLElement|null>(null);
const profile=computed(()=>localUsers.value.find(u=>u.id===profileID.value));
useModalFocus(computed(()=>!!profile.value),profileDialog,()=>{profileID.value=null;});
const selected=ref<number[]>([]),entitlementUsers=ref<User[]>([]),planFilter=ref('all');
const planOptions=computed(()=>[...new Map(localUsers.value.filter(u=>u.entitlement).map(u=>[u.entitlement!.plan_id,u.entitlement!.name])).entries()]);
const visibleUsers=computed(()=>users.value.filter(u=>planFilter.value==='all'||u.entitlement?.plan_id===planFilter.value));
const {page:listPage,pages:listPages,rows:listRows}=usePagination(visibleUsers);
const allSelected=computed(()=>!!visibleUsers.value.length&&visibleUsers.value.every(u=>selected.value.includes(u.id)));
function toggleAll(){selected.value=allSelected.value?[]:visibleUsers.value.filter(u=>!u.archived).map(u=>u.id);}
async function profileAction(action:'edit'|'entitlement'|'subscription'){const member=profile.value;if(!member)return;profileID.value=null;await nextTick();if(action==='edit')editUser(member);else if(action==='entitlement')entitlementUsers.value=[member];else showSub(member);}
function editEntitlements(){entitlementUsers.value=localUsers.value.filter(u=>!u.archived&&selected.value.includes(u.id));}

</script>
<template>
<section v-if="state" class="admin-page">
 <PageHeading :title="t('用户管理')" :description="t('账号、当前套餐与实际访问权限集中查看。')" eyebrow="MEMBERS"><template #actions><button class="primary" @click="editUser()"><Plus :size="17"/>{{t('添加成员')}}</button></template></PageHeading>
          <div class="user-stat-grid">
            <div>
              <span>{{ t("全部用户") }}</span><strong>{{ localUsers.length }}</strong>
            </div>
            <div>
              <span>{{ t("当前可用") }}</span><strong>{{ localUsers.filter(active).length }}</strong>
            </div>
            <div>
              <span>{{ t("7 天内到期") }}</span><strong>{{ expiringUsers }}</strong>
            </div>
            <div>
              <span>{{ t("累计流量") }}</span
              ><strong>{{
                bytes(localUsers.reduce((total,u)=>total+u.upload+u.download,0))
              }}</strong>
            </div>
          </div>
          <div class="toolbar user-toolbar">
            <div class="search">
              <Search :size="17" /><input
                type="search" v-model="userSearch"
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
            ><select v-model="planFilter" :aria-label="t('套餐筛选')"><option value="all">{{t("全部套餐")}}</option><option v-for="[id,name] in planOptions" :key="id" :value="id">{{name}}</option></select><span class="spacer"></span
            ><span class="muted">{{ visibleUsers.length }}{{ t("位成员") }}</span>
          </div>
          <div v-if="selected.length" class="batch-toolbar"><strong>{{t("已选择")}} {{selected.length}}</strong><button class="primary" :disabled="busy" @click="editEntitlements"><Package :size="15"/>{{t("批量设置权益")}}</button><button @click="selected=[]">{{t("清空选择")}}</button></div>
 <ListTable :total="visibleUsers.length" v-model:page="listPage" :pages="listPages">
            <table class="adaptive-table">
              <thead>
                <tr>
                  <th class="select-cell"><input type="checkbox" :checked="allSelected" :aria-label="t('选择全部当前结果')" @change="toggleAll"/></th><th>{{ t("成员") }}</th>
 <th>{{t("套餐与节点")}}</th>
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
                        <button class="member-name" @click="profileID=u.id">{{ u.username }}</button
                        ><small>{{
                          u.role === "owner" ? t("管理员") : t("成员 #") + u.id
                        }}</small>
                      </div>
                    </div>
                  </td>
                  <td :data-label="t('套餐与节点')"><strong>{{u.entitlement?.name||t("暂无套餐")}}</strong><small v-if="u.entitlement">v{{u.entitlement.version}}</small><small class="user-groups">{{groupLabel(u)}}</small><small v-if="u.meter?.end">{{t("下次重置")}} · {{date(u.meter.end)}}</small><small v-if="u.plan_queue?.length">{{t("排队套餐")}} · {{u.plan_queue.length}}</small></td>
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
                  <td colspan="9" class="empty"><span>{{ t("没有匹配的成员") }}</span><button v-if="userSearch||userFilter!=='all'||userRole!=='all'" class="text-button empty-reset" @click="userSearch='';userFilter='all';userRole='all'">{{t('清除筛选')}}</button></td>
                </tr>
              </tbody>
            </table>
          </ListTable>
        <Teleport to="body"><div v-if="profile" class="message-overlay" @click.self="profileID=null"><section ref="profileDialog" tabindex="-1" class="compose-card member-profile" role="dialog" aria-modal="true" :aria-label="t('成员详情')+' '+profile.username"><header><div><span class="eyebrow">MEMBER PROFILE</span><h2>{{profile.username}}</h2><p>{{t(profile.role==='owner'?'管理员':'成员')}} #{{profile.id}} · {{userStatus(profile)}}</p></div><button class="icon" :aria-label="t('关闭')" @click="profileID=null"><X :size="20"/></button></header><section class="profile-current"><div><span>{{t('当前生效套餐')}}</span><h3>{{profile.entitlement?.name||t('暂无套餐')}}</h3><small v-if="profile.entitlement">v{{profile.entitlement.version}} · {{t('到期时间')}} {{date(profile.expires)}}</small></div><span class="badge" :class="active(profile)?'success':'neutral'">{{userStatus(profile)}}</span></section><div class="profile-usage"><div><span>{{t('本期配额用量')}}</span><strong>{{bytes(quotaUsed(profile))}}<small>/ {{profile.quota?bytes(profile.quota):t('不限')}}</small></strong></div><div><span>{{t('下次重置')}}</span><strong>{{profile.meter?.end?date(profile.meter.end):'—'}}</strong></div></div><section class="profile-detail-section"><h3>{{t('实际节点权限')}}</h3><p>{{groupLabel(profile)}}</p><div class="protocol-tags"><span v-if="profile.vless" class="tag vless">VLESS</span><span v-if="profile.hy2" class="tag hy2">HY2</span></div></section><section v-if="profile.plan_queue?.length" class="profile-detail-section"><h3>{{t('等待恢复的套餐')}}</h3><div v-for="slot in [...profile.plan_queue].reverse()" :key="slot.entitlement.revision" class="profile-queued"><strong>{{slot.entitlement.name}}</strong><span>{{t('剩余')}} {{slot.expires?Math.max(0,Math.ceil((slot.expires-slot.paused_at)/86400))+' '+t('天'):t('不限')}}</span></div></section><dl class="profile-history"><div><dt>{{t('创建时间')}}</dt><dd>{{date(profile.created)}}</dd></div><div><dt>{{t('最近订阅')}}</dt><dd>{{profile.last_sub?date(profile.last_sub,true):t('尚未获取')}}</dd></div><div><dt>{{t('实际累计流量')}}</dt><dd>{{bytes(profile.upload+profile.download)}}</dd></div></dl><footer v-if="!profile.archived"><button @click="profileAction('edit')"><Pencil :size="16"/>{{t('编辑成员')}}</button><button @click="profileAction('entitlement')"><Package :size="16"/>{{t('调整权益')}}</button><button class="primary" @click="profileAction('subscription')"><SubscriptionIcon :size="16"/>{{t('查看订阅')}}</button></footer><p v-else class="field-help">{{t('历史记录只读')}}</p></section></div></Teleport>
        <EntitlementDialog v-if="entitlementUsers.length" :users="entitlementUsers" @close="entitlementUsers=[]" @updated="selected=[];refresh()"/></section>
</template>
<style scoped>
.user-groups{display:block;max-width:220px;white-space:normal;overflow-wrap:anywhere;line-height:1.6;margin-top:5px}
.member-name{padding:0;min-height:32px;border:0;background:none;font-weight:600;color:var(--text);font-size:13px;text-align:left}.member-name:hover{color:var(--accent);text-decoration:underline;background:none}.member-profile{max-width:700px;max-height:92dvh;overflow:auto}.member-profile>header{align-items:flex-start;padding-bottom:18px;border-bottom:1px solid var(--border)}.member-profile h2{font-size:24px;margin:6px 0 8px}.member-profile header p{color:var(--muted);font-size:13px;margin:0}.profile-current{display:flex;align-items:center;justify-content:space-between;gap:14px;margin:22px 0;padding:20px;background:var(--accent-soft);border-radius:10px}.profile-current span,.profile-current small{font-size:12px}.profile-current h3{font-size:20px;margin:8px 0;color:var(--accent)}.profile-usage{display:grid;grid-template-columns:1fr 1fr;gap:20px;padding-bottom:22px;border-bottom:1px solid var(--border)}.profile-usage>div{display:grid;gap:10px}.profile-usage span,.profile-usage small{font-size:12px;color:var(--secondary)}.profile-usage strong{font-size:17px}.profile-usage small{display:block;margin-top:6px;font-weight:400}.profile-detail-section{padding:20px 0;border-bottom:1px solid var(--border)}.profile-detail-section h3{margin:0 0 10px;font-size:14px}.profile-detail-section p{margin:0 0 12px;font-size:13px;line-height:1.8;color:var(--secondary);overflow-wrap:anywhere}.profile-queued,.profile-history>div{display:flex;justify-content:space-between;gap:15px;font-size:12px;padding:10px 0}.profile-queued span,.profile-history dt{color:var(--muted)}.profile-history{margin:12px 0 0}.profile-history dd{margin:0}.member-profile footer{flex-wrap:wrap;gap:8px}.member-profile footer button{min-height:44px}@media(max-width:600px){.member-profile{padding:20px}.member-profile footer button{flex:1;font-size:12px}.profile-current{padding:16px}.profile-usage{gap:12px}.profile-usage strong{font-size:15px}.profile-history>div{font-size:12px}.member-name{min-height:44px}}
</style>
