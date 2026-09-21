import {expect,it} from 'vitest';
import {applyMountGroups,defaultMountGroups,mountGroupSelection} from './mounts';
import type {NodeGroup} from '../types';
import type {MountedNode} from './sites';
it('preselects only the enabled default group without broadening access',()=>{
 const group={id:'default-subsite',enabled:true} as NodeGroup;
 expect(defaultMountGroups([group])).toEqual(['default-subsite']);
 expect(defaultMountGroups([{...group,enabled:false}])).toEqual([]);
 expect(defaultMountGroups([{...group,id:'legacy-private'}])).toEqual([]);
});

it('shows a common selection and applies it to existing mounted nodes',()=>{
 const nodes:MountedNode[]=[
  {node_id:'vless',name:'',enabled:true,group_ids:['default-subsite']},
  {node_id:'hy2',name:'',enabled:true,group_ids:['default-subsite']},
 ];
 expect(mountGroupSelection(nodes,['default-subsite'])).toEqual(['default-subsite']);
 expect(mountGroupSelection([{...nodes[1],group_ids:['custom']}],['default-subsite'])).toEqual(['custom']);
 expect(mountGroupSelection([{...nodes[0],group_ids:['custom']},nodes[1]])).toEqual([]);
 const updated=applyMountGroups(nodes,['custom']);
 expect(updated.map(node=>node.group_ids)).toEqual([['custom'],['custom']]);
 expect(nodes[0].group_ids).toEqual(['default-subsite']);
});
