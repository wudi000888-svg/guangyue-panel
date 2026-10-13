import {createPortfolioReader,type PortfolioAPI,type PortfolioSnapshot} from './cryptoPortfolio';
export interface GalleryTarget {walletID:string;chainID:string}
export interface WalletGalleryState {snapshots:Record<string,Record<string,PortfolioSnapshot>>;errors:Record<string,Record<string,string>>;loading:boolean}
export type WalletAction='assets'|'receive'|'gas'|'withdraw'|'activity';
/** At most two aggregate reads in flight; each endpoint supplies totals for every allocated address. */
export function createWalletGalleryReader(api:PortfolioAPI,publish:(state:WalletGalleryState)=>void){
 const readers=new Map<string,ReturnType<typeof createPortfolioReader>>();let state:WalletGalleryState={snapshots:{},errors:{},loading:false},flight:Promise<void>|undefined,epoch=0;
 const key=(target:GalleryTarget)=>target.walletID+'\0'+target.chainID;
 const update=(changes:Partial<WalletGalleryState>)=>{state={...state,...changes};publish(state)};
 function setTargets(targets:GalleryTarget[]){
  const wanted=new Set(targets.map(key));
  for(const [id,reader] of readers)if(!wanted.has(id)){readers.delete(id);reader.cancel()}
  const snapshots:WalletGalleryState['snapshots']={},errors:WalletGalleryState['errors']={};
  for(const target of targets){if(state.snapshots[target.walletID]?.[target.chainID]){snapshots[target.walletID]??={};snapshots[target.walletID]![target.chainID]=state.snapshots[target.walletID]![target.chainID]!}if(state.errors[target.walletID]?.[target.chainID]){errors[target.walletID]??={};errors[target.walletID]![target.chainID]=state.errors[target.walletID]![target.chainID]!}}
  update({snapshots,errors});
  for(const target of targets){const id=key(target);if(readers.has(id))continue;const reader=createPortfolioReader(api,value=>{if(!readers.has(id))return;const previous=state.snapshots[target.walletID]||{},problem=state.errors[target.walletID]||{};update({snapshots:{...state.snapshots,[target.walletID]:{...previous,...(value.snapshot?{[target.chainID]:value.snapshot}:{})}},errors:{...state.errors,[target.walletID]:{...problem,[target.chainID]:value.error}}})});readers.set(id,reader);reader.reset({walletID:target.walletID,chainID:target.chainID,filter:'all'})}
 }
 function cancel(){epoch++;for(const reader of readers.values())reader.cancel();flight=undefined;update({loading:false})}
 function clear(){const old=[...readers.values()];readers.clear();cancel();for(const reader of old)reader.cancel();update({snapshots:{},errors:{}})}
 function refresh():Promise<void>{
  if(flight)return flight;const current=epoch,queue=[...readers.entries()];let index=0;update({loading:true});
  const worker=async()=>{while(index<queue.length&&current===epoch){const [id,reader]=queue[index++]!;if(readers.get(id)===reader)await reader.read('summary')}};
  const run=Promise.all([worker(),worker()]).then(()=>{}).finally(()=>{if(current===epoch){flight=undefined;update({loading:false})}});flight=run;return run;
 }
 return {setTargets,refresh,cancel,clear,getState:()=>state};
}
