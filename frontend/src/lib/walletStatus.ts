import {canAllocate,MAX_WALLET_NEXT_INDEX,type CryptoWallet} from './cryptoWallet';
export interface WalletReceivingSettings {enabled:boolean;wallet_id:string}
/** Enabled is only the address allocation switch, never proof that checkout uses this wallet. */
export function walletStatus(wallet:CryptoWallet,settings?:WalletReceivingSettings,paymentModuleEnabled=false):{label:string;tone:'warning'|'muted'|'ready'}{
 if(wallet.recovery_required)return {label:'待核对恢复索引',tone:'warning'};
 if(wallet.mode==='hot'&&!wallet.backup_confirmed)return {label:'待备份确认',tone:'warning'};
 if(wallet.next_index>=MAX_WALLET_NEXT_INDEX)return {label:'地址索引已用尽',tone:'warning'};
 if(!wallet.enabled)return {label:'已暂停',tone:'muted'};
 if(canAllocate(wallet)&&paymentModuleEnabled&&settings?.enabled&&settings.wallet_id===wallet.id)return {label:'当前收款钱包',tone:'ready'};
 return {label:'可用钱包',tone:'muted'};
}
/** Receiving can be paused while existing funds remain withdrawable. The signing
 * gate is the payment module, hot custody, backup and recovery confirmation. */
export function walletWithdrawAvailability(wallet:CryptoWallet,paymentLoaded:boolean,paymentEnabled:boolean):{disabled:boolean;reason:string}{
 if(!paymentLoaded)return {disabled:true,reason:'支付服务状态尚未确认，暂不能归集提现。'};
 if(!paymentEnabled)return {disabled:true,reason:'支付模块已关闭，暂不能归集提现。'};
 if(wallet.mode!=='hot')return {disabled:true,reason:'只读钱包不能在面板归集，请使用持有私钥的外部钱包。'};
 if(wallet.recovery_required)return {disabled:true,reason:'请先核对钱包恢复索引，再进行资产提取。'};
 if(!wallet.backup_confirmed)return {disabled:true,reason:'请先下载并确认钱包备份，再进行资产提取。'};
 return {disabled:false,reason:''};
}
