import {afterEach,expect,it,vi} from 'vitest';
import {effectScope,nextTick} from 'vue';
import {invalidateSession,setRemoteSite} from './api';
import {notifyPaymentModuleChanged,usePaymentModule} from './paymentModule';
afterEach(()=>{setRemoteSite('');invalidateSession();vi.unstubAllGlobals()});
const flush=async()=>{await new Promise(resolve=>setTimeout(resolve,0));await nextTick()};
it('does not enable purchases before an explicit true settings response',async()=>{
 let respond!:(response:Response)=>void;vi.stubGlobal('fetch',vi.fn(()=>new Promise(resolve=>{respond=resolve})));
 const scope=effectScope(),gate=scope.run(()=>usePaymentModule())!;
 try{expect(gate.enabled.value).toBe(false);respond(Response.json({payment_module_enabled:true}));await flush();expect(gate.loaded.value).toBe(true);expect(gate.enabled.value).toBe(true)}finally{scope.stop()}
});
it('missing, false or failed settings cannot fall back to an enabled payment module',async()=>{
 const fetch=vi.fn().mockResolvedValueOnce(Response.json({})).mockResolvedValueOnce(Response.json({payment_module_enabled:false})).mockRejectedValueOnce(new Error('offline'));vi.stubGlobal('fetch',fetch);
 const scope=effectScope(),gate=scope.run(()=>usePaymentModule())!;
 try{await flush();expect(gate.enabled.value).toBe(false);await gate.refresh();expect(gate.enabled.value).toBe(false);await gate.refresh();expect(gate.loaded.value).toBe(false);expect(gate.enabled.value).toBe(false);expect(gate.error.value).toBeTruthy()}finally{scope.stop()}
});
it('refreshes immediately after an administrator change and ignores an older enabled response',async()=>{
 const responses:Array<(v:Response)=>void>=[];vi.stubGlobal('fetch',vi.fn(()=>new Promise(resolve=>responses.push(resolve))));
 const scope=effectScope(),gate=scope.run(()=>usePaymentModule())!;
 try{notifyPaymentModuleChanged();expect(responses).toHaveLength(2);responses[1]!(Response.json({payment_module_enabled:false}));await flush();responses[0]!(Response.json({payment_module_enabled:true}));await flush();expect(gate.loaded.value).toBe(true);expect(gate.enabled.value).toBe(false)}finally{scope.stop()}
});
it('reads the entry-site module gate even while a child site is selected',async()=>{
 setRemoteSite('child');const fetch=vi.fn().mockResolvedValue(Response.json({payment_module_enabled:false}));vi.stubGlobal('fetch',fetch);
 const scope=effectScope();scope.run(()=>usePaymentModule());try{await flush();expect(fetch.mock.calls[0]![0]).toBe('/api/commerce/settings');expect(new Headers(fetch.mock.calls[0]![1].headers).has('X-Guangyue-Site')).toBe(false)}finally{scope.stop()}
});
