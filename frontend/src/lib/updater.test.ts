import {describe,it,expect} from 'vitest';
import {newerVersion,updateOutcome,type VersionOperation} from './updater';
const pending:VersionOperation={action:'update',version:'0.18.0',expected_version:'0.17.0',request_id:'current',stage:'queued'};
describe('version switching',()=>{
 it('compares numeric version components',()=>{expect(newerVersion('0.10.0','0.9.99')).toBe(true);expect(newerVersion('0.17.0','0.17.0')).toBe(false);expect(newerVersion('0.9.0','1.0.0')).toBe(false);});
 it('requires this operation and a healthy target before refreshing',()=>{expect(updateOutcome(pending,{...pending,stage:'succeeded'},'0.17.0')).toBe('waiting');expect(updateOutcome(pending,{...pending,request_id:'previous',stage:'succeeded'},'0.18.0')).toBe('waiting');expect(updateOutcome(pending,{...pending,stage:'succeeded'},'0.18.0')).toBe('success');});
 it('never treats a failed operation or temporary outage as success',()=>{expect(updateOutcome(pending,{...pending,stage:'failed'},'0.18.0')).toBe('failed');expect(updateOutcome(pending,undefined)).toBe('waiting');expect(updateOutcome(pending,undefined,'0.18.0')).toBe('waiting');});
 it('can refresh after rollback to a version without the status endpoint',()=>{const old={...pending,action:'rollback' as const,version:'0.16.0'};expect(updateOutcome(old,undefined,'0.16.0')).toBe('success');expect(updateOutcome(old,undefined,'0.17.0')).toBe('waiting');});
});
