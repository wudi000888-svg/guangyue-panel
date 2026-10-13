import {t} from '../i18n';
import {tokenAtoms,type CryptoChain,type CryptoConfiguredAsset} from './cryptoPayments';
import type {CryptoWallet} from './cryptoWallet';
import {cryptoAddress,sameCryptoAddress,SOLANA_CHAIN_ID,supportedCryptoChain} from './cryptoNetworks';
import type {NativeFiatQuote} from './cryptoFiat';
export interface CryptoBalance {order_id?:string;invoice_id?:string;user_id?:number;address_id:string;index:number;address:string;path:string;native_atoms:string;tokens:Array<{asset_id:string;symbol:string;decimals:number;balance_atoms:string}>}
export interface CryptoBalancePage {wallet_id:string;chain_id:string;native_symbol:string;native_decimals?:number;watch_only:boolean;operations_supported:boolean;unavailable_reason:string;funding_address:string;funding_path:string;funding_balance_atoms:string;checked_at:number;block_number:number;items:CryptoBalance[];next_after:number|null}
export type SweepFeeMode='economy'|'standard'|'priority'|'custom';
export interface SweepFeeOption {id:Exclude<SweepFeeMode,'custom'>;gas_price_atoms:string;total_gas_atoms:string}
export interface SweepPreview extends NativeFiatQuote {fee_mode?:SweepFeeMode;gas_price_atoms?:string;fee_options?:SweepFeeOption[];native_decimals?:number;fee_model?:'evm'|'solana';total_rent_atoms?:string;can_submit:boolean;unavailable_reason:string;required_funding_atoms:string;funding_shortfall_atoms?:string;wallet_id:string;chain_id:string;asset_id:string;destination:string;min_atoms:string;max_gas_atoms:string;checked_at:number;expires_at:number;block_number:number;native_symbol:string;funding_address:string;funding_balance_atoms:string;total_atoms:string;total_topup_atoms:string;total_gas_atoms:string;items:Array<{address_id:string;address:string;path:string;amount_atoms:string;gas_limit:string;gas_price_atoms:string;gas_fee_atoms:string;topup_atoms:string;funding_gas_atoms:string}>;quote:string}
export interface SweepJob {can_retry?:boolean;can_cancel:boolean;id:string;wallet_id:string;chain_id:number;asset_id:string;destination:string;state:string;error:string;created:number;updated:number;items:Array<{id:string;address:string;amount_atoms:string;state:string;error:string;gas_tx_hash:string;sweep_tx_hash:string}>}
const states:Record<string,string>={cancelled:'已结束',queued:'等待执行',running:'正在执行',waiting:'等待恢复条件',complete:'链上已确认完成',failed:'执行失败',review_required:'需要核对',fee_review_required:'Gas 费用待确认',gas_pending:'补 Gas 等待确认',sweep_pending:'转出等待确认'};
export const sweepStateText=(state:string)=>t(states[state]||state);
export function hasCryptoBalance(row:CryptoBalance){return BigInt(row.native_atoms)>0n||row.tokens.some(t=>BigInt(t.balance_atoms)>0n)}
export function previewFundingFees(preview:SweepPreview){return preview.items.reduce((sum,row)=>sum+BigInt(row.funding_gas_atoms),0n).toString()}
export function previewGasFees(preview:SweepPreview){return preview.items.reduce((sum,row)=>sum+BigInt(row.gas_fee_atoms),0n).toString()}
/** Solana rent is separate from transaction fees but still leaves the fee payer. */
export function previewTotalCost(preview:SweepPreview){return (BigInt(preview.total_gas_atoms)+(preview.fee_model==='solana'||preview.chain_id==='solana'?BigInt(preview.total_rent_atoms||'0'):0n)).toString()}
export function canSignSweep(mode:string,operationsSupported:boolean){return mode==='hot'&&operationsSupported}
export function sweepFinished(job:SweepJob){return job.state==='complete'&&job.items.length>0&&job.items.every(item=>item.state==='complete')}

export function previewFundingShortfall(preview:SweepPreview){if(preview.funding_shortfall_atoms!==undefined&&/^\d+$/.test(preview.funding_shortfall_atoms))return preview.funding_shortfall_atoms;const difference=BigInt(preview.required_funding_atoms)-BigInt(preview.funding_balance_atoms);return (difference>0n?difference:0n).toString()}

export function sweepJobStateText(job:SweepJob){return sweepStateText(job.state==='complete'&&!sweepFinished(job)?'review_required':job.state)}

/** A wallet's declared networks are the authority; another configured RPC must not expand them. */
export function treasuryChains(wallet:(Pick<CryptoWallet,'supported_chain_ids'>&Partial<Pick<CryptoWallet,'mode'>>)|undefined,chains:CryptoChain[]):CryptoChain[]{
  return [...new Set(wallet?.supported_chain_ids||[])].filter(id=>supportedCryptoChain(id)&&(wallet?.mode!=='watch_only'||id!==SOLANA_CHAIN_ID)).flatMap(id=>chains.filter(chain=>chain.chain_id===id));
}
export function treasuryChainID(current:string,chains:CryptoChain[]):string{
  return chains.some(chain=>chain.id===current)?current:chains.find(chain=>chain.has_rpc)?.id||chains[0]?.id||'';
}
export interface SweepInput {fee_mode?:SweepFeeMode;gas_price_atoms?:string;chain_id:string;asset_id:string;destination:string;min_atoms:string;max_gas_atoms:string;address_ids?:string[]}
export function treasurySweepInput(chain:CryptoChain|undefined,asset:CryptoConfiguredAsset|undefined,destination:string,minimum:string,selected:string[]):SweepInput{
  if(!chain||!asset||asset.chain_id!==chain.chain_id)throw new Error(t('请选择网络与币种'));
  destination=destination.trim();
  if(!cryptoAddress(chain.chain_id,destination,true))throw new Error(t('请输入有效的收款地址'));
  return {chain_id:chain.id,asset_id:asset.id,destination,min_atoms:tokenAtoms(minimum,asset.decimals),max_gas_atoms:'',...(selected.length?{address_ids:[...new Set(selected)]}:{})};
}
export function treasuryFeeSelection(chain:CryptoChain|undefined,mode:SweepFeeMode,customGwei:string):Pick<SweepInput,'fee_mode'|'gas_price_atoms'>{
 if(chain?.chain_id===SOLANA_CHAIN_ID)return {};
 if(!['economy','standard','priority','custom'].includes(mode))throw new Error(t('请选择费用档位'));
 if(mode!=='custom')return {fee_mode:mode};
 const gas_price_atoms=tokenAtoms(customGwei.trim(),9);
 if(BigInt(gas_price_atoms)<=0n)throw new Error(t('Gas 单价必须大于零'));
 return {fee_mode:mode,gas_price_atoms};
}
/** Confirmation can only authorize the exact wallet, network, asset, recipient and address selection reviewed. */
export function treasuryPreviewMatches(preview:SweepPreview,walletID:string,input:SweepInput):boolean{
  return preview.wallet_id===walletID&&preview.chain_id===input.chain_id&&preview.asset_id===input.asset_id&&sameCryptoAddress(input.chain_id==='solana'?SOLANA_CHAIN_ID:1,preview.destination,input.destination)&&preview.min_atoms===input.min_atoms&&(preview.fee_mode||'standard')===(input.fee_mode||'standard')&&(input.fee_mode!=='custom'||preview.gas_price_atoms===input.gas_price_atoms)&&(!input.address_ids||preview.items.every(item=>input.address_ids!.includes(item.address_id)));
}
