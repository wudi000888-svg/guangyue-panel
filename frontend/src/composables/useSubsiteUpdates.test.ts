import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { effectScope, type EffectScope } from 'vue';
import { ApiError } from '../lib/api';
import { useSubsiteUpdates } from './useSubsiteUpdates';
import type { UpdateState, VersionOperation } from '../lib/updater';

const scopes:EffectScope[]=[];
beforeEach(()=>{vi.useFakeTimers();vi.setSystemTime(new Date('2026-10-13T01:00:00Z'));});
afterEach(()=>{scopes.splice(0).forEach(scope=>scope.stop());vi.restoreAllMocks();vi.useRealTimers();});
const snapshot=(overrides:Partial<UpdateState>={}):UpdateState=>({available:true,current_version:'0.39.15',checked_at:Date.now()/1000,releases:[{version:'0.39.16',name:'Latest',notes:'',url:'https://example.com/release',published_at:''}],rollback_versions:[{version:'0.39.14',created:1,compatible:true}],...overrides});
function setup(handler?:(path:string,method:string,body:any,normal:()=>unknown)=>unknown){
 let server=snapshot(),visible=true;
 const request=vi.fn(async(path:string,method='GET',body?:any)=>{
  const normal=()=>{
   if(path.endsWith('/apply')){server={...server,operation:{...body,stage:'queued',started_at:Date.now()/1000}};return server.operation;}
   return server;
  };
  return handler?await handler(path,method,body,normal):normal();
 });
 const scope=effectScope();scopes.push(scope);const notify=vi.fn(),onVersion=vi.fn();
 const controller=scope.run(()=>useSubsiteUpdates({api:request as any,visible:()=>visible,notify,onVersion}))!;
 return {controller,request,notify,onVersion,scope,set:(state:UpdateState)=>{server=state;},state:()=>server,visible:(value:boolean)=>{visible=value;}};
}
describe('sub-site version operations',()=>{
 it('refreshes the release catalogue rather than merely reading cached status',async()=>{
  const s=setup();await s.controller.check('child');
  expect(s.request.mock.calls[0].slice(0,3)).toEqual(['/child/updates/check','POST',{}]);
  expect(s.controller.updateChoice.child).toBe('0.39.16');expect(s.controller.canApply('child','update','0.39.16')).toBe(true);
 });
 it('rejects unknown, failed, stale or absent targets and replaces vanished selections',async()=>{
  const s=setup();s.set(snapshot({checked_at:0,releases:[]}));await s.controller.check('child');expect(s.controller.canApply('child','update','0.39.16')).toBe(false);
  s.set(snapshot({catalog_error:'Offline',catalog_stale:true}));await s.controller.check('child');expect(s.controller.errors.child).toBe('Offline');expect(s.controller.canApply('child','update','0.39.16')).toBe(false);
  s.set(snapshot({checked_at:Date.now()/1000-901}));await s.controller.check('child');expect(s.controller.canApply('child','update','0.39.16')).toBe(false);
  s.set(snapshot());await s.controller.check('child');s.controller.updateChoice.child='0.39.16';s.controller.rollbackChoice.child='0.39.14';
  s.set(snapshot({releases:[{...s.state().releases![0],version:'0.39.17'}],rollback_versions:[]}));await s.controller.check('child');
  expect(s.controller.updateChoice.child).toBe('0.39.17');expect(s.controller.rollbackChoice.child).toBe('');expect(s.controller.canApply('child','update','0.39.16')).toBe(false);
 });
 it('polls across child restarts and requires this receipt and the healthy target',async()=>{
  let disconnected=false;
  const s=setup((_path,method,_body,normal)=>{if(method==='GET'&&disconnected)throw new Error('Restart');return normal();});
  await s.controller.check('child');await s.controller.apply('child','update','0.39.16');const receipt=s.controller.pending.child!;
  expect(s.controller.canApply('child','update','0.39.16')).toBe(false);const count=s.request.mock.calls.length;
  await s.controller.apply('child','update','0.39.16');expect(s.request.mock.calls).toHaveLength(count);
  disconnected=true;await vi.advanceTimersByTimeAsync(2100);expect(s.controller.running('child')).toBe(true);expect(s.controller.errors.child).toContain('正在确认');
  disconnected=false;s.set(snapshot({current_version:'0.39.16',operation:{...receipt,stage:'restarting'}}));await vi.advanceTimersByTimeAsync(2100);expect(s.notify).not.toHaveBeenCalled();
  s.set(snapshot({current_version:'0.39.16',operation:{...receipt,request_id:'different',stage:'succeeded'}}));await vi.advanceTimersByTimeAsync(2100);expect(s.notify).not.toHaveBeenCalled();
  s.set(snapshot({current_version:'0.39.15',operation:{...receipt,stage:'succeeded'}}));await vi.advanceTimersByTimeAsync(2100);expect(s.notify).not.toHaveBeenCalled();
  s.set(snapshot({current_version:'0.39.16',operation:{...receipt,stage:'succeeded'}}));await vi.advanceTimersByTimeAsync(2100);
  expect(s.controller.running('child')).toBe(false);expect(s.notify).toHaveBeenCalledWith('child',expect.objectContaining({request_id:receipt.request_id}));expect(s.onVersion).toHaveBeenLastCalledWith('child','0.39.16');
  expect(s.request.mock.calls.slice(2).every(([,method])=>method==='GET')).toBe(true);
 });
 it('adopts a child operation already in progress and avoids catalogue scans',async()=>{
  const s=setup();const operation:VersionOperation={action:'update',version:'0.39.16',expected_version:'0.39.15',request_id:'existing',stage:'downloading',started_at:Date.now()/1000};
  s.set(snapshot({operation}));await s.controller.check('child',false);await s.controller.check('child');
  expect(s.controller.pending.child?.request_id).toBe('existing');expect(s.request.mock.calls.every(([,method])=>method==='GET')).toBe(true);
 });
 it('keeps an uncertain accepted mutation pending until read-back, without duplicate writes',async()=>{
  const s=setup((path,_method,_body,normal)=>{const value=normal();if(path.endsWith('/apply'))throw new Error('Lost response');return value;});
  await s.controller.check('child');expect(await s.controller.apply('child','update','0.39.16')).toBe(false);
  const receipt=s.controller.pending.child!;expect(receipt.request_id).toBeTruthy();await vi.advanceTimersByTimeAsync(2100);
  expect(s.controller.pending.child?.request_id).toBe(receipt.request_id);expect(s.request.mock.calls.filter(([path])=>path.endsWith('/apply'))).toHaveLength(1);
 });
 it('shows authoritative rejection or operation failure without claiming success',async()=>{
  let reject=true;
  const s=setup((path,_method,_body,normal)=>{if(path.endsWith('/apply')&&reject)throw new ApiError(409,{error:'Version changed'});return normal();});
  await s.controller.check('child');await s.controller.apply('child','update','0.39.16');expect(s.controller.pending.child).toBeUndefined();expect(s.controller.errors.child).toBe('Version changed');
  reject=false;await s.controller.check('child');await s.controller.apply('child','update','0.39.16');const receipt=s.controller.pending.child!;
  s.set(snapshot({operation:{...receipt,stage:'failed',error:'Installation rejected'}}));await vi.advanceTimersByTimeAsync(2100);
  expect(s.controller.pending.child).toBeUndefined();expect(s.controller.errors.child).toBe('Installation rejected');expect(s.notify).not.toHaveBeenCalled();
 });
 it('pauses hidden polling and ignores removed-site or disposed responses',async()=>{
  const s=setup();await s.controller.check('child');await s.controller.apply('child','update','0.39.16');const count=s.request.mock.calls.length;
  s.visible(false);await vi.advanceTimersByTimeAsync(10000);expect(s.request.mock.calls).toHaveLength(count);
  s.visible(true);await vi.advanceTimersByTimeAsync(2100);expect(s.request.mock.calls.length).toBeGreaterThan(count);
  s.controller.remove('child');expect(s.controller.pending.child).toBeUndefined();const after=s.request.mock.calls.length;await vi.advanceTimersByTimeAsync(5000);expect(s.request.mock.calls).toHaveLength(after);
  let resolve!:(state:UpdateState)=>void;const slow=setup(()=>new Promise<UpdateState>(done=>{resolve=done;}));const loading=slow.controller.check('old');slow.scope.stop();resolve(snapshot());await loading;expect(slow.controller.states.old).toBeUndefined();
 });
 it('invalidates a cached authorization after any failed check and restores it only after a successful read',async()=>{
  let offline=false;const s=setup((_path,_method,_body,normal)=>{if(offline)throw new Error('Offline');return normal();});
  await s.controller.check('child');expect(s.controller.canApply('child','update','0.39.16')).toBe(true);
  offline=true;await s.controller.check('child');expect(s.controller.states.child?.catalog_stale).toBe(true);expect(s.controller.canApply('child','update','0.39.16')).toBe(false);expect(s.controller.canApply('child','rollback','0.39.14')).toBe(false);
  offline=false;await s.controller.check('child');expect(s.controller.canApply('child','update','0.39.16')).toBe(true);
 });
 it('safely retries an undelivered request using the same operation receipt, after idle read-back',async()=>{
  let lost=true;const s=setup((path,_method,_body,normal)=>{if(path.endsWith('/apply')&&lost)throw new Error('Not delivered');return normal();});
  await s.controller.check('child');await s.controller.apply('child','update','0.39.16');const receipt={...s.controller.pending.child!};
  expect(s.controller.uncertain.child).toBe(true);expect(s.controller.canRetry('child')).toBe(false);
  await s.controller.check('child',false);expect(s.controller.canRetry('child')).toBe(true);lost=false;expect(await s.controller.retry('child')).toBe(true);
  const calls=s.request.mock.calls.filter(([path])=>path.endsWith('/apply'));expect(calls).toHaveLength(2);expect(calls[1][2]).toEqual(calls[0][2]);expect(s.controller.pending.child?.request_id).toBe(receipt.request_id);expect(s.controller.uncertain.child).toBeUndefined();
  s.set(snapshot({current_version:'0.39.16',operation:{...receipt,stage:'succeeded'}}));await s.controller.check('child',false);expect(s.notify).toHaveBeenCalledWith('child',expect.objectContaining({request_id:receipt.request_id}));
 });
 it('does not retry during another operation, and can stop local waiting after an unrelated version change',async()=>{
  const s=setup((path,_method,_body,normal)=>{if(path.endsWith('/apply'))throw new Error('Not delivered');return normal();});
  await s.controller.check('child');await s.controller.apply('child','update','0.39.16');const receipt={...s.controller.pending.child!};
  s.set(snapshot({operation:{...receipt,request_id:'another-admin',stage:'installing'}}));await s.controller.check('child',false);expect(s.controller.canRetry('child')).toBe(false);expect(s.controller.canStopWaiting('child')).toBe(false);expect(s.notify).not.toHaveBeenCalled();
  s.set(snapshot({current_version:'0.39.16',operation:{...receipt,request_id:'another-admin',stage:'succeeded'}}));await s.controller.check('child',false);expect(s.controller.canRetry('child')).toBe(false);expect(s.controller.canStopWaiting('child')).toBe(true);expect(s.notify).not.toHaveBeenCalled();
  expect(s.controller.stopWaiting('child')).toBe(true);expect(s.controller.pending.child).toBeUndefined();expect(s.controller.errors.child).toContain('重新检查');expect(s.controller.canApply('child','rollback','0.39.14')).toBe(false);
  expect(s.request.mock.calls.filter(([path])=>path.endsWith('/apply'))).toHaveLength(1);
 });
 it('distinguishes a timeout converted to AbortError from navigation cancellation',async()=>{
  const timeout=new AbortController();vi.spyOn(AbortSignal,'timeout').mockReturnValue(timeout.signal);
  let fail=false;const s=setup((_path,_method,_body,normal)=>{if(fail){timeout.abort();throw new DOMException('Cancelled','AbortError');}return normal();});
  await s.controller.check('child');fail=true;await s.controller.check('child');expect(s.controller.errors.child).toBe('版本检查超时，请重试');expect(s.controller.canApply('child','update','0.39.16')).toBe(false);
 });
 it('retains a timed-out submission for receipt read-back, rather than ignoring its AbortError',async()=>{
  const timeout=new AbortController();vi.spyOn(AbortSignal,'timeout').mockReturnValue(timeout.signal);
  const s=setup((path,_method,_body,normal)=>{if(path.endsWith('/apply')){timeout.abort();throw new DOMException('Cancelled','AbortError');}return normal();});
  await s.controller.check('child');await s.controller.apply('child','update','0.39.16');expect(s.controller.pending.child).toBeDefined();expect(s.controller.uncertain.child).toBe(true);expect(s.controller.errors.child).toBe('提交等待超时，正在确认更新状态');
 });
 it('does not revive the poll timer after an in-flight submission is disposed',async()=>{
  let resolve!:(operation:VersionOperation)=>void;let body:any;
  const s=setup((path,_method,input,normal)=>{if(path.endsWith('/apply')){body=input;return new Promise<VersionOperation>(done=>{resolve=done;});}return normal();});
  await s.controller.check('child');const submitting=s.controller.apply('child','update','0.39.16');s.scope.stop();resolve({...body,stage:'queued'});await submitting;
  expect(vi.getTimerCount()).toBe(0);const count=s.request.mock.calls.length;await vi.advanceTimersByTimeAsync(10000);expect(s.request.mock.calls).toHaveLength(count);expect(s.notify).not.toHaveBeenCalled();
 });
});
