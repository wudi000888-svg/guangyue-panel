export interface NavigationItem { id: string; label: string }

// Regroup only routes already admitted by the panel's permission checks.
// Unknown routes remain reachable when future features are added.
export function groupNavigation<T extends NavigationItem>(items: T[], administrator: boolean) {
  const sections = administrator ? [
    { id: 'workspace', label: '工作台', routes: ['overview', 'monitor', 'tasks', 'clients'] },
    { id: 'resources', label: '节点与站点', routes: ['nodes', 'subsite-nodes', 'node-groups', 'ips', 'public', 'public-nodes', 'public-subscription', 'fleet', 'pairing'] },
    { id: 'access', label: '用户与运营', routes: ['users', 'plans', 'shop', 'subscription', 'wallet', 'orders', 'redeem-codes'] },
    { id: 'system', label: '系统与支持', routes: ['tickets', 'messages', 'system', 'settings'] },
  ] : [
    { id: 'service', label: '我的服务', routes: ['subscription', 'shop', 'clients', 'overview'] },
    { id: 'account', label: '账户与支持', routes: ['wallet', 'orders', 'tickets', 'messages'] },
  ];
  const byId = new Map(items.map(item => [item.id, item]));
  const groups = sections.map(section => ({
    id: section.id, label: section.label,
    items: section.routes.flatMap(id => { const item = byId.get(id); byId.delete(id); return item ? [item] : []; }),
  })).filter(group => group.items.length);
  if (byId.size) groups.push({ id: 'other', label: '系统与服务', items: [...byId.values()] });
  return groups;
}

export function searchNavigation<T extends NavigationItem & { group: string; description: string }>(items: T[], query: string): T[] {
  const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return items;
  return items.filter(item => {
    const searchable = `${item.label} ${item.group} ${item.description} ${item.id}`.toLocaleLowerCase();
    return words.every(word => searchable.includes(word));
  }).sort((a, b) => Number(b.label.toLocaleLowerCase().includes(words.join(' '))) - Number(a.label.toLocaleLowerCase().includes(words.join(' '))));
}
