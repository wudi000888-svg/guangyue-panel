import {requestGeneration} from './api';
export interface SupportDraft {ticketID:string;creating:boolean;title:string;body:string;category:string;orderID:string;resource:string;internal:boolean;attachments:string[];saved:number}
let draft:{generation:number;user:number;value:SupportDraft}|null=null;
// Drafts stay in memory only and cannot cross logout or site changes.
export function saveSupportDraft(user:number,value:SupportDraft){draft=value.body.trim()?{generation:requestGeneration(),user,value}:null}
export function takeSupportDraft(user:number):SupportDraft|null{if(!draft||draft.generation!==requestGeneration()||draft.user!==user){draft=null;return null}const value=draft.value;draft=null;return {...value,attachments:Date.now()-value.saved<15*60000?value.attachments:[]}}
