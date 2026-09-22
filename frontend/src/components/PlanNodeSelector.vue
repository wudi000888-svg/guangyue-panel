<script setup lang="ts">
import {computed} from 'vue';
import type {GroupMember,Node,NodeGroup} from '../types';
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

const memberSelected=(member:GroupMember)=>planMemberSelected(member,props.nodeIds);
const visibleGroups=computed(()=>props.groups.filter(group=>!props.hidePublic||group.scope!=='public'));
// /api/node-groups returns the complete effective membership, including
// mounted child-site nodes. Do not discard those rows just because they are
// absent from the local node catalog.
const rowsFor=(groupID:string)=>props.members[groupID]||[];
const groupHasNodes=(groupID:string)=>rowsFor(groupID).length>0;
const groupSelected=(groupID:string)=>props.groupIds.includes(groupID);
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
        <div v-if="groupSelected(group.id)" class="group-node-list">
          <template v-if="groupHasNodes(group.id)">
            <label v-for="member in rowsFor(group.id)" :key="memberKey(member)" class="node-option">
              <input type="checkbox" :checked="memberSelected(member)" @change="updateNode(member,($event.target as HTMLInputElement).checked)"/>
              <span><strong>{{member.name||member.node_id}}</strong><small>{{(member.protocol||'').toUpperCase()}} · {{member.entry_host||member.exit_ip||member.node_id}}<template v-if="member.source&&member.source!=='local'"> · {{member.site_name||member.source}}</template></small></span>
            </label>
          </template>
          <p v-else class="field-help">{{t('此组暂无可单独选择的可用节点。')}}</p>
          <small v-if="groupHasNodes(group.id)" class="group-selection-summary">{{selectedCount(group.id)}} / {{rowsFor(group.id).length}} {{t('个节点已单独选择')}}</small>
        </div>
      </div>
      <p v-if="!visibleGroups.length" class="field-help">{{t('节点组加载失败，请刷新后重试。')}}</p>
    </fieldset>
  </div>
</template>

<style scoped>
.plan-node-selector{display:flex;flex-direction:column;gap:12px}.selector-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:14px}.selector-heading strong,.selector-heading small{display:block}.selector-heading strong{font-size:13px}.selector-heading small,.selection-count,.group-option small,.node-option small,.group-selection-summary{font-size:11px;color:var(--muted)}.selector-heading small{margin-top:5px;line-height:1.5}.selection-count{white-space:nowrap}.group-list{border:1px solid var(--border);border-radius:10px;padding:12px 14px;display:flex;flex-direction:column;gap:12px;max-height:430px;overflow:auto;min-width:0}.group-list legend{font-size:12px;color:var(--muted);padding:0 5px}.group-block{border-bottom:1px solid var(--border);padding-bottom:12px}.group-block:last-child{border-bottom:0;padding-bottom:0}.group-option,.node-option{display:flex;flex-direction:row;align-items:flex-start;gap:9px;margin:0;font-size:13px;overflow-wrap:anywhere}.group-option input,.node-option input{width:16px;height:16px;margin:2px 0 0;flex-shrink:0}.group-option strong,.node-option strong{font-weight:550}.group-option small,.node-option small{display:block;margin-top:4px}.group-node-list{margin:10px 0 0 25px;padding:10px 12px;border-radius:8px;background:var(--surface-hover);display:flex;flex-direction:column;gap:10px}.group-selection-summary{display:block;margin-left:25px;margin-top:2px}@media(max-width:600px){.selector-heading{display:block}.selection-count{display:block;margin-top:6px}.group-node-list{margin-left:12px}}
</style>
