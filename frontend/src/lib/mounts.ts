import type {NodeGroup} from '../types';
import type {MountedNode} from './sites';

// A default is only a convenience for NEW selections, never a fallback access
// grant or an instruction to overwrite saved node membership.
export function defaultMountGroups(groups: NodeGroup[]): string[] {
  return groups.some(g => g.id === 'default-subsite' && g.enabled) ? ['default-subsite'] : [];
}
// Prefer the dedicated child group instead of preselecting every overlapping
// group, which could also authorize unrelated local or public nodes.
export function mountAssignmentGroups(nodes: MountedNode[], groups: NodeGroup[]): string[] {
  const active=nodes.filter(n=>n.enabled),enabled=new Set(groups.filter(g=>g.enabled).map(g=>g.id));
  const selected=defaultMountGroups(groups).filter(id=>active.some(n=>n.group_ids.includes(id)));
  for(const node of active){
    if(node.group_ids.some(id=>selected.includes(id)))continue;
    const id=node.group_ids.find(id=>enabled.has(id));
    if(id)selected.push(id);
  }
  return selected;
}
export type MountGrantDraft = {user_id:number;budget:number;quota:number;original_budget:number};
export function mountGrantPayload(grants: MountGrantDraft[]) {
  return grants.map(g => ({user_id:g.user_id,budget:g.budget,quota:g.budget===g.original_budget?g.quota:0}));
}
