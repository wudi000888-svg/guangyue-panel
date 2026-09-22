<script setup lang="ts">
import {computed,nextTick,ref,watch} from 'vue';
import {Building2,CreditCard,Settings2,SlidersHorizontal} from 'lucide-vue-next';
import {usePanelContext} from '../composables/panelContext';
import {t} from '../i18n';
import CommerceSettings from '../components/CommerceSettings.vue';
import PanelSettings from '../PanelSettings.vue';
import RuntimeSettings from '../RuntimeSettings.vue';
import NetworkSettings from '../NetworkSettings.vue';
const {state,selectedSite,publicFeaturesEnabled,refresh}=usePanelContext();
const section=ref('site'),visited=ref(new Set(['site']));
const tabs=computed(()=>[{id:'site',label:t('站点与注册'),icon:Building2},...(!selectedSite.value?[{id:'commerce',label:t('交易与接入'),icon:CreditCard}]:[]),{id:'runtime',label:t('运行与网络'),icon:SlidersHorizontal}]);
function choose(id:string){section.value=id;visited.value.add(id);}
watch(selectedSite,()=>{section.value='site';visited.value=new Set(['site']);});
async function tabKey(event:KeyboardEvent){const keys=['ArrowLeft','ArrowRight','Home','End'];if(!keys.includes(event.key))return;event.preventDefault();const items=tabs.value,index=items.findIndex(v=>v.id===section.value);choose(items[event.key==='Home'?0:event.key==='End'?items.length-1:(index+(event.key==='ArrowRight'?1:-1)+items.length)%items.length].id);const parent=(event.currentTarget as HTMLElement).parentElement;await nextTick();parent?.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus();}
</script>
<template>
<section v-if="state" class="settings-hub">
 <header class="page-heading"><div><div class="eyebrow">PREFERENCES</div><h1>{{t('系统设置')}}</h1><p class="section-subtitle">{{t('按用途集中设置，切换页签保留未保存的修改。')}}</p></div><Settings2 :size="24"/></header>
 <nav class="settings-tabs" role="tablist" :aria-label="t('设置分类')"><button v-for="tab in tabs" :key="tab.id" :id="'settings-tab-'+tab.id" role="tab" :aria-selected="section===tab.id" :aria-controls="'settings-panel-'+tab.id" :tabindex="section===tab.id?0:-1" @click="choose(tab.id)" @keydown="tabKey"><component :is="tab.icon" :size="17"/><span>{{tab.label}}</span></button></nav>
 <div id="settings-panel-site" v-show="section==='site'" role="tabpanel" aria-labelledby="settings-tab-site"><PanelSettings :key="selectedSite||'local'" :remote-site="!!selectedSite" :public-features-enabled="publicFeaturesEnabled" @update-public-features-enabled="publicFeaturesEnabled=$event" @saved="refresh()"/></div>
 <div v-if="!selectedSite&&visited.has('commerce')" id="settings-panel-commerce" v-show="section==='commerce'" role="tabpanel" aria-labelledby="settings-tab-commerce"><CommerceSettings/></div>
 <div v-if="visited.has('runtime')" id="settings-panel-runtime" v-show="section==='runtime'" role="tabpanel" aria-labelledby="settings-tab-runtime" class="runtime-panel"><RuntimeSettings @updated="refresh()"/><NetworkSettings v-if="!selectedSite"/></div>
</section>
</template>
<style scoped>
.settings-hub{width:100%;max-width:1120px;margin:0 auto;display:flex;flex-direction:column;gap:20px}.settings-hub>.page-heading{margin:0}.settings-tabs{display:flex;flex-direction:row;margin:0;gap:5px;padding:5px;border:1px solid var(--border);border-radius:12px;background:var(--surface);width:fit-content;max-width:100%}.settings-tabs button{border:0;background:transparent;color:var(--muted);gap:8px;padding:11px 20px;min-height:43px;border-radius:8px;font-size:13px}.settings-tabs button[aria-selected=true]{background:var(--accent-soft);color:var(--accent-text)}.settings-tabs button:focus-visible{outline:2px solid var(--accent);outline-offset:-2px}.runtime-panel{display:flex;flex-direction:column;gap:20px}.runtime-panel :deep(.runtime-settings){margin:0}@media(max-width:650px){.settings-hub{gap:15px}.settings-tabs{width:100%;padding:4px;gap:2px}.settings-tabs button{flex:1;padding:9px 5px;gap:5px;font-size:11px;white-space:nowrap}.settings-tabs button svg{width:14px}}
</style>
