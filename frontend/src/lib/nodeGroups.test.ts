import {expect,it} from 'vitest';
import {filterGroupMembers,groupMemberKey,groupSources,userNodeGroups} from './nodeGroups';
import type {User,GroupMember} from '../types';
it('retains exact explicit permissions, including denial, before plan or defaults',()=>{
 const u={} as User;
 expect(userNodeGroups(u)).toEqual(['legacy-private','legacy-public']);
 u.entitlement={group_ids:['plan']} as User['entitlement'];
 expect(userNodeGroups(u)).toEqual(['plan']);
 u.node_group_ids=[];
 expect(userNodeGroups(u)).toEqual([]);
 u.node_group_ids=['child'];
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
