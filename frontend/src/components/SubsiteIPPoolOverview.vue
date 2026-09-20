<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { ArrowRight, Database, LoaderCircle, RefreshCw, Trash2 } from 'lucide-vue-next';
import { useApi, isCancelled } from '../lib/api';
import { usePanelContext } from '../composables/panelContext';
import type { ManagedSite } from '../lib/sites';
import type { IPResource, Node } from '../types';
import { t } from '../i18n';

const sitesAPI = useApi('/business-sites'), api = useApi();
const { state, go, refresh } = usePanelContext();
const sites = ref<ManagedSite[]>([]), busy = ref(false), error = ref(''), creating = ref('');
const pools = computed(() => (state.value?.ip_pool || []).filter(p => p.pool_group === 'subsite'));
const connected = computed(() => sites.value.filter(s => !!s.connection?.scope && !!s.mount));
function poolFor(site: ManagedSite, node: Node) { return pools.value.find(p => p.source === site.id && (p.notes || '').endsWith(' · ' + node.id)); }
async function load() { busy.value = true; error.value = ''; try { sites.value = (await sitesAPI<{sites:ManagedSite[]}>()).sites; } catch (e) { if (!isCancelled(e)) error.value = (e as Error).message; } finally { busy.value = false; } }
async function create(site: ManagedSite, node: Node) { creating.value = site.id + '/' + node.id; error.value = ''; try { await sitesAPI('/' + site.id + '/egress-pool', 'POST', {node_id: node.id}); await refresh(); await load(); } catch (e) { if (!isCancelled(e)) error.value = (e as Error).message; } finally { creating.value = ''; } }
async function remove(pool: IPResource) { busy.value = true; error.value = ''; try { await api('/ips/' + pool.id, 'DELETE', {}); await refresh(); } catch (e) { if (!isCancelled(e)) error.value = (e as Error).message; } finally { busy.value = false; } }
onMounted(load);
</script>

<template>
  <section class="subsite-egress">
    <div class="resource-section-heading"><div><h2>{{ t('子站 IP 池') }}</h2><p>{{ t('主站节点接收用户流量，再经本地中转连接子站节点；最终出口 IP 属于子站。') }}</p></div><div class="pool-actions"><button :disabled="busy" @click="load"><RefreshCw :size="15"/>{{ t('刷新') }}</button><button class="text-button" @click="go('subsite-nodes')"><ArrowRight :size="15"/>{{ t('管理直连子站节点') }}</button></div></div>
    <div class="pool-intro relay"><Database :size="19"/><div><strong>{{ t('主站中转模式') }}</strong><p>{{ t('与子站节点挂载不同：客户端仍连接主站，本地核心通过加密桥接使用子站 IP 出口。创建出口后，可在本地节点配置中选择它。') }}</p></div></div>
    <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
    <div v-if="pools.length" class="egress-list"><article v-for="p in pools" :key="p.id"><div><strong>{{ p.label || p.name }}</strong><small>{{ p.source || t('子站') }} · {{ p.probe_ip || t('等待检测') }} · {{ p.upstream_type?.toUpperCase() || 'SUBSITE' }}</small></div><span class="badge" :class="p.enabled ? 'success' : 'neutral'">{{ p.enabled ? t('已启用') : t('已停用') }}</span><button class="danger-button" :disabled="busy" @click="remove(p)"><Trash2 :size="14"/>{{ t('删除') }}</button></article></div>
    <div v-for="site in connected" :key="site.id" class="site-egress-card"><header><div><h3>{{ site.name }}</h3><p>{{ t('选择子站节点创建主站中转出口') }}</p></div><span class="badge success">{{ site.mount?.nodes.filter(n=>n.enabled).length || 0 }} {{ t('个可用节点') }}</span></header><div class="egress-node-grid"><article v-for="n in site.mount?.catalog.nodes.filter(n=>n.enabled)" :key="n.id"><div><span :class="['tag',n.protocol]">{{ n.protocol.toUpperCase() }}</span><strong>{{ n.name }}</strong><small>{{ n.probe_ip || t('待检测') }}</small></div><button class="primary" :disabled="!!creating||!!poolFor(site,n)" @click="create(site,n)"><LoaderCircle v-if="creating===site.id+'/'+n.id" :size="14" class="spin"/>{{ poolFor(site,n) ? t('已创建') : t('创建中转出口') }}</button></article></div></div>
    <div v-if="!connected.length&&!pools.length&&!busy" class="pool-empty"><h3>{{ t('尚未连接可用子站') }}</h3><p>{{ t('先在子站节点页面导入管理令牌，再选择子站 IP 创建中转出口。') }}</p><button @click="go('subsite-nodes')">{{ t('连接子站') }}</button></div>
  </section>
</template>

<style scoped>
.subsite-egress{display:grid;gap:18px}.pool-actions{display:flex;gap:8px;flex-wrap:wrap}.relay{display:flex;align-items:flex-start;gap:12px}.relay p{margin:5px 0 0;font-size:12px;line-height:1.7}.egress-list,.egress-node-grid{display:grid;gap:10px}.egress-list article{display:flex;align-items:center;gap:12px;padding:13px 16px;border:1px solid var(--border);border-radius:10px;background:var(--surface)}.egress-list article>div{flex:1;min-width:0}.egress-list small,.egress-node-grid small{display:block;color:var(--muted);font-size:11px;margin-top:5px;overflow-wrap:anywhere}.site-egress-card{padding:18px;border:1px solid var(--border);border-radius:12px;background:var(--surface)}.site-egress-card header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:13px}.site-egress-card h3{margin:0;font-size:15px}.site-egress-card header p{margin:5px 0 0;color:var(--muted);font-size:12px}.egress-node-grid article{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px;border:1px solid var(--border);border-radius:9px}.egress-node-grid article>div{min-width:0}.egress-node-grid strong{margin-left:8px;font-size:13px}.egress-node-grid button{flex-shrink:0}.pool-empty{text-align:center;padding:38px 18px;border:1px dashed var(--border);border-radius:12px;color:var(--muted)}.pool-empty h3{color:var(--text);font-size:15px}.pool-empty p{font-size:12px;line-height:1.7}@media(max-width:640px){.site-egress-card header,.egress-node-grid article{align-items:flex-start;flex-direction:column}.egress-node-grid button{width:100%}.egress-list article{align-items:flex-start;flex-wrap:wrap}}
</style>
