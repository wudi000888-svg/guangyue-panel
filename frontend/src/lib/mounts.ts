import type {NodeGroup} from '../types';
import type {MountedNode} from './sites';

// A default is only a convenience for NEW selections, never a fallback access
// grant or an instruction to overwrite saved node membership.
export function defaultMountGroups(groups: NodeGroup[]): string[] {
  return groups.some(g => g.id === 'default-subsite' && g.enabled) ? ['default-subsite'] : [];
}

function sameGroups(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((id, index) => id === b[index]);
}

// A mount dialog edits a pool of nodes. Show the common group selection when
// every mounted node currently uses the same groups; mixed assignments are
// represented by an empty selection until the administrator chooses a new
// shared selection.
export function mountGroupSelection(nodes: MountedNode[], fallback: string[] = []): string[] {
  if (!nodes.length) return [...fallback];
  const first = [...(nodes[0].group_ids || [])];
  return nodes.every(node => sameGroups(node.group_ids || [], first)) ? first : [];
}

// Applying the pool selector must update nodes that were already mounted with
// the default group. Previously it only affected nodes selected afterwards,
// making “save and mount” appear to do nothing for an existing pool.
export function applyMountGroups(nodes: MountedNode[], groups: string[]): MountedNode[] {
  return nodes.map(node => ({...node, group_ids: [...groups]}));
}
