import {expect,it} from 'vitest';
import {defaultMountGroups} from './mounts';
import type {NodeGroup} from '../types';
it('preselects only the enabled default group without broadening access',()=>{
 const group={id:'default-subsite',enabled:true} as NodeGroup;
 expect(defaultMountGroups([group])).toEqual(['default-subsite']);
 expect(defaultMountGroups([{...group,enabled:false}])).toEqual([]);
 expect(defaultMountGroups([{...group,id:'legacy-private'}])).toEqual([]);
});
