import type {NodeGroup} from '../types';

// A default is only a convenience for NEW selections, never a fallback access
// grant or an instruction to overwrite saved node membership.
export function defaultMountGroups(groups: NodeGroup[]): string[] {
  return groups.some(g => g.id === 'default-subsite' && g.enabled) ? ['default-subsite'] : [];
}
