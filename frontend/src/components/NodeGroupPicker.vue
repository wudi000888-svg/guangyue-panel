<script setup lang="ts">
import type {NodeGroup} from '../types';
import {t} from '../i18n';
const model=defineModel<string[]>({default:()=>[]});
defineProps<{groups:NodeGroup[];scope?:'private'|'public';hidePublic?:boolean}>();
</script>
<template>
<div class="group-picker">
 <fieldset v-for="kind in (scope?[scope]:hidePublic?['private']:['private','public'])" :key="kind"><legend>{{kind==='public'?t('公共节点组'):t('普通节点组')}}</legend>
 <label v-for="g in groups.filter(g=>g.scope===kind)" :key="g.id"><input type="checkbox" v-model="model" :value="g.id"/><span>{{g.name}}<small v-if="!g.enabled"> · {{t('已停用')}}</small></span></label>
 <p v-if="!groups.some(g=>g.scope===kind)" class="field-help">{{t('暂无节点权限组，请先在套餐管理中创建。')}}</p>
 </fieldset>
 <small v-if="hidePublic&&groups.some(g=>g.scope==='public'&&model.includes(g.id))" class="field-help">{{t('已有公共组授权已保留，专业模式下可编辑。')}}</small>
</div>
</template>
<style scoped>
.group-picker{display:flex;flex-direction:column;gap:12px}.group-picker fieldset{border:1px solid var(--border);border-radius:10px;padding:12px 14px;display:flex;flex-direction:column;gap:10px;max-height:220px;overflow:auto;min-width:0}.group-picker legend{font-size:12px;color:var(--muted);padding:0 5px}.group-picker label{display:flex;flex-direction:row;align-items:center;gap:9px;margin:0;font-size:13px;overflow-wrap:anywhere}.group-picker input{width:16px;height:16px;margin:0;flex-shrink:0}.group-picker small{color:var(--muted)}
</style>
