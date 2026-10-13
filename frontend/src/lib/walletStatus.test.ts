import {expect,it} from 'vitest';
import {walletStatus,walletWithdrawAvailability} from './walletStatus';
import {MAX_WALLET_NEXT_INDEX,type CryptoWallet} from './cryptoWallet';
const wallet={id:'one',enabled:true,mode:'hot',backup_confirmed:true,recovery_required:false,next_index:0} as CryptoWallet;
it('only an allocatable selected wallet with both payment gates open is the current collection wallet',()=>{
 expect(walletStatus(wallet,{enabled:true,wallet_id:'one'},true).label).toBe('当前收款钱包');
 expect(walletStatus(wallet,{enabled:true,wallet_id:'other'},true).label).toBe('可用钱包');
 expect(walletStatus(wallet,{enabled:false,wallet_id:'one'},true).label).toBe('可用钱包');
 expect(walletStatus(wallet,{enabled:true,wallet_id:'one'},false).label).toBe('可用钱包');
 expect(walletStatus(wallet).label).toBe('可用钱包');
});
it('recovery, backup, exhausted index and paused allocation take precedence over receiving configuration',()=>{
 const settings={enabled:true,wallet_id:'one'};
 expect(walletStatus({...wallet,recovery_required:true,backup_confirmed:false,next_index:MAX_WALLET_NEXT_INDEX,enabled:false},settings,true).label).toBe('待核对恢复索引');
 expect(walletStatus({...wallet,backup_confirmed:false,next_index:MAX_WALLET_NEXT_INDEX,enabled:false},settings,true).label).toBe('待备份确认');
 expect(walletStatus({...wallet,next_index:MAX_WALLET_NEXT_INDEX,enabled:false},settings,true).label).toBe('地址索引已用尽');
 expect(walletStatus({...wallet,enabled:false},settings,true).label).toBe('已暂停');
 expect(walletStatus({...wallet,mode:'watch_only',backup_confirmed:false},settings,true).label).toBe('当前收款钱包');
});

it('withdrawal stays unavailable while payment gate is unknown or closed and explains every custody prerequisite',()=>{
 expect(walletWithdrawAvailability(wallet,false,true)).toEqual({disabled:true,reason:'支付服务状态尚未确认，暂不能归集提现。'});expect(walletWithdrawAvailability(wallet,true,false)).toEqual({disabled:true,reason:'支付模块已关闭，暂不能归集提现。'});
 expect(walletWithdrawAvailability({...wallet,mode:'watch_only'},true,true).reason).toContain('只读钱包');expect(walletWithdrawAvailability({...wallet,recovery_required:true},true,true).reason).toContain('恢复索引');expect(walletWithdrawAvailability({...wallet,backup_confirmed:false},true,true).reason).toContain('钱包备份');
 expect(walletWithdrawAvailability({...wallet,enabled:false},true,true)).toEqual({disabled:false,reason:''});
});
