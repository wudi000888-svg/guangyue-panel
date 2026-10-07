import type {GroupMember} from '../types';
import {planMemberSelected} from '../lib/nodeGroups';

export function selectedPlanGroupKeys(groupID:string,members:GroupMember[],nodeIds:string[]):string[]{
 return nodeIds.filter(id=>id.startsWith(groupID+'/')||members.some(member=>planMemberSelected(member,[id])));
}
export function unavailablePlanGroupKeys(groupID:string,members:GroupMember[],nodeIds:string[]):string[]{
 return selectedPlanGroupKeys(groupID,members,nodeIds).filter(id=>!members.some(member=>planMemberSelected(member,[id])));
}
