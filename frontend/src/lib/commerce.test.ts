import {expect,it} from 'vitest';
import {cents,money,operationID} from './commerce';
it('preserves exact cents without floating point rounding',()=>{
 expect(cents('0.01')).toBe('1');expect(cents('99.99')).toBe('9999');expect(cents('1000000000')).toBe('100000000000');expect(money('-9999')).toBe('-¥99.99');
 for(const value of ['-1','0','1.234','1e2','Infinity','01','1000000000.01'])expect(()=>cents(value)).toThrow();
});
it('creates a valid idempotency key without relying on randomUUID',()=>{
 const id=operationID();
 expect(id).toMatch(/^[A-Za-z0-9_-]{16,80}$/);
});

import {afterEach,vi} from 'vitest';
import {effectScope} from 'vue';
import {canResumePayment,paymentCheckoutURL,preparePaymentWindow,useCommerce,type PaymentAttempt} from './commerce';
import {invalidateSession} from './api';
afterEach(()=>{invalidateSession();vi.unstubAllGlobals()});
it('rejects unsafe payment redirects and preserves signed HTTPS checkout queries',()=>{
 const address='https://pay.example.com/submit.php?money=12.34&sign=abc%2Bdef';
 expect(paymentCheckoutURL(address)).toBe(address);
 for(const unsafe of ['javascript:alert(1)','data:text/html,pay','http://pay.example.com','https://secret@pay.example.com','//pay.example.com',''])expect(()=>paymentCheckoutURL(unsafe)).toThrow();
});
it('reserves the payment window before the async checkout and disconnects its opener',()=>{
 const replace=vi.fn(),close=vi.fn(),popup={closed:false,opener:{},document:{title:'',body:{textContent:''}},location:{replace},close};
 const open=vi.fn(()=>popup),assign=vi.fn();vi.stubGlobal('window',{open,location:{assign}});
 const pending=preparePaymentWindow();expect(open).toHaveBeenCalledWith('about:blank','_blank');expect(popup.opener).toBeNull();expect(replace).not.toHaveBeenCalled();
 expect(pending.open('https://checkout.stripe.com/c/pay/test')).toBe('new-tab');expect(replace).toHaveBeenCalledWith('https://checkout.stripe.com/c/pay/test');expect(assign).not.toHaveBeenCalled();
});
it('uses a same-page redirect if a browser blocks the popup',()=>{
 const assign=vi.fn();vi.stubGlobal('window',{open:vi.fn(()=>null),location:{assign}});
 expect(preparePaymentWindow().open('https://pay.example.com/')).toBe('same-tab');expect(assign).toHaveBeenCalledWith('https://pay.example.com/');
});
it('closes the empty window on checkout failure or an invalid destination',()=>{
 const close=vi.fn(),popup={closed:false,opener:{},document:{title:'',body:{textContent:''}},location:{replace:vi.fn()},close};vi.stubGlobal('window',{open:vi.fn(()=>popup),location:{assign:vi.fn()}});
 const pending=preparePaymentWindow();pending.close();expect(close).toHaveBeenCalledTimes(1);
 const invalid=preparePaymentWindow();expect(()=>invalid.open('javascript:alert(1)')).toThrow();expect(close).toHaveBeenCalledTimes(2);expect(popup.location.replace).not.toHaveBeenCalled();
});
it('does not reopen paid, refunded, or expired payment attempts',()=>{
 const payment={state:'awaiting_customer',expires:200} as PaymentAttempt;
 expect(canResumePayment(payment,100)).toBe(true);expect(canResumePayment(payment,200)).toBe(false);
 for(const state of ['paid','completed','refund_required','refunded','cancelled','expired'])expect(canResumePayment({...payment,state},100)).toBe(false);
});
it('reuses the payment operation id after a network failure even when the password is re-entered',async()=>{
 const fetch=vi.fn().mockRejectedValueOnce(new Error('network interrupted')).mockResolvedValue(Response.json({ok:true}));vi.stubGlobal('fetch',fetch);
 const scope=effectScope(),commerce=scope.run(()=>useCommerce())!;
 try{
  expect(await commerce.run('/payment-refunds',{receipt_id:'receipt-1',action:'confirm_manual',external_ref:'actual-refund-1',reason:'Confirmed',password:'first'})).toBeNull();
  expect(await commerce.run('/payment-refunds',{receipt_id:'receipt-1',action:'confirm_manual',external_ref:'actual-refund-1',reason:'Confirmed',password:'second'})).toEqual({ok:true});
  const first=JSON.parse(fetch.mock.calls[0]![1].body),second=JSON.parse(fetch.mock.calls[1]![1].body);
  expect(first.operation_id).toBe(second.operation_id);expect(second.receipt_id).toBe('receipt-1');expect(second.external_ref).toBe('actual-refund-1');expect(second.password).toBe('second');
 }finally{scope.stop()}
});
