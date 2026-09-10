import {expect,it} from 'vitest';
import {cents,money} from './commerce';
it('preserves exact cents without floating point rounding',()=>{
 expect(cents('0.01')).toBe('1');expect(cents('99.99')).toBe('9999');expect(cents('1000000000')).toBe('100000000000');expect(money('-9999')).toBe('-¥99.99');
 for(const value of ['-1','0','1.234','1e2','Infinity','01','1000000000.01'])expect(()=>cents(value)).toThrow();
});
