export type VersionOperation={action:'update'|'rollback';version:string;expected_version:string;request_id:string;stage:string;error?:string;started_at?:number};
export type Release={version:string;name:string;notes:string;url:string;published_at:string};
export type UpdateState={available:boolean;current_version:string;installed_version?:string;checked_at?:number;releases?:Release[];rollback_versions?:{version:string;created:number;compatible:boolean}[];operation?:VersionOperation;error?:string;warning?:string;catalog_error?:string;catalog_stale?:boolean;refresh_seconds?:number};
export const activeStages=new Set(['queued','downloading','verifying','backing_up','installing','restarting','recovering']);
const versionPattern=/^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$/;
// Older children reported catalogue failures only through their warning field.
export function catalogError(state:UpdateState|undefined):string{return state?.catalog_error||(state?.warning?.includes('版本来源暂不可用')?state.warning:'')||'';}
export function newerVersion(a:string,b:string):boolean {if(!versionPattern.test(a)||!versionPattern.test(b))return false;const x=a.split('.').map(Number),y=b.split('.').map(Number);for(let i=0;i<3;i++){if(x[i]!==y[i])return x[i]>y[i];}return false;}
export function usableReleases(state:UpdateState|undefined,current=state?.current_version||''):Release[]{return (state?.releases||[]).filter(r=>newerVersion(r.version,current)).sort((a,b)=>newerVersion(a.version,b.version)?-1:newerVersion(b.version,a.version)?1:0);}
export function freshCatalog(state:UpdateState|undefined,now=Date.now()/1000):boolean{return !!state?.available&&versionPattern.test(state.current_version)&&!!state.checked_at&&!state.error&&!catalogError(state)&&!state.catalog_stale&&state.checked_at>=now-900&&state.checked_at<=now+60;}
export function releaseStatusMessage(state:UpdateState|undefined,now=Date.now()/1000):string {
 if(state?.error)return state.error;
 if(catalogError(state))return catalogError(state);
 if(!state?.available)return '此部署尚未启用在线更新服务';
 if(!state.checked_at)return '尚未检查正式发布版本，请检查更新';
 if(!freshCatalog(state,now))return '版本列表已过期，请重新检查更新';
 return usableReleases(state).length?'有新版本可用！':'已是最新版本';
}
export function updateOutcome(pending:VersionOperation,operation:VersionOperation|undefined,healthVersion?:string):'waiting'|'success'|'failed' {
 if(operation?.request_id===pending.request_id){if(operation.stage==='failed')return 'failed';if(operation.stage==='succeeded'&&healthVersion===pending.version)return 'success';}
 // A version predating the updater has no status endpoint after a successful rollback.
 if(!operation&&pending.action==='rollback'&&healthVersion===pending.version)return 'success';
 return 'waiting';
}
