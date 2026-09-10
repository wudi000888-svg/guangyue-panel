<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { RefreshCw, XCircle, ListChecks } from 'lucide-vue-next';
import { useApi, isCancelled } from '../lib/api';
import { serialPoll } from '../lib/requests';
import type { BackgroundTask } from '../lib/tasks';
import { t } from '../i18n';
import TablePageLayout from '../components/TablePageLayout.vue';
const api=useApi('/tasks');
const tasks=ref<BackgroundTask[]>([]),error=ref(''),busy=ref(false);
const labels:Record<string,string>={queued:'排队中',running:'执行中',succeeded:'已完成',failed:'执行失败',cancelled:'已取消',speed:'出口测速',quality:'IP 质量检测','import-source':'更新订阅','public-source':'抓取公开来源','public-cycle':'公共代理采集','core-apply':'应用核心配置'};
async function load(){try{tasks.value=(await api<{items:BackgroundTask[]}>()).items;error.value='';}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}}
async function cancel(id:string){busy.value=true;try{await api('/'+id+'/cancel','POST',{});await load();}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
const poll=serialPoll(async()=>{if(!document.hidden)await load();},()=>3000);
onMounted(()=>{void load();poll.start();});onUnmounted(()=>poll.stop());
</script>
<template>
  <TablePageLayout :title="t('任务中心')" :description="t('统一查看任务排队、执行结果与失败原因')">
    <template #actions><button @click="load"><RefreshCw :size="16"/>{{t('刷新')}}</button></template>
    <template #notice><p v-if="error" class="error" role="alert">{{t(error)}}</p></template>
    <table class="adaptive-table"><thead><tr><th>{{t('任务类型')}}</th><th>{{t('目标资源')}}</th><th>{{t('状态')}}</th><th>{{t('尝试次数')}}</th><th>{{t('执行结果')}}</th><th>{{t('操作')}}</th></tr></thead>
      <tbody><tr v-for="task in tasks" :key="task.id"><td :data-label="t('任务类型')">{{t(labels[task.kind]||task.kind)}}</td><td :data-label="t('目标资源')"><code>{{task.target||'—'}}</code></td><td :data-label="t('状态')"><span :class="['badge',task.state==='failed'?'danger':task.state==='succeeded'?'success':'neutral']">{{t(labels[task.state]||task.state)}}</span></td><td :data-label="t('尝试次数')">{{task.attempts}}</td><td :data-label="t('执行结果')">{{task.error?t(task.error):task.finished?new Date(task.finished*1000).toLocaleString():'—'}}</td><td :data-label="t('操作')"><button v-if="['queued','running'].includes(task.state)" :disabled="busy" @click="cancel(task.id)"><XCircle :size="14"/>{{t('取消')}}</button></td></tr>
      <tr v-if="!tasks.length"><td colspan="6" class="empty"><ListChecks :size="24"/>{{t('暂无后台任务')}}</td></tr></tbody>
    </table>
    <template #pagination><span>{{t('最多显示最近 100 项任务')}}</span><span>{{t('无日志模式仅短暂保留结果供页面读取')}}</span></template>
  </TablePageLayout>
</template>
