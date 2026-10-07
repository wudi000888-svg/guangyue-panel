<script setup lang="ts">
import PageHeading from "../components/PageHeading.vue";
import "../styles/admin-ui.css";
import {computed,nextTick,ref,watch} from 'vue';
import {Building2,CreditCard,Settings2,SlidersHorizontal,ChevronRight,ShieldCheck} from 'lucide-vue-next';
import {usePanelContext} from '../composables/panelContext';
import {t} from '../i18n';
import CommerceSettings from '../components/CommerceSettings.vue';
import PanelSettings from '../PanelSettings.vue';
import RuntimeSettings from '../RuntimeSettings.vue';
import NetworkSettings from '../NetworkSettings.vue';
const {state,selectedSite,publicFeaturesEnabled,refresh}=usePanelContext();
const section=ref('site'),visited=ref(new Set(['site']));
const tabs=computed(()=>[
 {id:'site',label:t('站点与注册'),description:t('品牌信息、成员注册与访问'),icon:Building2},
 ...(!selectedSite.value?[{id:'commerce',label:t('交易与接入'),description:t('套餐销售、支付与邮件服务'),icon:CreditCard}]:[]),
 {id:'runtime',label:t('运行与网络'),description:t('日志记录、网络与运行策略'),icon:SlidersHorizontal}
]);
function choose(id:string){section.value=id;visited.value.add(id);}
watch(selectedSite,()=>{section.value='site';visited.value=new Set(['site']);});
async function tabKey(event:KeyboardEvent){const keys=['ArrowLeft','ArrowRight','ArrowUp','ArrowDown','Home','End'];if(!keys.includes(event.key))return;event.preventDefault();const items=tabs.value,index=items.findIndex(v=>v.id===section.value);choose(items[event.key==='Home'?0:event.key==='End'?items.length-1:(index+(['ArrowRight','ArrowDown'].includes(event.key)?1:-1)+items.length)%items.length].id);const parent=(event.currentTarget as HTMLElement).parentElement;await nextTick();parent?.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus();}
</script>
<template>
<section v-if="state" class="settings-hub admin-page">
 <PageHeading :title="t('系统设置')" :description="t('按任务集中配置站点、交易和运行策略。')" eyebrow="SETTINGS"><template #actions><span class="settings-context"><Settings2 :size="16"/>{{t(selectedSite?'子站设置':'主站设置')}}</span></template></PageHeading>
 <div class="admin-context-strip"><ShieldCheck :size="18"/><div><strong>{{t(selectedSite?'正在配置所选子站':'正在配置当前主站')}}</strong><p>{{t('每个分区独立保存。切换分类会保留草稿，离开页面前请完成保存。')}}</p></div></div>
 <div class="settings-workspace">
  <aside class="settings-sidebar"><nav class="settings-tabs" :style="{'--settings-tab-count':tabs.length}" role="tablist" :aria-label="t('设置分类')"><button v-for="tab in tabs" :key="tab.id" :id="'settings-tab-'+tab.id" role="tab" :aria-selected="section===tab.id" :aria-controls="'settings-panel-'+tab.id" :tabindex="section===tab.id?0:-1" @click="choose(tab.id)" @keydown="tabKey"><span class="settings-tab-icon"><component :is="tab.icon" :size="19"/></span><span class="settings-tab-label"><strong>{{tab.label}}</strong><small>{{tab.description}}</small></span><ChevronRight :size="15" class="settings-tab-arrow"/></button></nav><p class="settings-guidance"><ShieldCheck :size="16"/><span>{{t('切换分类会保留未保存的修改，请在各项设置中保存。')}}</span></p></aside>
  <div class="settings-content">
   <div id="settings-panel-site" v-show="section==='site'" role="tabpanel" aria-labelledby="settings-tab-site" tabindex="0"><PanelSettings :key="selectedSite||'local'" :remote-site="!!selectedSite" :public-features-enabled="publicFeaturesEnabled" @update-public-features-enabled="publicFeaturesEnabled=$event" @saved="refresh()"/></div>
   <div v-if="!selectedSite&&visited.has('commerce')" id="settings-panel-commerce" v-show="section==='commerce'" role="tabpanel" aria-labelledby="settings-tab-commerce" tabindex="0"><CommerceSettings/></div>
   <div v-if="visited.has('runtime')" id="settings-panel-runtime" v-show="section==='runtime'" role="tabpanel" aria-labelledby="settings-tab-runtime" tabindex="0" class="runtime-panel"><RuntimeSettings @updated="refresh()"/><NetworkSettings v-if="!selectedSite"/></div>
  </div>
 </div>
