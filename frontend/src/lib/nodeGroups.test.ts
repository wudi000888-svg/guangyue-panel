import {expect,it} from 'vitest';
import {filterGroupMembers,groupMemberKey,groupSources,userNodeGroups,planMemberSelected,removePlanGroupNodes} from './nodeGroups';
import type {User,GroupMember} from '../types';
it('uses plan groups even when legacy individual overrides exist',()=>{
 const u={} as User;
 expect(userNodeGroups(u)).toEqual(['legacy-private','legacy-public']);
 u.entitlement={group_ids:['plan']} as User['entitlement'];
 expect(userNodeGroups(u)).toEqual(['plan']);
 u.node_group_ids=[];
 expect(userNodeGroups(u)).toEqual(['plan']);
 u.node_group_ids=['child'];
 expect(userNodeGroups(u)).toEqual(['plan']);
 u.entitlement!.group_ids=[];
 expect(userNodeGroups(u)).toEqual([]);
 delete u.entitlement;
 expect(userNodeGroups(u)).toEqual(['child']);
});
it('distinguishes repeated node names and IDs across sites and protocols',()=>{
 const members:GroupMember[]=[
  {site_id:'main',site_name:'Main',source:'local',node_id:'vless-main',name:'Tokyo',protocol:'vless',entry_host:'main.example.com'},
  {site_id:'child',site_name:'Child Tokyo',source:'mounted',node_id:'vless-main',name:'Tokyo',protocol:'vless',entry_host:'child.example.com',exit_ip:'192.0.2.1'},
  {site_id:'child',site_name:'Child Tokyo',source:'mounted',node_id:'hy2-main',name:'Tokyo',protocol:'hy2',entry_host:'hy.example.com',exit_ip:'192.0.2.1'},
 ];
 expect(new Set(members.map(groupMemberKey)).size).toBe(3);
 expect(filterGroupMembers(members,'VLESS','mounted')).toEqual([members[1]]);
 expect(filterGroupMembers(members,'192.0.2.1')).toEqual(members.slice(1));
 expect(filterGroupMembers(members,'child.example.com')).toEqual([members[1]]);
 expect(filterGroupMembers(members,'Child Tokyo')).toEqual(members.slice(1));
 expect(groupSources(members)).toEqual(['本站节点','Child Tokyo']);
});

it('preserves unrelated and unavailable plan selections when removing a group',()=>{
 const child:GroupMember={site_id:'child',source:'mounted',node_id:'vless-main',selection_key:'default-subsite/child/vless-main',name:'Child',protocol:'vless'};
 expect(planMemberSelected(child,['legacy-private/vless-main'])).toBe(false);
 expect(planMemberSelected(child,['vless-main'])).toBe(false);
 expect(planMemberSelected(child,[child.selection_key!])).toBe(true);
 const selected=['legacy-private/temporarily-unavailable',child.selection_key!,'legacy-public/public-node'];
 expect(removePlanGroupNodes('default-subsite',[child],selected)).toEqual([selected[0],selected[2]]);
 expect(removePlanGroupNodes('legacy-private',[],selected)).toEqual(selected.slice(1));
});
