import type { Node,NodeGroup } from '../types';
export type SiteStatus = {
 service_error?:string;version:string;edition:string;role:string;paused:boolean;pending:boolean;control:boolean;
 users:number;nodes:number;online_users:number|null;upload:number;download:number;sampled_at:number;
 live:{upload_rate:number|null;download_rate:number|null;vless:number|null;hy2:number|null};
};
export type SiteTemplate={policy_version?:number;rate_milli?:number;group_ids?:string[];rate_revision?:string;id:string;name:string;exit_id:string;protocol:string;enabled:boolean;reality_sni?:string;dns?:{mode:string;doh?:string;ipv6?:string}};
export type MountedNode={node_id:string;name:string;enabled:boolean;group_ids:string[]};
export type SiteMount={assignment?:'groups'|'manual';catalog:{info:{vless_host:string;hy2_host:string};nodes:Node[];groups:NodeGroup[];revision:string;paused:boolean};nodes:MountedNode[];sequence:number;lease_until:number;last_sync:number;error:string};
export type ManagedSite={
 mount?:SiteMount;
 id:string;name:string;group:string;enabled:boolean;exclusive:boolean;revision:string;last_seen:number;applied:string;desired:string;lease_until:number;error:string;
 connection?:{url:string;site_id:string;scope:string;status?:SiteStatus};
 info?:{edition?:'lite'|'pro';version:string;vless_host:string;hy2_host:string};
 default_nodes?:SiteTemplate[];nodes:SiteTemplate[];grants:{user_id:number;quota:number;budget?:number}[];
 reports?:Node[];commands?:{node_id:string;kind:string;state:string}[];
 usage?:Record<string,{metered?:boolean;quota_upload?:number;quota_download?:number;upload:number;download:number}>;
};
export function siteState(site:ManagedSite,now=Date.now()/1000):string {
 if(site.error)return '连接失败';
 if(site.connection){
  if(!site.last_seen || site.last_seen<now-90)return '待刷新';
  if(site.connection.status?.service_error)return '服务应用失败';
  if(site.connection.status?.pending)return '等待应用';
  return site.connection.status?.paused?'服务已暂停':'已连接';
 }
 if(!site.info)return '等待连接';
 if(!site.enabled)return '已停用';
 if(site.last_seen<now-90)return '连接中断';
 return site.applied!==site.desired?'等待同步':'已同步';
}
