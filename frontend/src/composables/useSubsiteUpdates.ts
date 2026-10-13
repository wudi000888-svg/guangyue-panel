import { onScopeDispose, reactive } from 'vue';
import { ApiError, isCancelled } from '../lib/api';
import { serialPoll } from '../lib/requests';
import { activeStages, catalogError, freshCatalog, usableReleases, type UpdateState, type VersionOperation } from '../lib/updater';

type UpdateAPI = <T>(path: string, method?: string, body?: unknown, options?: RequestInit) => Promise<T>;
type Options = { api: UpdateAPI; visible?: () => boolean; notify?: (id: string, operation: VersionOperation) => void; onVersion?: (id: string, version: string) => void };

// The child owns installation and health checks. The controller only checks
// official releases, submits a CAS operation, and reconciles that exact receipt.
export function useSubsiteUpdates(options: Options) {
 const states=reactive<Record<string,UpdateState|undefined>>({});
 const busy=reactive<Record<string,boolean>>({});
 const errors=reactive<Record<string,string>>({});
 const updateChoice=reactive<Record<string,string>>({});
 const rollbackChoice=reactive<Record<string,string>>({});
 const pending=reactive<Record<string,VersionOperation|undefined>>({});
 const uncertain=reactive<Record<string,boolean>>({}),stale=reactive<Record<string,boolean>>({});
 const opened=new Set<string>(),requests=new Map<string,AbortController>(),lastRead=new Map<string,number>();
 let disposed=false;
 const running=(id:string)=>!!pending[id]||activeStages.has(states[id]?.operation?.stage||'');
 function receive(id:string,state:UpdateState){
  states[id]=state;lastRead.set(id,Date.now());
  const releases=usableReleases(state);
  if(!releases.some(r=>r.version===updateChoice[id]))updateChoice[id]=releases[0]?.version||'';
  if(!state.rollback_versions?.some(r=>r.version===rollbackChoice[id]&&r.compatible))rollbackChoice[id]=state.rollback_versions?.find(r=>r.compatible)?.version||'';
  const operation=state.operation,receipt=pending[id];
  if(!receipt&&operation&&activeStages.has(operation.stage))pending[id]={...operation};
  if(!receipt&&operation?.stage==='failed')errors[id]=operation.error||'版本操作失败';
  if(receipt&&operation?.request_id===receipt.request_id){
   delete uncertain[id];
   pending[id]={...receipt,...operation};
   if(operation.stage==='failed'){errors[id]=operation.error||'版本操作失败';delete pending[id];}
   else if(operation.stage==='succeeded'&&state.current_version===receipt.version){
    delete pending[id];options.onVersion?.(id,state.current_version);options.notify?.(id,operation);
   }
  }else if(!receipt&&!activeStages.has(operation?.stage||'')&&state.current_version)options.onVersion?.(id,state.current_version);
  if(pending[id]&&Date.now()-(pending[id]?.started_at||Date.now()/1000)*1000>600000)errors[id]='等待时间较长，请检查服务器运行状态。页面会继续重试。';
 }
 async function check(id:string,force=true){
  if(disposed||busy[id])return;
  const request=new AbortController(),timeout=AbortSignal.timeout(40000);requests.set(id,request);busy[id]=true;errors[id]='';
  try{
   // POST /check refreshes GitHub; GET only reads the child's local cache.
   // During a running operation, avoid catalogue work and poll its receipt.
   const refresh=force&&!running(id);
   const state=await options.api<UpdateState>('/'+id+'/updates'+(refresh?'/check':''),refresh?'POST':'GET',refresh?{}:undefined,{signal:AbortSignal.any([request.signal,timeout])});
   if(disposed||request.signal.aborted||requests.get(id)!==request)return;
   stale[id]=false;receive(id,state);
   if(state.error||catalogError(state))errors[id]=state.error||catalogError(state);
  }catch(error){
   if(!disposed&&!request.signal.aborted&&requests.get(id)===request){
    stale[id]=true;if(states[id])states[id]={...states[id]!,catalog_stale:true};
    if(timeout.aborted||!isCancelled(error))errors[id]=timeout.aborted?'版本检查超时，请重试':running(id)?'连接暂时中断，正在确认更新状态':(error as Error).message;
   }
  }finally{if(requests.get(id)===request){requests.delete(id);delete busy[id];}}
 }
 function canApply(id:string,action:'update'|'rollback',version:string):boolean{
  const state=states[id];
  if(!state?.available||!state.current_version||state.error||stale[id]||busy[id]||running(id)||!version||version===state.current_version)return false;
  return action==='update'?freshCatalog(state)&&usableReleases(state).some(r=>r.version===version):!!state.rollback_versions?.some(r=>r.version===version&&r.compatible);
 }
 async function apply(id:string,action:'update'|'rollback',version:string){
  if(!canApply(id,action,version)||disposed)return false;
  const receipt:VersionOperation={action,version,expected_version:states[id]!.current_version,request_id:crypto.randomUUID(),stage:'queued',started_at:Math.floor(Date.now()/1000)};
  return submit(id,receipt);
 }
 // A lost response is not an installation failure. After a later successful
 // status read proves the child is idle, retry exactly the same CAS receipt.
 function canStopWaiting(id:string):boolean{
  const state=states[id];
  return !disposed&&!busy[id]&&!stale[id]&&!!pending[id]&&!!uncertain[id]&&!!state?.available&&!state.error&&!!state.current_version&&Date.now()-(lastRead.get(id)||0)<15000&&!activeStages.has(state.operation?.stage||'')&&state.operation?.request_id!==pending[id]?.request_id;
 }
 function canRetry(id:string):boolean{return canStopWaiting(id)&&states[id]?.current_version===pending[id]?.expected_version;}
 async function retry(id:string){if(!canRetry(id))return false;return submit(id,{...pending[id]!});}
 function stopWaiting(id:string){
  if(!canStopWaiting(id))return false;
  delete pending[id];delete uncertain[id];stale[id]=true;
  if(states[id])states[id]={...states[id]!,catalog_stale:true};
  errors[id]='本次等待已结束，请重新检查子站版本。';return true;
 }
 async function submit(id:string,receipt:VersionOperation){
  const request=new AbortController(),timeout=AbortSignal.timeout(40000);requests.set(id,request);pending[id]=receipt;busy[id]=true;stale[id]=true;errors[id]='';
  try{
   const operation=await options.api<VersionOperation>('/'+id+'/updates/apply','POST',{action:receipt.action,version:receipt.version,expected_version:receipt.expected_version,request_id:receipt.request_id},{signal:AbortSignal.any([request.signal,timeout])});
   if(disposed||request.signal.aborted||requests.get(id)!==request)return false;
   if(operation.request_id!==receipt.request_id)throw new Error('未收到执行确认，请重新检测版本');
   receive(id,{...states[id]!,operation});
   return true;
  }catch(error){
   if(!disposed&&!request.signal.aborted&&requests.get(id)===request){
    if(error instanceof ApiError&&error.status<500){errors[id]=error.message;delete pending[id];delete uncertain[id];}
    else {uncertain[id]=true;errors[id]=timeout.aborted?'提交等待超时，正在确认更新状态':'连接暂时中断，正在确认更新状态';}
   }
   return false;
  }finally{if(requests.get(id)===request){requests.delete(id);delete busy[id];}if(!disposed)poll.start();}
 }
 function setOpen(id:string,open:boolean){if(disposed)return;if(open){opened.add(id);poll.start();void check(id);}else opened.delete(id);}
 function remove(id:string){requests.get(id)?.abort();requests.delete(id);opened.delete(id);lastRead.delete(id);delete states[id];delete busy[id];delete errors[id];delete pending[id];delete uncertain[id];delete stale[id];delete updateChoice[id];delete rollbackChoice[id];}
 const poll=serialPoll(async()=>{
  if(disposed||options.visible?.()===false)return;
  const ids=new Set([...opened,...Object.keys(pending)]),queue=[...ids].filter(id=>!busy[id]&&Date.now()-(lastRead.get(id)||0)>=(running(id)?2000:15000));
  await Promise.all(Array.from({length:Math.min(4,queue.length)},async()=>{while(queue.length&&!disposed){const id=queue.shift();if(id)await check(id,false);}}));
 },()=>2000);
 onScopeDispose(()=>{disposed=true;poll.stop();for(const request of requests.values())request.abort();requests.clear();});
 return {states,busy,errors,updateChoice,rollbackChoice,pending,uncertain,running,check,apply,canApply,canRetry,retry,canStopWaiting,stopWaiting,setOpen,remove};
}
