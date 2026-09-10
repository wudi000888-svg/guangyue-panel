export type VersionOperation={action:'update'|'rollback';version:string;expected_version:string;request_id:string;stage:string;error?:string;started_at?:number};
export type Release={version:string;name:string;notes:string;url:string;published_at:string};
export type UpdateState={available:boolean;current_version:string;installed_version?:string;checked_at?:number;releases?:Release[];rollback_versions?:{version:string;created:number;compatible:boolean}[];operation?:VersionOperation;error?:string;warning?:string;refresh_seconds?:number};
export const activeStages=new Set(['queued','downloading','verifying','backing_up','installing','restarting','recovering']);
export function newerVersion(a:string,b:string):boolean {const x=a.split('.').map(Number),y=b.split('.').map(Number);for(let i=0;i<3;i++){if(x[i]!==y[i])return x[i]>y[i];}return false;}
export function updateOutcome(pending:VersionOperation,operation:VersionOperation|undefined,healthVersion?:string):'waiting'|'success'|'failed' {
 if(operation?.request_id===pending.request_id){if(operation.stage==='failed')return 'failed';if(operation.stage==='succeeded'&&healthVersion===pending.version)return 'success';}
 // A version predating the updater has no status endpoint after a successful rollback.
 if(!operation&&pending.action==='rollback'&&healthVersion===pending.version)return 'success';
 return 'waiting';
}
