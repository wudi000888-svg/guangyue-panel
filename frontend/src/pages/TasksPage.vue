<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { RefreshCw, XCircle, ListChecks } from 'lucide-vue-next';
import { useApi, isCancelled } from '../lib/api';
import { latestRequest, serialPoll } from '../lib/requests';
import type { BackgroundTask } from '../lib/tasks';
import { t } from '../i18n';
import TablePageLayout from '../components/TablePageLayout.vue';
const api=useApi('/tasks'),requests=latestRequest();
const tasks=ref<BackgroundTask[]>([]),error=ref(''),busy=ref(false),loading=ref(false),loaded=ref(false),filter=ref('all');
const visibleTasks=computed(()=>tasks.value.filter(task=>filter.value==='all'||(filter.value==='active'?['queued','running'].includes(task.state):task.state===filter.value)));
const taskFilters=computed(()=>[{id:'all',label:t('全部'),count:tasks.value.length},{id:'active',label:t('进行中'),count:tasks.value.filter(task=>['queued','running'].includes(task.state)).length},{id:'succeeded',label:t('已完成'),count:tasks.value.filter(task=>task.state==='succeeded').length},{id:'failed',label:t('执行失败'),count:tasks.value.filter(task=>task.state==='failed').length}]);
const labels:Record<string,string>={queued:'排队中',running:'执行中',succeeded:'已完成',failed:'执行失败',cancelled:'已取消',speed:'出口测速',quality:'IP 质量检测','import-source':'更新订阅','public-source':'抓取公开来源','public-cycle':'公共代理采集','core-apply':'应用核心配置'};
async function load(){const request=requests.start();loading.value=true;try{const result=await api<{items:BackgroundTask[]}>('','GET',undefined,{signal:request.signal});if(requests.isCurrent(request)){tasks.value=result.items;error.value='';loaded.value=true}}catch(e){if(requests.isCurrent(request)&&!isCancelled(e))error.value=(e as Error).message;}finally{if(requests.isCurrent(request))loading.value=false}}
async function cancel(id:string){busy.value=true;try{await api('/'+id+'/cancel','POST',{});await load();}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
const poll=serialPoll(async()=>{if(!document.hidden&&!busy.value&&!loading.value)await load();},()=>3000);
onMounted(()=>{void load();poll.start();});onUnmounted(()=>{poll.stop();requests.cancel()});
</script>
<template>
  <TablePageLayout :title="t('任务中心')" :description="t('统一查看任务排队、执行结果与失败原因')">
    <template #actions><button :disabled="loading||busy" :aria-busy="loading" @click="load"><RefreshCw :size="16" :class="{spin:loading}"/>{{t('刷新')}}</button></template>
    <template #filters><div class="task-filters" role="group" :aria-label="t('任务状态')"><button v-for="item in taskFilters" :key="item.id" :aria-pressed="filter===item.id" @click="filter=item.id">{{item.label}}<span>{{item.count}}</span></button></div></template>
    <template #notice><p v-if="error" class="error" role="alert">{{t(error)}}</p></template>
    <table class="adaptive-table"><thead><tr><th>{{t('任务类型')}}</th><th>{{t('目标资源')}}</th><th>{{t('状态')}}</th><th>{{t('尝试次数')}}</th><th>{{t('执行结果')}}</th><th>{{t('操作')}}</th></tr></thead>
      <tbody><tr v-for="task in visibleTasks" :key="task.id"><td :data-label="t('任务类型')">{{t(labels[task.kind]||task.kind)}}</td><td :data-label="t('目标资源')"><code>{{task.target||'—'}}</code></td><td :data-label="t('状态')"><span :class="['badge',task.state==='failed'?'danger':task.state==='succeeded'?'success':'neutral']">{{t(labels[task.state]||task.state)}}</span></td><td :data-label="t('尝试次数')">{{task.attempts}}</td><td :data-label="t('执行结果')">{{task.error?t(task.error):task.finished?new Date(task.finished*1000).toLocaleString():'—'}}</td><td :data-label="t('操作')"><button v-if="['queued','running'].includes(task.state)" :disabled="busy" @click="cancel(task.id)"><XCircle :size="14"/>{{t('取消')}}</button></td></tr>
      <tr v-if="!visibleTasks.length"><td colspan="6" class="empty"><ListChecks :size="24"/>{{!loaded&&loading?t('加载中'):t('暂无后台任务')}}</td></tr></tbody>
    </table>
    <template #pagination><span>{{t('最多显示最近 100 项任务')}}</span><span>{{t('无日志模式仅短暂保留结果供页面读取')}}</span></template>
  </TablePageLayout>
</template>

<style scoped>
.task-filters{display:flex;flex-wrap:wrap;gap:8px}.task-filters button{min-height:40px;border-color:var(--border);font-size:13px;background:var(--surface);gap:10px}.task-filters button[aria-pressed=true]{background:var(--accent-soft);border-color:var(--accent);color:var(--accent-text)}.task-filters span{font-size:12px;font-variant-numeric:tabular-nums;opacity:.8}.adaptive-table code{overflow-wrap:anywhere;white-space:normal}@media(max-width:650px){.task-filters{width:100%}.task-filters button{flex:1;min-height:44px}}
</style>
