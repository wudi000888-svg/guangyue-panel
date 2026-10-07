export interface NavigationItem { id: string; label: string }

export const DEFAULT_ADMIN_SIDEBAR = ['overview', 'users', 'plans', 'nodes', 'fleet', 'settings'];
export const DEFAULT_MEMBER_SIDEBAR = ['subscription', 'shop', 'orders', 'settings'];
export const MEMBER_NAVIGATION: NavigationItem[] = [
  { id: 'subscription', label: '我的服务' }, { id: 'shop', label: '选购套餐' },
  { id: 'orders', label: '我的订单' }, { id: 'wallet', label: '我的钱包' },
  { id: 'clients', label: '使用指南' }, { id: 'tickets', label: '联系支持' },
  { id: 'messages', label: '消息通知' }, { id: 'settings', label: '个人设置' },
];
export const MEMBER_NAVIGATION_LABELS = Object.fromEntries(MEMBER_NAVIGATION.map(item => [item.id, item.label]));

/** Display preferences never determine route access or remove search entries. */
export function sidebarNavigation<T extends NavigationItem>(items: T[], configured: readonly string[] | undefined, administrator: boolean): T[] {
  const selected = new Set(configured ?? (administrator ? DEFAULT_ADMIN_SIDEBAR : DEFAULT_MEMBER_SIDEBAR));
  selected.add('settings');
  return items.filter(item => selected.has(item.id));
}

// Regroup only routes already admitted by the panel's permission checks.
// Unknown routes remain reachable when future features are added.
export function groupNavigation<T extends NavigationItem>(items: T[], administrator: boolean) {
  const sections = administrator ? [
    { id: 'operations', label: '运营管理', routes: ['overview', 'monitor', 'users', 'plans', 'shop', 'subscription', 'wallet', 'orders', 'redeem-codes'] },
    { id: 'resources', label: '节点与站点', routes: ['nodes', 'subsite-nodes', 'node-groups', 'ips', 'public', 'public-nodes', 'public-subscription', 'fleet', 'pairing'] },
    { id: 'support', label: '服务与支持', routes: ['tickets', 'messages', 'clients'] },
    { id: 'system', label: '系统管理', routes: ['tasks', 'system', 'settings'] },
  ] : [
    { id: 'service', label: '我的服务', routes: ['subscription', 'shop', 'overview'] },
    { id: 'account', label: '订单与钱包', routes: ['orders', 'wallet'] },
    { id: 'support', label: '帮助与支持', routes: ['clients', 'tickets', 'messages'] },
    { id: 'preferences', label: '设置与更多', routes: ['settings'] },
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
