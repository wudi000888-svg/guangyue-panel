import {computed,onScopeDispose,ref,watch,type Ref} from 'vue';
import type {CryptoChain} from '../lib/cryptoPayments';
import {createPortfolioReader,type PortfolioAPI,type PortfolioFilter,type PortfolioReadState,type PortfolioSnapshot} from '../lib/cryptoPortfolio';
export function useWalletPortfolio(api:PortfolioAPI,walletID:Ref<string>,chainID:Ref<string>,chains:Ref<CryptoChain[]>,readable:()=>boolean){
 const filter=ref<PortfolioFilter>('funded'),state=ref<PortfolioReadState>({items:[],nextAfter:null,loading:false,loaded:false,error:''});
 const summaries=ref<Record<string,PortfolioSnapshot>>({}),summaryErrors=ref<Record<string,string>>({});
 const reader=createPortfolioReader(api,value=>{state.value=value;if(value.snapshot&&value.snapshot.wallet_id===walletID.value){summaries.value={...summaries.value,[value.snapshot.chain_id]:value.snapshot};summaryErrors.value={...summaryErrors.value,[value.snapshot.chain_id]:value.error}}else if(!value.snapshot&&chainID.value){summaryErrors.value={...summaryErrors.value,[chainID.value]:value.error}}});
 const others=new Map<string,ReturnType<typeof createPortfolioReader>>();
 function cancel(){reader.cancel();for(const value of others.values())value.cancel()}
 function clear(){cancel();summaries.value={};summaryErrors.value={};reader.reset({walletID:'',chainID:'',filter:filter.value});others.clear()}
 async function load(append=false){if(readable())await reader.read(append?'append':'detail')}
 async function refreshSummaries(){
  if(!readable()||!walletID.value)return;
  const work=chains.value.map(chain=>{
   if(chain.id===chainID.value)return reader.read('summary');
   let item=others.get(chain.id);
   if(!item){item=createPortfolioReader(api,value=>{if(value.snapshot&&value.snapshot.wallet_id===walletID.value)summaries.value={...summaries.value,[chain.id]:value.snapshot};summaryErrors.value={...summaryErrors.value,[chain.id]:value.error}});item.reset({walletID:walletID.value,chainID:chain.id,filter:'all'});others.set(chain.id,item)}
   return item.read('summary');
  });await Promise.all(work);
 }
 watch(walletID,()=>{cancel();summaries.value={};summaryErrors.value={};others.clear()});
 watch([walletID,chainID,filter],()=>{reader.reset({walletID:walletID.value,chainID:chainID.value,filter:filter.value});if(readable())void load()},{immediate:true});
 onScopeDispose(cancel);
 return {filter,state,snapshot:computed(()=>state.value.snapshot),balances:computed(()=>state.value.items),loading:computed(()=>state.value.loading),loaded:computed(()=>state.value.loaded),error:computed(()=>state.value.error),nextAfter:computed(()=>state.value.nextAfter),summaries,summaryErrors,load,refreshSummaries,cancel,clear};
}
