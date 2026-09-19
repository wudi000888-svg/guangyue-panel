import type {User,GroupMember} from '../types';
export const userNodeGroups=(u:User):string[]=>u.node_group_ids??u.entitlement?.group_ids??['legacy-private','legacy-public'];
export const groupMemberKey=(m:GroupMember)=>[m.source||'',m.site_id,m.node_id].join('/');
export function filterGroupMembers(members:GroupMember[],search:string,source='all'):GroupMember[]{
 const query=search.trim().toLowerCase();
 return members.filter(m=>(source==='all'||m.source===source)&&(!query||[m.name,m.protocol,m.site_name,m.site_id,m.entry_host,m.exit_ip,m.node_id].join(' ').toLowerCase().includes(query)));
}
export function groupSources(members:GroupMember[]):string[] {
 return [...new Set(members.map(m=>m.source==='mounted'||m.source==='business'?m.site_name||m.site_id:m.source==='public'?'公共节点':'本站节点'))];
}
