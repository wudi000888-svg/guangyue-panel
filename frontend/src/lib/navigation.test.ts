import { describe, expect, it } from 'vitest';
import { groupNavigation, searchNavigation, sidebarNavigation } from './navigation';

describe('shell navigation', () => {
  it('hides optional shortcuts without losing search or directory entries', () => {
    const permitted = ['subscription', 'shop', 'orders', 'wallet', 'clients', 'settings'].map(id => ({id, label:id}));
    expect(sidebarNavigation(permitted, undefined, false).map(item => item.id)).toEqual(['subscription', 'shop', 'orders', 'settings']);
    expect(sidebarNavigation(permitted, [], false).map(item => item.id)).toEqual(['settings']);
    expect(sidebarNavigation(permitted, ['wallet', 'nodes', 'settings', 'wallet'], false).map(item => item.id)).toEqual(['wallet', 'settings']);
    expect(groupNavigation(permitted, false).flatMap(group => group.items)).toHaveLength(6);
    expect(searchNavigation(permitted.map(item => ({...item,group:'',description:''})), 'clients')[0]?.id).toBe('clients');
  });
  it('keeps every permitted route once, including future routes, without adding permissions', () => {
    const items = ['subscription', 'shop', 'nodes', 'fleet', 'future-feature'].map(id => ({ id, label: id }));
    for (const administrator of [true, false]) {
      const actual = groupNavigation(items, administrator).flatMap(group => group.items.map(item => item.id));
      expect(actual.sort()).toEqual(items.map(item => item.id).sort());
      expect(new Set(actual).size).toBe(actual.length);
    }
  });

  it('places mounted nodes immediately after local nodes', () => {
    const items = ['ips', 'subsite-nodes', 'nodes'].map(id => ({ id, label: id }));
    expect(groupNavigation(items, true)[0]?.items.map(item => item.id)).toEqual(['nodes', 'subsite-nodes', 'ips']);
  });

  it('searches translated labels, groups, descriptions and route names without empty-query loss', () => {
    const items = [
      { id: 'nodes', label: '本地节点', group: '节点与站点', description: '管理接入节点' },
      { id: 'subsite-nodes', label: '子站节点', group: '节点与站点', description: '直接挂载子站' },
    ];
    expect(searchNavigation(items, '子站 节点').map(item => item.id)).toEqual(['subsite-nodes']);
    expect(searchNavigation(items, ' SUBSITE ').map(item => item.id)).toEqual(['subsite-nodes']);
    expect(searchNavigation(items, 'missing')).toEqual([]);
    expect(searchNavigation(items, '  ')).toEqual(items);
  });
});
