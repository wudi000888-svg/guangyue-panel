import { expect, it } from 'vitest';
import { allowedRoute } from './access';
it('keeps role and edition permissions independent',()=>{
 expect(allowedRoute({owner:true},{role:'user',edition:'pro'})).toBe(false);
 expect(allowedRoute({pro:true},{role:'owner',edition:'lite'})).toBe(false);
 expect(allowedRoute({owner:true},{role:'owner',edition:'lite'})).toBe(true);
 expect(allowedRoute({},{role:null,edition:'pro'})).toBe(false);
});
