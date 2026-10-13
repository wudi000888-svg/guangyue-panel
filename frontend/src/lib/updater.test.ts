import {describe,it,expect} from 'vitest';
import {freshCatalog,newerVersion,releaseStatusMessage,usableReleases,updateOutcome,type UpdateState,type VersionOperation} from './updater';
const pending:VersionOperation={action:'update',version:'0.18.0',expected_version:'0.17.0',request_id:'current',stage:'queued'};
describe('version switching',()=>{
 it('compares numeric version components',()=>{expect(newerVersion('0.10.0','0.9.99')).toBe(true);expect(newerVersion('0.17.0','0.17.0')).toBe(false);expect(newerVersion('0.9.0','1.0.0')).toBe(false);});
 it('rejects malformed versions instead of offering an invalid target',()=>{expect(newerVersion('1.2','1.1.0')).toBe(false);expect(newerVersion('1.2.3-beta','1.1.0')).toBe(false);expect(newerVersion('v1.2.3','1.1.0')).toBe(false);expect(newerVersion('1.2.3','')).toBe(false);});
 it('requires this operation and a healthy target before refreshing',()=>{expect(updateOutcome(pending,{...pending,stage:'succeeded'},'0.17.0')).toBe('waiting');expect(updateOutcome(pending,{...pending,request_id:'previous',stage:'succeeded'},'0.18.0')).toBe('waiting');expect(updateOutcome(pending,{...pending,stage:'succeeded'},'0.18.0')).toBe('success');});
 it('never treats a failed operation or temporary outage as success',()=>{expect(updateOutcome(pending,{...pending,stage:'failed'},'0.18.0')).toBe('failed');expect(updateOutcome(pending,undefined)).toBe('waiting');expect(updateOutcome(pending,undefined,'0.18.0')).toBe('waiting');});
 it('can refresh after rollback to a version without the status endpoint',()=>{const old={...pending,action:'rollback' as const,version:'0.16.0'};expect(updateOutcome(old,undefined,'0.16.0')).toBe('success');expect(updateOutcome(old,undefined,'0.17.0')).toBe('waiting');});
});
describe('release catalogue confidence',()=>{
 const state=(extra:Partial<UpdateState>={}):UpdateState=>({available:true,current_version:'0.39.15',checked_at:1000,releases:[],...extra});
 it('never labels an unchecked, unavailable or stale empty catalogue as current',()=>{
  for(const value of [undefined,state({checked_at:0}),state({available:false}),state({current_version:''}),state({warning:'版本来源暂不可用，请稍后重试'}),state({catalog_error:'GitHub unavailable'}),state({catalog_stale:true})]){expect(freshCatalog(value,1100)).toBe(false);expect(releaseStatusMessage(value,1100)).not.toBe('已是最新版本');}
  expect(freshCatalog(state(),1901)).toBe(false);expect(releaseStatusMessage(state(),1901)).not.toBe('已是最新版本');expect(releaseStatusMessage(state(),1100)).toBe('已是最新版本');
 });
 it('filters invalid and older releases, then offers the true newest numeric version',()=>{
  const releases=['0.9.0','0.40.0','0.39.16','v1.0.0','0.39.15'].map(version=>({version,name:version,notes:'',published_at:'',url:'https://example.com'}));
  expect(usableReleases(state({releases})).map(r=>r.version)).toEqual(['0.40.0','0.39.16']);
 });
});
