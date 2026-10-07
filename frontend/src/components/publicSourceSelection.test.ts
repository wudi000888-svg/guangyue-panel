import {expect,it} from 'vitest';
import {rebaseSourceSelection} from './publicSourceSelection';
it('preserves pending source selections when collection settings change',()=>{
 expect(rebaseSourceSelection(['a'],['b'],['a'],['a','b'])).toEqual(['b']);
});
it('includes newly created sources without restoring a source the user unchecked',()=>{
 expect(rebaseSourceSelection(['a'],['b'],['a','new'],['a','b','new'])).toEqual(['new','b']);
});
it('does not retain a draft reference to a deleted source',()=>{
 expect(rebaseSourceSelection(['a'],['a','b'],['a'],['a'])).toEqual(['a']);
});
