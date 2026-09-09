<script setup lang="ts">
import { useApi, isCancelled } from "./lib/api";
const api = useApi();
import { latestRequest } from "./lib/requests";
const loadRequest = latestRequest();
onUnmounted(()=>loadRequest.cancel());
import { onMounted, onUnmounted, reactive, ref } from 'vue';
import { Settings2, Save, Building2, Languages, LifeBuoy, Info } from 'lucide-vue-next';
import { t } from './i18n';
import RuntimeSettings from './RuntimeSettings.vue';
defineProps<{simpleMode?:boolean}>();
export type SiteSettings = {panel_name:string;organization:string;default_locale:string;support_email:string;login_notice:string;revision:string};
const emit=defineEmits<{saved:[]}>();
const form=reactive<SiteSettings>({panel_name:'',organization:'',default_locale:'zh-CN',support_email:'',login_notice:'',revision:''});
const ready=ref(false),busy=ref(false),error=ref(''),saved=ref(false);
async function load(){const request=loadRequest.start();try{const value=await api<SiteSettings>('/settings','GET',undefined,{signal:request.signal});if(!loadRequest.isCurrent(request))return;Object.assign(form,value);ready.value=true;error.value='';}catch(e){if(loadRequest.isCurrent(request)&&!isCancelled(e))error.value=(e as Error).message;}}
async function save(){if(busy.value)return;busy.value=true;loadRequest.cancel();error.value='';saved.value=false;try{const value=await api<SiteSettings>('/settings','PUT',form);Object.assign(form,value);saved.value=true;emit('saved');}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
onMounted(load);
</script>
<template>
 <section class="settings-page">
  <div class="page-heading"><div><div class="eyebrow">ADMINISTRATION</div><h1>{{t('系统设置')}}</h1><p class="section-subtitle">{{t('统一管理企业品牌、访问偏好与成员支持')}}</p></div><Settings2 :size="24"/></div>
  <p v-if="error" class="error" role="alert">{{t(error)}} <button @click="load">{{t('重新加载')}}</button></p>
  <p v-if="saved" class="settings-saved" role="status">{{t('系统设置已保存并生效')}}</p>
  <RuntimeSettings @updated="emit('saved')"/>
  <form v-if="ready" @submit.prevent="save">
   <div class="settings-grid"><div class="settings-sections">
    <section class="settings-card"><header><Building2 :size="19"/><div><h2>{{t('品牌与工作区')}}</h2><p>{{t('显示在登录页、侧边栏与浏览器标题中')}}</p></div></header><div class="settings-fields"><label>{{t('面板名称')}}<input v-model="form.panel_name" required maxlength="40" :aria-label="t('面板名称')"/></label><label>{{t('组织名称')}}<input v-model="form.organization" required maxlength="60" :aria-label="t('组织名称')"/></label></div></section>
    <section class="settings-card"><header><Languages :size="19"/><div><h2>{{t('语言与访问')}}</h2><p>{{t('成员可以在顶部覆盖默认语言，选择会保存在当前浏览器')}}</p></div></header><label>{{t('默认语言')}}<select v-model="form.default_locale" :aria-label="t('默认语言')"><option value="zh-CN">简体中文</option><option value="en">English</option></select></label><label>{{t('登录公告')}}<textarea v-model="form.login_notice" rows="4" maxlength="500" :aria-label="t('登录公告')" :placeholder="t('可填写维护时间或访问说明')"/></label><small>{{form.login_notice.length}} / 500 · {{t('对未登录访客可见')}}</small></section>
    <section class="settings-card"><header><LifeBuoy :size="19"/><div><h2>{{t('成员支持')}}</h2><p>{{t('联系邮箱显示在成员站内信页面')}}</p></div></header><label>{{t('联系邮箱')}}<input v-model="form.support_email" type="email" maxlength="254" placeholder="support@example.com" :aria-label="t('联系邮箱')"/></label></section>
   </div><aside class="settings-guide"><Info :size="22"/><h3>{{t('功能分区')}}</h3><dl><div><dt>{{t('业务资源')}}</dt><dd>{{t('普通节点、私有 IP 池及普通订阅，用于稳定业务接入。')}}</dd></div><div v-if="!simpleMode"><dt>{{t('公共代理')}}</dt><dd>{{t('自动采集的 IP 池、公共节点及独立公共订阅。')}}</dd></div><div><dt>{{t('系统管理')}}</dt><dd>{{t('成员权限、面板设置与运行状态。')}}</dd></div></dl><p v-if="!simpleMode">{{t('公共代理采集开关与筛选阈值请在公共 IP 池调整。')}}</p></aside></div>
   <footer class="settings-save"><span>{{t('修改面板设置不会中断节点连接')}}</span><button class="primary" :disabled="busy"><Save :size="16"/>{{t(busy?'保存中…':'保存设置')}}</button></footer>
  </form>
 </section>
</template>
