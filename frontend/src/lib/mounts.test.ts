import {expect,it} from 'vitest';
import {defaultMountGroups,mountGrantPayload,mountAssignmentGroups} from './mounts';
import type {NodeGroup} from '../types';
it('preselects only the enabled default group without broadening access',()=>{
 const group={id:'default-subsite',enabled:true} as NodeGroup;
 expect(defaultMountGroups([group])).toEqual(['default-subsite']);
 expect(defaultMountGroups([{...group,enabled:false}])).toEqual([]);
 expect(defaultMountGroups([{...group,id:'legacy-private'}])).toEqual([]);
});
it('prefers the child group without also suggesting overlapping local access',()=>{
 const groups=['default-subsite','legacy-private','other'].map(id=>({id,enabled:true} as NodeGroup));
 const nodes=[{node_id:'vless',name:'Same',enabled:true,group_ids:['default-subsite']},{node_id:'hy2',name:'Same',enabled:true,group_ids:['legacy-private','default-subsite']}];
 expect(mountAssignmentGroups(nodes,groups)).toEqual(['default-subsite']);
 expect(mountAssignmentGroups([...nodes,{...nodes[0],node_id:'other',group_ids:['other']}],groups)).toEqual(['default-subsite','other']);
 expect(mountAssignmentGroups(nodes,groups.map(g=>({...g,enabled:false})))).toEqual([]);
});
it('retains absolute manual limits when the remaining-budget field is unchanged',()=>{
 expect(mountGrantPayload([{user_id:7,budget:30,quota:100,original_budget:30}])).toEqual([{user_id:7,budget:30,quota:100}]);
 expect(mountGrantPayload([{user_id:7,budget:40,quota:100,original_budget:30}])).toEqual([{user_id:7,budget:40,quota:0}]);
});