</section>
</template>
<style scoped>
.settings-hub{width:100%;margin:0 auto;display:flex;flex-direction:column;gap:26px;min-width:0}.settings-hub>.page-heading{margin:0}.settings-context{display:flex;align-items:center;gap:8px;font-size:12px;color:var(--secondary);padding:10px 14px;border:1px solid var(--border);border-radius:10px;background:var(--surface)}.settings-workspace{display:grid;grid-template-columns:245px minmax(0,1fr);gap:28px;align-items:start}.settings-sidebar{position:sticky;top:24px}.settings-tabs{display:flex;flex-direction:column;gap:8px}.settings-tabs button{display:flex;justify-content:flex-start;text-align:left;width:100%;border:1px solid transparent;background:transparent;color:var(--secondary);gap:12px;padding:15px 13px;min-height:78px;border-radius:13px}.settings-tab-icon{display:grid;place-items:center;flex-shrink:0;width:34px;height:34px;border:1px solid var(--border);border-radius:10px;background:var(--surface)}.settings-tab-label{min-width:0;flex:1}.settings-tab-label strong{display:block;font-size:13px;font-weight:600}.settings-tab-label small{display:block;font-size:12px;line-height:1.6;color:var(--muted);margin-top:5px;white-space:normal}.settings-tabs button[aria-selected=true]{background:var(--surface);border-color:var(--border);color:var(--accent);box-shadow:0 3px 12px #1f342105}.settings-tabs button[aria-selected=true] .settings-tab-icon{background:var(--accent-soft);border-color:transparent}.settings-tabs button:not([aria-selected=true]) .settings-tab-arrow{opacity:0}.settings-tab-arrow{flex-shrink:0}.settings-tabs button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}.settings-guidance{display:flex;align-items:flex-start;gap:10px;color:var(--muted);font-size:12px;line-height:1.8;padding:18px 13px;margin:10px 0 0;border-top:1px solid var(--border)}.settings-guidance svg{flex-shrink:0;margin-top:2px}.settings-content{min-width:0}.settings-content>div:focus-visible{outline:2px solid var(--accent);outline-offset:5px;border-radius:12px}.runtime-panel{display:flex;flex-direction:column;gap:20px}.runtime-panel :deep(.runtime-settings){margin:0}.settings-content :deep(.preference-grid){grid-template-columns:1fr}.settings-content :deep(.settings-card){border-radius:16px}.settings-content :deep(.settings-card header h2){font-size:16px}.settings-content :deep(.settings-fields){display:grid;grid-template-columns:1fr 1fr;gap:18px}.settings-content :deep(.settings-save){border-radius:12px;padding:14px 0}.settings-content :deep(.preference-note){font-size:12px}.settings-content :deep(.settings-card .preference-toggle){padding:18px 0}.settings-content :deep(.settings-card .preference-toggle strong){font-size:13px}.settings-content :deep(.settings-card small){line-height:1.7}.settings-content :deep(button){min-height:44px}
@media(min-width:1500px){.settings-workspace{grid-template-columns:270px minmax(0,1fr)}.settings-content :deep(.preference-grid){grid-template-columns:1.15fr 1fr}}
@media(max-width:1000px){.settings-workspace{grid-template-columns:1fr;gap:20px}.settings-sidebar{position:static}.settings-tabs{display:grid;grid-template-columns:repeat(var(--settings-tab-count),minmax(0,1fr));gap:8px}.settings-tabs button{padding:12px 10px;gap:9px;min-height:70px}.settings-tab-arrow{display:none}.settings-guidance{margin:0;border:0;padding:12px 3px 0}.settings-tab-label small{font-size:12px}.settings-tab-icon{width:30px;height:30px}.settings-content :deep(.settings-save){bottom:0}}
@media(max-width:900px){.settings-content :deep(.settings-save){bottom:calc(76px + env(safe-area-inset-bottom, 0px))}}
@media(max-width:600px){.settings-hub{gap:18px}.settings-context{display:none}.settings-workspace{gap:14px}.settings-tabs{gap:5px}.settings-tabs button{padding:12px 5px;gap:7px;min-height:74px;flex-direction:column;text-align:center;border-color:var(--border);border-radius:12px}.settings-tab-label strong{font-size:12px}.settings-tab-label small{display:none}.settings-tab-icon{width:28px;height:28px;border:0;background:transparent}.settings-tabs button[aria-selected=true]{background:var(--accent-soft);border-color:color-mix(in srgb,var(--accent) 35%,var(--border))}.settings-guidance{font-size:12px;gap:7px}.settings-content :deep(.settings-fields){grid-template-columns:1fr}.settings-content :deep(.settings-card){padding:18px}.settings-content :deep(.settings-save){display:flex;flex-direction:column;gap:10px;align-items:stretch}.settings-content :deep(.settings-save>button){margin:0 12px}.settings-content :deep(.settings-save>span){font-size:12px;padding:0 12px}}
</style>
