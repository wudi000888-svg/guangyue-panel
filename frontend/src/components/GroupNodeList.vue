<script setup lang="ts">
import type {GroupMember} from '../types';
import {groupMemberKey} from '../lib/nodeGroups';
import {t} from '../i18n';
defineProps<{members:GroupMember[]}>();
</script>
<template>
<ul class="group-node-list"><li v-for="m in members" :key="groupMemberKey(m)">
 <div class="node-identity"><span :class="['tag',m.protocol]">{{m.protocol.toUpperCase()}}</span><strong>{{m.name}}</strong><span class="badge" :class="m.status==='disabled'?'neutral':m.status==='pending'?'warning':'success'">{{t(m.status==='disabled'?'已停用':m.status==='pending'?'等待同步':'已启用')}}</span></div>
 <div class="node-origin"><span>{{t(m.source==='mounted'?'子站挂载':m.source==='public'?'公共节点':m.source==='business'?'业务站':'本站节点')}}</span><span>{{m.site_name||m.site_id}}</span></div>
 <dl><div><dt>{{t('接入地址')}}</dt><dd>{{m.entry_host||'—'}}{{m.entry_host?':'+(m.entry_port||443):''}}</dd></div><div><dt>{{t('出口 IP')}}</dt><dd>{{m.exit_ip||t('待检测')}}</dd></div></dl><small class="node-id">{{m.node_id}}</small>
</li></ul>
</template>
<style scoped>
.group-node-list{list-style:none;padding:0;margin:0;display:grid;gap:10px;max-height:330px;overflow:auto}.group-node-list li{padding:12px;border:1px solid var(--border);border-radius:9px;background:var(--surface-hover);min-width:0}.node-identity{display:flex;gap:8px;align-items:center;flex-wrap:wrap;font-size:13px}.node-identity strong{flex:1;overflow-wrap:anywhere}.node-origin{display:flex;gap:8px;flex-wrap:wrap;margin:9px 0;font-size:12px;color:var(--muted)}.node-origin>span:first-child{color:var(--accent-text)}dl{font-size:12px;margin:0;display:grid;gap:6px}dl>div{display:flex;gap:12px}dt{color:var(--muted);flex-shrink:0}dd{margin:0;overflow-wrap:anywhere}.node-id{display:block;font-size:10px;color:var(--muted);margin-top:8px;overflow-wrap:anywhere}
</style>
