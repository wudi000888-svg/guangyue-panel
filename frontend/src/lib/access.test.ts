import { expect, it } from 'vitest';
import { allowedRoute } from './access';
it('keeps role, edition and interface mode independent',()=>{
 expect(allowedRoute({owner:true},{role:'user',edition:'pro',simple:false})).toBe(false);
 expect(allowedRoute({professional:true},{role:'owner',edition:'pro',simple:true})).toBe(false);
 expect(allowedRoute({pro:true},{role:'owner',edition:'lite',simple:false})).toBe(false);
 expect(allowedRoute({owner:true},{role:'owner',edition:'lite',simple:true})).toBe(true);
 expect(allowedRoute({},{role:null,edition:'pro',simple:false})).toBe(false);
});
