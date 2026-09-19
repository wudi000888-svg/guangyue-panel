import {expect,it} from 'vitest';
import {defaultMountGroups,mountGrantPayload} from './mounts';
import type {NodeGroup} from '../types';
it('preselects only the enabled default group without broadening access',()=>{
 const group={id:'default-subsite',enabled:true} as NodeGroup;
 expect(defaultMountGroups([group])).toEqual(['default-subsite']);
 expect(defaultMountGroups([{...group,enabled:false}])).toEqual([]);
 expect(defaultMountGroups([{...group,id:'legacy-private'}])).toEqual([]);
});
it('retains absolute manual limits when the remaining-budget field is unchanged',()=>{
 expect(mountGrantPayload([{user_id:7,budget:30,quota:100,original_budget:30}])).toEqual([{user_id:7,budget:30,quota:100}]);
 expect(mountGrantPayload([{user_id:7,budget:40,quota:100,original_budget:30}])).toEqual([{user_id:7,budget:40,quota:0}]);
});
