import {expect,it} from 'vitest';
import {selectedPlanGroupKeys,unavailablePlanGroupKeys} from './planNodeSelection';
import {removePlanGroupNodes} from '../lib/nodeGroups';
import type {GroupMember} from '../types';
const mounted:GroupMember={site_id:'child',source:'mounted',node_id:'vless-main',selection_key:'default-subsite/child/vless-main',name:'Child',protocol:'vless'};
it('keeps a restriction visible when its selected node temporarily disappears',()=>{
 const keys=['default-subsite/child/removed','legacy-private/vless-main'];
 expect(selectedPlanGroupKeys('default-subsite',[mounted],keys)).toEqual([keys[0]]);
 expect(unavailablePlanGroupKeys('default-subsite',[mounted],keys)).toEqual([keys[0]]);
 expect(removePlanGroupNodes('default-subsite',[mounted],keys)).toEqual([keys[1]]);
});
it('distinguishes matching local IDs from mounted nodes and preserves other groups',()=>{
 const local:GroupMember={site_id:'main',source:'local',node_id:'vless-main',selection_key:'legacy-private/vless-main',name:'Local',protocol:'vless'};
 const keys=['vless-main',mounted.selection_key!];
 expect(selectedPlanGroupKeys('legacy-private',[local],keys)).toEqual(['vless-main']);
 expect(selectedPlanGroupKeys('default-subsite',[mounted],keys)).toEqual([mounted.selection_key]);
 expect(unavailablePlanGroupKeys('default-subsite',[mounted],keys)).toEqual([]);
});
