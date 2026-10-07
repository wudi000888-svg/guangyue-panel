<script setup lang="ts">
import {computed,ref} from 'vue';
import type {GroupMember,Node,NodeGroup} from '../types';
import {selectedPlanGroupKeys,unavailablePlanGroupKeys} from './planNodeSelection';
import {t} from '../i18n';
import {planMemberKey as memberKey,planMemberSelected,removePlanGroupNodes} from '../lib/nodeGroups';

const props=defineProps<{
  groups:NodeGroup[];
  members:Record<string,GroupMember[]>;
  nodes:Node[];
  groupIds:string[];
  nodeIds:string[];
  hidePublic?:boolean;
}>();
const emit=defineEmits<{
  (event:'update:groupIds',value:string[]):void;
  (event:'update:nodeIds',value:string[]):void;
}>();

const search=ref<Record<string,string>>({});
const filteredRows=(id:string)=>rowsFor(id).filter(n=>[n.name,n.protocol,n.site_name,n.entry_host,n.exit_ip].join(' ').toLowerCase().includes((search.value[id]||'').toLowerCase()));
function useWholeGroup(id:string){emit('update:nodeIds',removePlanGroupNodes(id,rowsFor(id),props.nodeIds));}
const memberSelected=(member:GroupMember)=>planMemberSelected(member,props.nodeIds);
const visibleGroups=computed(()=>props.groups.filter(group=>!props.hidePublic||group.scope!=='public'));
// /api/node-groups returns the complete effective membership, including
// mounted child-site nodes. Do not discard those rows just because they are
// absent from the local node catalog.
const rowsFor=(groupID:string)=>props.members[groupID]||[];
const groupHasNodes=(groupID:string)=>rowsFor(groupID).length>0;
const groupSelected=(groupID:string)=>props.groupIds.includes(groupID);
const restricted=(groupID:string)=>selectedPlanGroupKeys(groupID,rowsFor(groupID),props.nodeIds).length>0;
const unavailable=(groupID:string)=>unavailablePlanGroupKeys(groupID,rowsFor(groupID),props.nodeIds).length;
const selectedCount=(groupID:string)=>rowsFor(groupID).filter(member=>memberSelected(member)).length;

function updateGroups(groupID:string,checked:boolean){
  const next=checked?[...props.groupIds,groupID]:props.groupIds.filter(id=>id!==groupID);
  emit('update:groupIds',[...new Set(next)]);
  if(!checked)emit('update:nodeIds',removePlanGroupNodes(groupID,rowsFor(groupID),props.nodeIds));
}
function updateNode(member:GroupMember,checked:boolean){
  const nodeID=memberKey(member);
  const current=props.nodeIds.filter(id=>id!==nodeID&&(['mounted','business'].includes(member.source||'')||id!==member.node_id));
  const next=checked?[...current,nodeID]:current;
  emit('update:nodeIds',[...new Set(next)]);
}
</script>

