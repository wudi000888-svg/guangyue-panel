<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { errorMessage } from '../lib/api';
import { ArrowRight, Globe2, LoaderCircle, RefreshCw, Search, Tag } from 'lucide-vue-next';
import { usePanelContext } from '../composables/panelContext';
import PageHeading from '../components/PageHeading.vue';
import CountryMark from '../CountryMark.vue';
import { t } from '../i18n';
import '../styles/member-ui.css';

type SquarePlan = { id: string; name: string; price: string; valid_days: number; cycle: string };
type SquareNode = { id: string; site_id: string; site_name?: string; name: string; protocol: 'vless' | 'hy2'; enabled: boolean; square_visible: boolean; published: boolean; country?: string; country_code?: string; plans: SquarePlan[] };

const { api, busy, owner } = usePanelContext();
const nodes = ref<SquareNode[]>([]), loading = ref(false), error = ref(''), search = ref(''), protocol = ref('all'), updating = ref<Record<string, boolean>>({});
async function load() {
  if (loading.value) return;
  loading.value = true; error.value = '';
  try {
    const response = await api<{ nodes: SquareNode[] }>('/node-square');
    nodes.value = response.nodes || [];
  } catch (reason) { error.value = errorMessage(reason); }
  finally { loading.value = false; }
}
function nodeKey(node: SquareNode) { return `${node.site_id}:${node.id}`; }
async function updateVisibility(node: SquareNode, value: boolean) {
  const key = nodeKey(node);
  if (!owner.value || updating.value[key]) return;
  const previous = node.square_visible;
  node.square_visible = value;
  updating.value = { ...updating.value, [key]: true };
  error.value = '';
  try {
    await api('/node-square', 'PUT', { site_id: node.site_id, node_id: node.id, square_visible: value });
    await load();
  } catch (reason) {
    node.square_visible = previous;
    error.value = errorMessage(reason);
  } finally {
    const next = { ...updating.value };
    delete next[key];
    updating.value = next;
  }
}
const visible = computed(() => {
  const query = search.value.trim().toLocaleLowerCase();
  return nodes.value.filter(node => (protocol.value === 'all' || node.protocol === protocol.value) && (!query || `${node.name} ${node.site_name || ''} ${node.country} ${node.country_code}`.toLocaleLowerCase().includes(query)));
});
function money(value: string) {
  const cents = Number(value);
  if (!Number.isFinite(cents) || cents === 0) return t('免费');
  return `¥${(cents / 100).toFixed(2)}`;
}
function cycle(plan: SquarePlan) {
  if (plan.valid_days > 0) return `${plan.valid_days}${t('天')}`;
  return plan.cycle === 'none' ? t('长期有效') : plan.cycle;
}
onMounted(load);
</script>

<template>
  <section class="member-page node-square-page">
    <PageHeading :title="t('节点广场')" :description="t(owner ? '管理公开展示的线路，并查看每条线路适用的套餐。' : '浏览管理员公开展示的线路，并查看每条线路适用的套餐。')" eyebrow="NODE SQUARE">
      <template #actions><button :disabled="loading || busy" @click="load"><RefreshCw :size="16" :class="{spin:loading}"/>{{t('刷新')}}</button></template>
    </PageHeading>
    <div class="node-square-intro"><span><Globe2 :size="23"/></span><div><h2>{{t(owner ? '控制公开线路' : '找到适合你的线路')}}</h2><p>{{t(owner ? '打开公开开关后，成员可以在节点广场看到节点名称、协议、地区和适用套餐。' : '这里仅展示公开信息；实际能否使用由你已购买的套餐决定。')}}</p></div><strong>{{visible.length}} {{t('条线路')}}</strong></div>
    <div class="node-square-toolbar"><label class="member-search"><Search :size="16"/><input v-model="search" type="search" :placeholder="t('搜索节点或地区')" :aria-label="t('搜索节点或地区')"/></label><div class="member-tabs" role="group" :aria-label="t('协议筛选')"><button :aria-pressed="protocol==='all'" @click="protocol='all'">{{t('全部')}}</button><button :aria-pressed="protocol==='vless'" @click="protocol='vless'">VLESS</button><button :aria-pressed="protocol==='hy2'" @click="protocol='hy2'">Hysteria2</button></div></div>
    <p v-if="error" class="error member-feedback" role="alert">{{t(error)}}<button :disabled="loading" @click="load">{{t('重试')}}</button></p>
    <div v-if="loading && !nodes.length" class="member-empty" role="status"><LoaderCircle :size="27" class="spin"/><p>{{t('正在读取节点广场…')}}</p></div>
    <div v-else-if="visible.length" class="node-square-grid">
      <article v-for="node in visible" :key="nodeKey(node)" class="member-panel node-square-card" :class="{ 'node-square-hidden': owner && !node.square_visible, 'node-square-disabled': owner && !node.enabled }">
        <header><CountryMark :code="node.country_code || ''" :country="node.country || ''"/><div><h2>{{node.name}}</h2><p>{{node.site_name ? node.site_name + ' · ' : ''}}{{node.country || t('地区待识别')}}<span v-if="node.country_code"> · {{node.country_code}}</span></p></div><span :class="['node-square-protocol',node.protocol]">{{node.protocol === 'hy2' ? 'Hysteria2' : 'VLESS'}}</span></header>
        <div v-if="owner" class="node-square-owner-control"><label><input type="checkbox" :checked="node.square_visible" :disabled="!!updating[nodeKey(node)]" @change="updateVisibility(node,($event.target as HTMLInputElement).checked)"/>{{t('在节点广场展示')}}</label><span v-if="!node.enabled">{{t('节点已停用')}}</span><span v-else-if="!node.plans.length">{{t('暂无上架套餐')}}</span><span v-else>{{node.published ? t('成员可见') : t('等待上架套餐')}}</span></div>
        <div class="node-square-plans"><div class="node-square-plans-head"><strong>{{t('可用套餐')}}</strong><span>{{node.plans.length}}</span></div><ul><li v-for="plan in node.plans" :key="plan.id"><span><Tag :size="14"/><b>{{plan.name}}</b><small>{{cycle(plan)}}</small></span><strong>{{money(plan.price)}}</strong></li></ul></div>
        <footer><span>{{owner ? t('公开状态由此处控制') : t('套餐生效后自动加入订阅')}}</span><RouterLink to="/shop">{{t('查看套餐')}}<ArrowRight :size="14"/></RouterLink></footer>
      </article>
    </div>
    <div v-else class="member-empty"><Globe2 :size="30"/><h2>{{t('暂无公开节点')}}</h2><p>{{t('管理员暂未公开符合条件的线路，或当前没有上架套餐。')}}</p><button @click="search='';protocol='all'">{{t('清除筛选')}}</button></div>
  </section>
