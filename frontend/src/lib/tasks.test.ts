import { afterEach, expect, it, vi } from 'vitest';
const mock=vi.hoisted(()=>({generation:1,api:vi.fn()}));
vi.mock('./api',()=>({api:mock.api,requestGeneration:()=>mock.generation}));
import { runQueued } from './tasks';
afterEach(()=>{vi.useRealTimers();mock.api.mockReset();mock.generation=1;});
it('stops a queued result poll when the selected site changes',async()=>{
 vi.useFakeTimers();mock.api.mockResolvedValueOnce({id:'task-a'}).mockResolvedValue({state:'running'});
 const result=runQueued('speed','ips/one');const rejected=expect(result).rejects.toMatchObject({name:'AbortError'});
 await vi.advanceTimersByTimeAsync(10);mock.generation++;await vi.advanceTimersByTimeAsync(1200);await rejected;
 expect(mock.api).toHaveBeenCalledTimes(2);
});
it('reports durable failures instead of returning an empty result',async()=>{
 mock.api.mockResolvedValueOnce({id:'task-a'}).mockResolvedValueOnce({state:'failed',error:'source changed'});
 await expect(runQueued('quality','ips/one')).rejects.toThrow('source changed');
});