<template>
  <div class="plan-node-selector">
    <div class="selector-heading">
      <div><strong>{{t('节点权限')}}</strong><small>{{t('先选择节点组；未勾选单独节点时使用整组，勾选后仅使用本组所选节点。')}}</small></div>
      <span class="selection-count">{{groupIds.length}} {{t('个组')}} · {{nodeIds.length}} {{t('个单独节点')}}</span>
    </div>
    <fieldset class="group-list">
      <legend>{{t('套餐节点组')}}</legend>
      <div v-for="group in visibleGroups" :key="group.id" class="group-block">
        <label class="group-option">
          <input type="checkbox" :checked="groupSelected(group.id)" @change="updateGroups(group.id,($event.target as HTMLInputElement).checked)"/>
          <span><strong>{{group.name}}</strong><small>{{rowsFor(group.id).length}} {{t('个节点')}}<template v-if="group.scope==='subsite'"> · {{t('子站节点组')}}</template><template v-if="!group.enabled"> · {{t('已停用')}}</template></small></span>
        </label>
        <div v-if="groupSelected(group.id)" class="group-authorization"><strong>{{restricted(group.id)?t('仅授权本组所选节点'):t('整组授权，包含后续新增节点')}}</strong><span>{{restricted(group.id)?selectedCount(group.id):rowsFor(group.id).length}} {{t('个节点')}}</span></div><details v-if="groupSelected(group.id)" :open="restricted(group.id)" class="group-customize"><summary>{{t('按需选择组内节点')}}</summary><div class="group-node-list"><div class="node-selection-tools"><input v-model="search[group.id]" type="search" :placeholder="t('搜索组内节点')" :aria-label="t('搜索组内节点')"/><button v-if="restricted(group.id)" type="button" @click="useWholeGroup(group.id)">{{t('恢复整组授权')}}</button></div>
          <p v-if="unavailable(group.id)" class="group-selection-note">{{unavailable(group.id)}} {{t('个已选节点暂不在目录中，原有选择仍保留。')}}</p>
          <template v-if="groupHasNodes(group.id)">
            <label v-for="member in filteredRows(group.id)" :key="memberKey(member)" class="node-option">
              <input type="checkbox" :checked="memberSelected(member)" @change="updateNode(member,($event.target as HTMLInputElement).checked)"/>
              <span><strong>{{member.name||member.node_id}}</strong><small>{{(member.protocol||'').toUpperCase()}} · {{member.entry_host||member.exit_ip||member.node_id}}<template v-if="member.source&&member.source!=='local'"> · {{member.site_name||member.source}}</template></small></span>
            </label>
          </template>
          <p v-else class="field-help">{{t('此组暂无可单独选择的可用节点。')}}</p>
          <p v-if="groupHasNodes(group.id)&&!filteredRows(group.id).length" class="field-help">{{t('没有匹配的节点，请调整搜索。')}}</p>
          <small v-if="groupHasNodes(group.id)" class="group-selection-summary">{{selectedCount(group.id)}} / {{rowsFor(group.id).length}} {{t('个节点已单独选择')}}</small><p class="group-selection-note">{{t('不勾选单独节点时使用整组；勾选后仅授权本组所选节点。')}}</p>
        </div></details>
      </div>
      <p v-if="!visibleGroups.length" class="field-help">{{t('节点组加载失败，请刷新后重试。')}}</p>
    </fieldset>
  </div>
</template>

<style scoped>
.plan-node-selector{display:flex;flex-direction:column;gap:12px}.selector-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:14px}.selector-heading strong,.selector-heading small{display:block}.selector-heading strong{font-size:13px}.selector-heading small,.selection-count,.group-option small,.node-option small,.group-selection-summary{font-size:12px;color:var(--muted)}.selector-heading small{margin-top:5px;line-height:1.5}.selection-count{white-space:nowrap}.group-list{border-radius:10px;display:flex;flex-direction:column;max-height:540px;overflow:auto;min-width:0}.group-list legend{color:var(--muted)}.group-block{border-bottom:1px solid var(--border);padding-bottom:12px}.group-block:last-child{border-bottom:0;padding-bottom:0}.group-option,.node-option{display:flex;flex-direction:row;gap:9px;margin:0;font-size:13px;overflow-wrap:anywhere}.group-option input,.node-option input{width:16px;height:16px;margin:2px 0 0;flex-shrink:0}.group-option strong,.node-option strong{font-weight:550}.group-option small,.node-option small{display:block;margin-top:4px}.group-node-list{border-radius:8px;background:var(--surface-hover);display:flex;flex-direction:column;gap:10px}.group-selection-summary{display:block;margin-left:25px;margin-top:2px}@media(max-width:600px){.selector-heading{display:block}.selection-count{display:block;margin-top:6px}.group-node-list{margin-left:12px}}
.group-block{padding:14px;border:1px solid var(--border);border-radius:10px;background:var(--surface)}.group-block:last-child{border:1px solid var(--border);padding:14px}.group-list{padding:0;border:0;gap:10px}.group-list legend{padding:0 0 12px;font-size:13px}.group-option{min-height:44px;align-items:center}.group-authorization{display:flex;gap:10px;justify-content:space-between;margin:12px 0 0;padding:10px 12px;background:var(--accent-soft);border-radius:8px;color:var(--accent);font-size:12px}.group-authorization strong{font-weight:500}.group-customize summary{padding:12px 0;min-height:44px;align-content:center;font-size:13px;color:var(--secondary)}.group-node-list{margin:0;padding:12px}.node-selection-tools{display:flex;gap:8px;flex-wrap:wrap}.node-selection-tools input{min-width:0;flex:1;min-height:44px}.node-selection-tools button{min-height:44px;font-size:12px}.node-option{align-items:center;min-height:44px}.group-selection-note{margin:0;color:var(--secondary);font-size:12px;line-height:1.7}
</style>
