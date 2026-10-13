import {expect,it} from 'vitest';
import {approximateFiat,feeFiat,fiatPriceCurrent} from './cryptoFiat';
it('converts native fee atoms using exact decimal prices without losing amounts above Number precision',()=>{
 expect(approximateFiat('21000000000000',18,'2456.78')).toBe('0.05');
 expect(approximateFiat('5000',9,'154.123456')).toBe('0.0008');
 expect(approximateFiat('1',9,'100')).toBe('<0.0001');
 expect(approximateFiat('9007199254740993123456789000000000',18,'1.25')).toBe('11258999068426241.40');
 expect(approximateFiat('0',9,'154.12')).toBe('0.00');
});
it('never turns missing, malformed or expired prices into fake zero or a fixed USD/CNY ratio',()=>{
 for(const price of [undefined,'','0','NaN','1e4','-1'])expect(approximateFiat('5000',9,price)).toBe('—');
 expect(approximateFiat(undefined,9,'154.12')).toBe('—');
 expect(feeFiat('5000',9,{native_price_usd:'154.123456',native_price_cny:'1098.23'})).toBe('≈ $0.0008 / ¥0.0055');
 expect(feeFiat('5000',9,{native_price_usd:'154.123456'})).toBe('≈ $0.0008 / ¥—');
 const quote={native_price_usd:'154.12',price_checked_at:100,price_expires_at:200};expect(fiatPriceCurrent(quote,100)).toBe(true);expect(fiatPriceCurrent(quote,200)).toBe(false);expect(fiatPriceCurrent({...quote,fiat_error:'provider unavailable'},100)).toBe(false);
});
