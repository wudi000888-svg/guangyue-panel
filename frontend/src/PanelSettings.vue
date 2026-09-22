<script setup lang="ts">
import { useApi, isCancelled } from "./lib/api";
const api = useApi();
import { latestRequest } from "./lib/requests";
const loadRequest = latestRequest();
onUnmounted(()=>loadRequest.cancel());
import { onMounted, onUnmounted, reactive, ref } from 'vue';
import { Save, Building2, ShieldCheck } from 'lucide-vue-next';
import { t } from './i18n';
const props=defineProps<{remoteSite?:boolean;publicFeaturesEnabled?:boolean}>();
const emit=defineEmits<{saved:[]; updatePublicFeaturesEnabled:[value:boolean]}>();
export type SiteSettings = {panel_name:string;organization:string;default_locale:string;support_email:string;login_notice:string;registration_enabled:boolean;registration_captcha:boolean;revision:string};
const form=reactive<SiteSettings>({panel_name:'',organization:'',default_locale:'zh-CN',support_email:'',login_notice:'',registration_enabled:false,registration_captcha:false,revision:''});
const ready=ref(false),busy=ref(false),error=ref(''),saved=ref(false);
async function load(){const request=loadRequest.start();try{const value=await api<SiteSettings>('/settings','GET',undefined,{signal:request.signal});if(!loadRequest.isCurrent(request))return;Object.assign(form,value);ready.value=true;error.value='';}catch(e){if(loadRequest.isCurrent(request)&&!isCancelled(e))error.value=(e as Error).message;}}
async function save(){if(busy.value)return;busy.value=true;loadRequest.cancel();error.value='';saved.value=false;try{const value=await api<SiteSettings>('/settings','PUT',form);Object.assign(form,value);saved.value=true;emit('saved');}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
onMounted(load);
</script>
<template>
<section class="site-preferences">
 <p v-if="error" class="error" role="alert">{{t(error)}} <button @click="load">{{t('重新加载')}}</button></p>
 <p v-if="saved" class="settings-saved" role="status">{{t('系统设置已保存并生效')}}</p>
 <form v-if="ready" @submit.prevent="save">
  <div class="preference-grid">
   <section class="settings-card"><header><Building2 :size="20"/><div><h2>{{t('站点信息')}}</h2><p>{{t('品牌、默认语言与成员联系方式')}}</p></div></header>
    <div class="settings-fields"><label>{{t('面板名称')}}<input v-model="form.panel_name" required maxlength="40"/></label><label>{{t('组织名称')}}<input v-model="form.organization" required maxlength="60"/></label><label>{{t('默认语言')}}<select v-model="form.default_locale"><option value="zh-CN">简体中文</option><option value="en">English</option></select></label><label>{{t('联系邮箱')}}<input v-model="form.support_email" type="email" maxlength="254" placeholder="support@example.com"/></label></div>
    <label>{{t('登录公告')}}<textarea v-model="form.login_notice" rows="3" maxlength="500" :placeholder="t('可填写维护时间或访问说明')"/></label><small>{{form.login_notice.length}} / 500 · {{t('对未登录访客可见')}}</small>
   </section>
   <section class="settings-card access-preferences"><header><ShieldCheck :size="20"/><div><h2>{{t('注册与访问')}}</h2><p>{{t('管理成员加入方式和公共功能入口')}}</p></div></header>
    <label class="preference-toggle"><span><strong>{{t('开放注册')}}</strong><small>{{t('注册账号默认获得演示套餐')}}</small></span><input v-model="form.registration_enabled" type="checkbox" role="switch"/></label>
    <label class="preference-toggle" :class="{muted:!form.registration_enabled}"><span><strong>{{t('拼图滑动验证')}}</strong><small>{{t('注册前完成拼图验证，支持手机与桌面')}}</small></span><input v-model="form.registration_captcha" type="checkbox" role="switch" :disabled="!form.registration_enabled"/></label>
    <div class="preference-note">{{t('用户订阅由生效套餐决定，套餐销售请前往“套餐上架”。')}}</div>
    <label class="preference-toggle public-toggle"><span><strong>{{t('公共功能显示')}}</strong><small>{{t('显示公共 IP 池、公共节点和公共订阅')}}</small></span><input :checked="publicFeaturesEnabled" type="checkbox" role="switch" @change="emit('updatePublicFeaturesEnabled',($event.target as HTMLInputElement).checked)"/></label><small>{{t('此显示偏好在当前浏览器立即生效，已有数据不会删除。')}}</small>
   </section>
  </div>
  <footer class="settings-save"><span>{{t('修改面板设置不会中断节点连接')}}</span><button class="primary" :disabled="busy"><Save :size="16"/>{{t(busy?'保存中…':'保存站点设置')}}</button></footer>
 </form><p v-else-if="!error" role="status">{{t('加载中…')}}</p>
</section>
</template>
<style scoped>
.preference-grid{display:grid;grid-template-columns:1.2fr 1fr;gap:18px;align-items:start}.settings-fields{gap:16px}.settings-card header{margin-bottom:20px}.settings-card .preference-toggle{display:flex;flex-direction:row;align-items:center;justify-content:space-between;gap:20px;margin:0;padding:18px 0;border-bottom:1px solid var(--border);cursor:pointer;white-space:normal}.preference-toggle>span{min-width:0}.preference-toggle strong{display:block;font-size:13px;font-weight:550}.preference-toggle small{display:block;margin-top:6px}.settings-card .preference-toggle input{appearance:none;width:37px;height:22px;min-height:22px;flex:0 0 37px;border:1px solid var(--border);border-radius:20px;background:var(--input);position:relative;cursor:pointer;margin:0;padding:0;transition:background .15s}.preference-toggle input:before{content:'';position:absolute;width:14px;height:14px;top:3px;left:3px;border-radius:50%;background:var(--muted);transition:transform .15s}.preference-toggle input:checked{background:var(--accent-soft);border-color:var(--accent)}.preference-toggle input:checked:before{transform:translateX(15px);background:var(--accent)}.preference-toggle input:focus-visible{outline:2px solid var(--accent);outline-offset:3px}.muted{opacity:.55}.preference-note{padding:14px;border-radius:8px;background:var(--accent-soft);font-size:11px;line-height:1.7;color:var(--secondary);margin:18px 0 2px}.settings-card .public-toggle{border-bottom:0;padding-bottom:8px}.settings-save{position:sticky;bottom:0;padding:16px 0;background:var(--surface);border-top:1px solid var(--border);z-index:1;margin-top:18px;border-radius:8px}.settings-save>span{padding-left:12px}.settings-save>button{margin-right:12px}@media(max-width:850px){.preference-grid{grid-template-columns:1fr}.settings-fields{grid-template-columns:1fr 1fr}}@media(max-width:900px){.settings-save{bottom:calc(76px + env(safe-area-inset-bottom, 0px))}}@media(max-width:500px){.settings-fields{grid-template-columns:1fr}.settings-save{align-items:center}.settings-save>span{font-size:10px}.settings-save>button{font-size:12px;padding:9px 12px}}
</style>
