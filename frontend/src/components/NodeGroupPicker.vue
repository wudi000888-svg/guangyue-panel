<script setup lang="ts">
import {computed,inject} from 'vue';
import type {NodeGroup,GroupMember} from '../types';
import {panelKey} from '../composables/panelContext';
import {groupSources} from '../lib/nodeGroups';
import GroupNodeList from './GroupNodeList.vue';
import {t} from '../i18n';
const model=defineModel<string[]>({default:()=>[]});
const props=defineProps<{groups:NodeGroup[];members?:Record<string,GroupMember[]>;scope?:'private'|'public';hidePublic?:boolean}>();
const panel=inject(panelKey,null),rows=computed(()=>props.members??panel?.groupMembers.value??{});
</script>
<template>
<div class="group-picker"><fieldset><legend>{{t('节点组')}}</legend>
 <div v-for="g in groups" :key="g.id" class="group-option"><label><input type="checkbox" v-model="model" :value="g.id"/><span><strong>{{g.name}}</strong><small> · {{rows[g.id]?.length||0}} {{t('个节点')}}<template v-if="!g.enabled"> · {{t('已停用')}}</template></small><small class="sources">{{groupSources(rows[g.id]||[]).map(s=>t(s)).join(' · ')||t('此组暂无节点')}}</small></span></label><details v-if="rows[g.id]?.length"><summary>{{t('查看组内节点')}}</summary><GroupNodeList :members="rows[g.id]"/></details></div>
 <p v-if="!groups.length" class="field-help">{{t('暂无节点权限组，请先在套餐管理中创建。')}}</p>
 </fieldset></div>
</template>
<style scoped>
.group-picker{display:flex;flex-direction:column;gap:12px}.group-picker fieldset{border:1px solid var(--border);border-radius:10px;padding:12px 14px;display:flex;flex-direction:column;gap:12px;max-height:340px;overflow:auto;min-width:0}.group-picker legend{font-size:12px;color:var(--muted);padding:0 5px}.group-picker label{display:flex;flex-direction:row;align-items:flex-start;gap:9px;margin:0;font-size:13px;overflow-wrap:anywhere}.group-picker input{width:16px;height:16px;margin:2px 0 0;flex-shrink:0}.group-picker strong{font-weight:500}.group-picker small{color:var(--muted);font-size:11px}.sources{display:block;margin-top:5px;line-height:1.5}.group-option details{margin:8px 0 0 25px;font-size:12px}.group-option summary{cursor:pointer;color:var(--accent-text);margin-bottom:8px}
</style>
