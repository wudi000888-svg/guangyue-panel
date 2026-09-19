import type {NodeGroup} from '../types';

// A default is only a convenience for NEW selections, never a fallback access
// grant or an instruction to overwrite saved node membership.
export function defaultMountGroups(groups: NodeGroup[]): string[] {
  return groups.some(g => g.id === 'default-subsite' && g.enabled) ? ['default-subsite'] : [];
}
export type MountGrantDraft = {user_id:number;budget:number;quota:number;original_budget:number};
export function mountGrantPayload(grants: MountGrantDraft[]) {
  return grants.map(g => ({user_id:g.user_id,budget:g.budget,quota:g.budget===g.original_budget?g.quota:0}));
}