</template>

<style scoped>
.node-square-intro{display:flex;align-items:center;gap:15px;padding:22px 24px;border:1px solid var(--border);border-radius:12px;background:var(--accent-soft)}
.node-square-intro>span{display:grid;place-items:center;width:47px;height:47px;border-radius:12px;background:var(--surface);color:var(--accent-text);flex-shrink:0}.node-square-intro h2{margin:0 0 5px;font-size:17px}.node-square-intro p{margin:0;color:var(--secondary);font-size:13px;line-height:1.7}.node-square-intro>strong{margin-left:auto;color:var(--accent-text);font-size:13px;white-space:nowrap}.node-square-toolbar{display:flex;align-items:center;gap:16px;flex-wrap:wrap}.node-square-toolbar .member-search{flex:1;min-width:220px}.node-square-toolbar .member-tabs{width:auto}.node-square-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,340px),1fr));gap:20px}.node-square-card{display:flex;flex-direction:column;gap:19px;min-width:0}.node-square-card>header{display:flex;align-items:center;gap:12px}.node-square-card>header>div{flex:1;min-width:0}.node-square-card h2{font-size:17px;line-height:1.5;margin:0;overflow-wrap:anywhere}.node-square-card header p{margin:5px 0 0;color:var(--secondary);font-size:12px}.node-square-protocol{padding:5px 8px;border-radius:6px;font-size:11px;white-space:nowrap}.node-square-protocol.vless{color:var(--accent-text);background:var(--accent-soft)}.node-square-protocol.hy2{color:var(--secondary);background:var(--surface-raised)}.node-square-plans{border-top:1px solid var(--border);border-bottom:1px solid var(--border);padding:14px 0}.node-square-plans-head{display:flex;justify-content:space-between;align-items:center;color:var(--secondary);font-size:12px;margin-bottom:6px}.node-square-plans-head strong{font-size:13px;color:var(--text)}.node-square-plans-head span{font-variant-numeric:tabular-nums}.node-square-plans ul{list-style:none;padding:0;margin:0}.node-square-plans li{display:flex;align-items:center;justify-content:space-between;gap:14px;padding:10px 0;border-top:1px solid var(--border)}.node-square-plans li>span{display:flex;align-items:center;gap:7px;min-width:0}.node-square-plans li svg{color:var(--accent-text);flex-shrink:0}.node-square-plans li b{font-size:13px;overflow-wrap:anywhere}.node-square-plans li small{font-size:11px;color:var(--secondary);white-space:nowrap}.node-square-plans li>strong{font-size:13px;white-space:nowrap}.node-square-card>footer{display:flex;justify-content:space-between;gap:12px;align-items:center;font-size:12px;color:var(--secondary);margin-top:auto}.node-square-card>footer a{display:inline-flex;align-items:center;gap:5px;color:var(--accent-text);text-decoration:none;min-height:36px;white-space:nowrap}.node-square-card>footer a:hover{color:var(--text)}.node-square-page .member-feedback{display:flex;justify-content:space-between;gap:12px;align-items:center}.node-square-page .member-feedback button{min-height:34px}@media(max-width:600px){.node-square-intro{padding:18px;align-items:flex-start;flex-wrap:wrap}.node-square-intro>div{flex:1;min-width:0}.node-square-intro>strong{margin-left:59px}.node-square-toolbar{align-items:stretch}.node-square-toolbar .member-search{flex-basis:100%;min-width:0}.node-square-toolbar .member-tabs{width:100%}.node-square-toolbar .member-tabs button{flex:1}.node-square-card{padding:18px}.node-square-card>footer{align-items:flex-start;flex-wrap:wrap}}
.node-square-owner-control{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:11px 12px;border:1px solid var(--border);border-radius:9px;background:var(--surface-raised);font-size:12px;color:var(--secondary)}.node-square-owner-control label{display:flex;align-items:center;gap:8px;margin:0;color:var(--text);cursor:pointer}.node-square-owner-control input{width:17px;height:17px;margin:0;accent-color:var(--accent)}.node-square-owner-control span{color:var(--muted);text-align:right}.node-square-hidden{border-style:dashed}.node-square-disabled{border-color:var(--input-border)}
</style>
