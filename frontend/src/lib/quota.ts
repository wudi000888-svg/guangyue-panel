import type {User} from '../types';
export const quotaUsed=(u:User)=>u.meter?Math.max(0,u.meter.upload-u.meter.base_upload)+Math.max(0,u.meter.download-u.meter.base_download):u.upload+u.download;
export const rawPeriodUsed=(u:User)=>u.meter?Math.max(0,u.upload-u.meter.raw_base_upload)+Math.max(0,u.download-u.meter.raw_base_download):u.upload+u.download;
export const rateText=(n:{rate_milli?:number})=>`${(n.rate_milli??1000)/1000}×`;
