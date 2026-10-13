import type {CryptoBalance} from './cryptoTreasury';
import {tokenAmount} from './cryptoPayments';
import {cryptoAddress,solanaAddress,SOLANA_CHAIN_ID,PLASMA_CHAIN_ID} from './cryptoNetworks';
export type PortfolioFilter='all'|'funded'|'empty'|'failed';
export interface PortfolioBalance extends CryptoBalance {label:string;checked_at:number;last_success_at:number;block_number:number;error:string;stale:boolean;funded:boolean}
export interface PortfolioSnapshot {
 wallet_id:string;chain_id:string;native_symbol:string;native_decimals?:number;watch_only:boolean;operations_supported:boolean;unavailable_reason:string;
 funding_address:string;funding_path:string;funding_balance_atoms:string;funding_checked_at:number;funding_error:string;
 native_balance_atoms:string;tokens:Array<{asset_id:string;symbol:string;decimals:number;balance_atoms:string}>;
 total_addresses:number;checked_addresses:number;funded_addresses:number;empty_addresses:number;failed_addresses:number;
 complete:boolean;stale:boolean;refreshing:boolean;checked_at:number;last_attempt_at:number;block_number:number;block_hash:string;finality:string;
 gas_price_atoms:string;reference_native_transfer_fee_atoms:string;gas_checked_at:number;gas_error:string;errors:string[];
 items:PortfolioBalance[];next_after:number|null;filter:PortfolioFilter;
}
export interface PortfolioContext {walletID:string;chainID:string;filter:PortfolioFilter}
export interface PortfolioReadState {snapshot?:PortfolioSnapshot;items:PortfolioBalance[];nextAfter:number|null;loading:boolean;loaded:boolean;error:string}
export type PortfolioAPI=<T>(path:string,method?:string,body?:unknown,options?:RequestInit)=>Promise<T>;
export function portfolioPath(context:PortfolioContext,after?:number|null):string {
 const query=new URLSearchParams({chain_id:context.chainID,filter:context.filter});
 if(after!=null)query.set('after',String(after));
 return '/wallets/'+encodeURIComponent(context.walletID)+'/portfolio?'+query;
}
export const portfolioAmount=(atoms:string|undefined,decimals=18)=>tokenAmount(atoms||'',decimals);
export function portfolioStatus(snapshot?:PortfolioSnapshot,error=''):'loading'|'refreshing'|'stale'|'partial'|'failed'|'ready'{
 if(!snapshot)return error?'failed':'loading';
 if(snapshot.refreshing)return 'refreshing';
 if(!snapshot.complete&&snapshot.checked_addresses>0&&snapshot.checked_addresses<snapshot.total_addresses)return 'partial';
 const hasSuccess=snapshot.checked_addresses>0||snapshot.funding_checked_at>0||snapshot.gas_checked_at>0||snapshot.items.some(row=>row.last_success_at>0)||(snapshot.total_addresses>0&&/^\d+$/.test(snapshot.native_balance_atoms));
 if(snapshot.stale||error)return hasSuccess?'stale':'failed';
 if(!snapshot.complete)return hasSuccess?'partial':'failed';
 return 'ready';
}
/** Explorer domains and EIP-681 network IDs come from a known chain, never from RPC response content. */
const explorers:Record<number,string>={1:'https://etherscan.io',56:'https://bscscan.com',8453:'https://basescan.org',42161:'https://arbiscan.io',10:'https://optimistic.etherscan.io',[PLASMA_CHAIN_ID]:'https://plasmascan.to',[SOLANA_CHAIN_ID]:'https://solscan.io'};
export function chainExplorer(chainID:number,address:string,kind:'address'|'tx'='address'):string {
 if(chainID===SOLANA_CHAIN_ID){const valid=kind==='address'?solanaAddress(address):/^[1-9A-HJ-NP-Za-km-z]{87,88}$/.test(address);return valid?explorers[chainID]+'/'+(kind==='address'?'account':'tx')+'/'+address:''}
 return explorers[chainID]&&new RegExp(kind==='address'?'^0x[0-9a-fA-F]{40}$':'^0x[0-9a-fA-F]{64}$').test(address)?explorers[chainID]+'/'+kind+'/'+address:'';
}
export function gasReceiveURI(chainID:number,address:string):string {
 if(chainID===SOLANA_CHAIN_ID)return solanaAddress(address)?'solana:'+address:'';
 return Number.isSafeInteger(chainID)&&chainID>0&&cryptoAddress(chainID,address)?'ethereum:'+address+'@'+chainID:'';
}
export function addressHasAsset(row:PortfolioBalance,assetID:string):boolean {
 if(!assetID)return true;
 const amount=assetID==='native'?row.native_atoms:row.tokens.find(token=>token.asset_id===assetID)?.balance_atoms;
 return !!amount&&/^\d+$/.test(amount)&&BigInt(amount)>0n;
}
/** One context and one flight: errors retain the last usable snapshot, page totals always remain server aggregates. */
export function createPortfolioReader(api:PortfolioAPI,publish:(state:PortfolioReadState)=>void){
 let context:PortfolioContext={walletID:'',chainID:'',filter:'all'},generation=0,request:AbortController|undefined,flight:Promise<void>|undefined;
 let state:PortfolioReadState={items:[],nextAfter:null,loading:false,loaded:false,error:''};
 const update=(changes:Partial<PortfolioReadState>)=>{state={...state,...changes};publish(state)};
 function cancel(){generation++;request?.abort();request=undefined;flight=undefined;update({loading:false})}
 function reset(next:PortfolioContext){cancel();context={...next};state={items:[],nextAfter:null,loading:false,loaded:false,error:''};publish(state)}
 function read(mode:'summary'|'detail'|'append'='detail'):Promise<void>{
  if(!context.walletID||!context.chainID||(mode==='append'&&state.nextAfter==null))return Promise.resolve();
  if(flight)return flight;
  const captured={...context},current=++generation,controller=new AbortController();request=controller;
  const lastIndex=state.items.at(-1)?.index??-1;update({loading:true});
  const run=(async()=>{
   try{
    let after=mode==='append'?state.nextAfter:undefined,items=mode==='append'?[...state.items]:[],page:PortfolioSnapshot;
    do{
     page=await api<PortfolioSnapshot>(portfolioPath(captured,after),'GET',undefined,{signal:controller.signal});
     if(current!==generation||controller.signal.aborted)return;
     if(page.wallet_id!==captured.walletID||page.chain_id!==captured.chainID||page.filter!==captured.filter)throw new Error('Portfolio response does not match requested wallet and network');
     items.push(...page.items);
     if(mode!=='detail'||page.next_after==null||page.next_after>=lastIndex)break;
     if(after!=null&&page.next_after<=after)throw new Error('Portfolio pagination did not advance');
     after=page.next_after;
    }while(true);
    const replaceRows=mode!=='summary'||!state.loaded;
    update({snapshot:page,error:'',...(replaceRows?{items:[...new Map(items.map(item=>[item.address_id,item])).values()],nextAfter:page.next_after,loaded:true}:{})});
   }catch(reason){if(current===generation&&!controller.signal.aborted){const message=reason instanceof Error?reason.message:String(reason);update({error:message,items:state.items.map(row=>({...row,stale:true})),...(state.snapshot?{snapshot:{...state.snapshot,complete:false,stale:true,refreshing:false,errors:[...new Set([...(state.snapshot.errors||[]),message])]}}:{})})}}
   finally{if(current===generation){flight=undefined;request=undefined;update({loading:false})}}
  })();flight=run;return run;
 }
 return {read,cancel,reset,getState:()=>state};
}
