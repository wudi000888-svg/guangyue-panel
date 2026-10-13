<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue';
import {ArrowUpRight,Check,ChevronDown,Coins,History,LoaderCircle,RefreshCw,Settings2,WalletCards,X} from 'lucide-vue-next';
import {useApi,isCancelled,ApiError,onSessionExpired,onAccessDenied} from '../lib/api';
import {operationID,stamp} from '../lib/commerce';
import {tokenAmount,cryptoOperationsSupported,type CryptoSettings} from '../lib/cryptoPayments';
import {canSignSweep,previewFundingFees,previewGasFees,previewTotalCost,previewFundingShortfall,sweepStateText,sweepJobStateText,sweepFinished,treasuryChains,treasuryChainID,treasurySweepInput,treasuryPreviewMatches,treasuryFeeSelection,type SweepFeeMode,type SweepPreview,type SweepJob} from '../lib/cryptoTreasury';
import {walletFundingAddress,type CryptoWallet} from '../lib/cryptoWallet';
import {nativeDecimals,SOLANA_CHAIN_ID} from '../lib/cryptoNetworks';
import WalletFeeFiat from './WalletFeeFiat.vue';
import WalletCollectionFees from './WalletCollectionFees.vue';
import {latestRequest,serialPoll} from '../lib/requests';
import {createGasReader,type CryptoGasSnapshot} from '../lib/cryptoGas';
import {createSweepEstimator,sweepEstimateCurrent,WALLET_GAS_POLL_MS} from '../lib/sweepEstimates';
import {t} from '../i18n';
import {usePaymentModule} from '../lib/paymentModule';
import {useWalletPortfolio} from '../composables/useWalletPortfolio';
import {portfolioStatus,chainExplorer} from '../lib/cryptoPortfolio';
import WalletAssetOverview from './WalletAssetOverview.vue';
import WalletAddressList from './WalletAddressList.vue';
import WalletGasPanel from './WalletGasPanel.vue';
import WalletAssetMark from './WalletAssetMark.vue';
const props=withDefaults(defineProps<{wallets:CryptoWallet[];active?:boolean;selectedWalletId?:string;requestedAction?:'assets'|'receive'|'gas'|'withdraw'|'activity';actionNonce?:number}>(),{active:true,actionNonce:0}),api=useApi('/commerce/crypto/admin');
const emit=defineEmits<{'update:selectedWalletId':[id:string];'manage-wallets':[]}>();
const {enabled:moduleEnabled,loaded:moduleLoaded}=usePaymentModule(()=>props.active);
const walletID=ref(''),chainID=ref(''),settings=ref<CryptoSettings>(),jobs=ref<SweepJob[]>([]),selected=ref<string[]>([]);
const autoRefresh=ref(true),jobsLoading=ref(false),jobsError=ref(''),settingsError=ref(''),copyError=ref(''),notice=ref('');

const wallet=computed(()=>props.wallets.find(w=>w.id===walletID.value));
const chains=computed(()=>treasuryChains(wallet.value,settings.value?.chains||[]));
const chain=computed(()=>chains.value.find(c=>c.id===chainID.value));
const assets=computed(()=>settings.value?.assets.filter(a=>a.chain_id===chain.value?.chain_id)||[]);


const dialog=ref<HTMLDialogElement>(),action=ref<'sweep'|'retry'|'cancel'|''>(''),retryJob=ref<SweepJob>(),assetID=ref(''),destination=ref(''),threshold=ref('0.1'),password=ref(''),preview=ref<SweepPreview>(),formError=ref(''),withdrawalError=ref(''),busy=ref(false),previewLoading=ref(false),now=ref(Date.now()/1000);
const quoteReady=ref(false),feeMode=ref<SweepFeeMode>('standard'),customGwei=ref('');
const asset=computed(()=>assets.value.find(a=>a.id===assetID.value));
const feeInputError=computed(()=>{try{treasuryFeeSelection(chain.value,feeMode.value,customGwei.value);return ''}catch(reason){return (reason as Error).message}});
const draftPreview=ref<SweepPreview>(),draftLoading=ref(false),draftError=ref('');
const gasSnapshot=ref<CryptoGasSnapshot>(),gasLoading=ref(false),gasError=ref('');
const estimator=createSweepEstimator((path,method,body,options)=>api<SweepPreview>(path,method,body,options),state=>{draftPreview.value=state.preview;draftLoading.value=state.loading;draftError.value=state.error});
const gasReader=createGasReader((path,method,body,options)=>api<CryptoGasSnapshot>(path,method,body,options),state=>{gasSnapshot.value=state.snapshot;gasLoading.value=state.loading;gasError.value=state.error});
const jobRequests=latestRequest(),settingsRequests=latestRequest();
let disposed=false,suspended=false,generation=0,trigger:HTMLElement|null=null,pending:{key:string;id:string}|undefined;
let jobFlight:Promise<void>|undefined,lastSummary=0,lastDetail=0,lastJobs=0;
const readable=()=>!disposed&&!suspended&&props.active&&!document.hidden;
const portfolio=useWalletPortfolio(api,walletID,chainID,chains,readable);
const {snapshot,balances,loading,loaded,error:balanceError,filter,summaries,summaryErrors,nextAfter,load}=portfolio;
const error=computed(()=>[settingsError.value,balanceError.value,jobsError.value,copyError.value].filter(Boolean).join(' '));
const writable=computed(()=>moduleEnabled.value&&!!chain.value&&cryptoOperationsSupported(chain.value.chain_id)&&snapshot.value?.chain_id===chainID.value&&canSignSweep(wallet.value?.mode||'',snapshot.value?.operations_supported===true)&&!!wallet.value?.backup_confirmed&&!wallet.value?.recovery_required);
const fundingAddress=computed(()=>gasSnapshot.value?.chain_id===chainID.value&&gasSnapshot.value.funding_address||snapshot.value?.chain_id===chainID.value&&snapshot.value.funding_address||walletFundingAddress(wallet.value,chain.value?.chain_id||0));
const previewDecimals=(value:SweepPreview)=>value.native_decimals??nativeDecimals(chain.value);
const workspace=ref<'overview'|'addresses'|'tasks'>('overview'),withdrawalOpen=ref(false),tabs=ref<HTMLElement>();
const gasDialog=ref<HTMLDialogElement>(),withdrawalDialog=ref<HTMLDialogElement>();
let drawerTrigger:HTMLElement|null=null;
const walletAccessNote=computed(()=>wallet.value?.mode==='watch_only'?t('此钱包仅保存公钥，可查看余额。资产转出请在持有私钥的外部钱包操作。'):wallet.value?.recovery_required?t('请先核对钱包恢复索引，再进行资产提取。'):wallet.value&&!wallet.value.backup_confirmed?t('请先下载并确认钱包备份，再进行资产提取。'):snapshot.value&&!snapshot.value.operations_supported?t(snapshot.value.unavailable_reason||'此网络尚未完成最终性验证，当前仅可查询余额。'):moduleLoaded.value&&!moduleEnabled.value?t('支付模块已关闭，暂停新提取；已批准任务与历史记录继续保留。'):'');
const workspaceTabs=[{id:'overview',label:'资产',icon:Coins},{id:'addresses',label:'收款地址',icon:WalletCards},{id:'tasks',label:'活动',icon:History}] as const;
const statusLabels={loading:'等待链上查询',refreshing:'正在更新',stale:'上次成功快照',partial:'部分地址待核对',failed:'暂无成功快照',ready:'余额已更新'};
function navigateTab(event:KeyboardEvent){const index=workspaceTabs.findIndex(tab=>tab.id===workspace.value);let next=index;if(event.key==='ArrowRight')next=(index+1)%workspaceTabs.length;else if(event.key==='ArrowLeft')next=(index+workspaceTabs.length-1)%workspaceTabs.length;else if(event.key==='Home')next=0;else if(event.key==='End')next=workspaceTabs.length-1;else return;event.preventDefault();workspace.value=workspaceTabs[next]!.id;void nextTick(()=>tabs.value?.querySelectorAll<HTMLButtonElement>('[role=tab]')[next]?.focus())}
async function showWithdrawal(){if(busy.value||!writable.value)return;if(!gasDialog.value?.open)drawerTrigger=document.activeElement as HTMLElement;gasDialog.value?.close();withdrawalOpen.value=true;await nextTick();withdrawalDialog.value?.showModal();document.getElementById('treasury-withdrawal-address')?.focus();void refreshEstimate()}
function hideWithdrawal(){estimator.setContext(undefined);withdrawalOpen.value=false;withdrawalDialog.value?.close();drawerTrigger?.focus();drawerTrigger=null}
function showGas(){if(busy.value)return;drawerTrigger=document.activeElement as HTMLElement;gasDialog.value?.showModal();void loadGas();void load()}
function hideGas(){gasDialog.value?.close();drawerTrigger?.focus();drawerTrigger=null}
function showReceive(){filter.value='all';workspace.value='addresses';void nextTick(()=>document.getElementById('wallet-panel-addresses')?.focus({preventScroll:true}))}
async function chooseAsset(value:{chainID:string;assetID:string}){if(busy.value)return;chainID.value=value.chainID;await nextTick();if(assets.value.some(item=>item.id===value.assetID))assetID.value=value.assetID;}
function cancelReads(){gasReader.cancel();estimator.cancel();portfolio.cancel();jobRequests.cancel();settingsRequests.cancel();jobFlight=undefined;jobsLoading.value=false}
async function loadSettings(){
  if(!readable())return;
  const request=settingsRequests.start();
  try{
    const value=await api<CryptoSettings>('/settings','GET',undefined,{signal:request.signal});
    if(!readable()||!settingsRequests.isCurrent(request))return;
    settings.value=value;settingsError.value='';
    await nextTick();await refresh();applyRequestedAction();
  }catch(reason){if(readable()&&settingsRequests.isCurrent(request)&&!isCancelled(reason))settingsError.value=t((reason as Error).message)}
}
function loadJobs():Promise<void>{
  if(!readable()||!walletID.value)return Promise.resolve();
  if(jobFlight)return jobFlight;
  const request=jobRequests.start(),wid=walletID.value;jobsLoading.value=true;lastJobs=Date.now();
  const flight=(async()=>{
    try{const value=await api<{items:SweepJob[]}>('/sweeps?wallet_id='+encodeURIComponent(wid),'GET',undefined,{signal:request.signal});if(!readable()||!jobRequests.isCurrent(request))return;jobs.value=value.items;jobsError.value=''}
    catch(reason){if(readable()&&jobRequests.isCurrent(request)&&!isCancelled(reason))jobsError.value=t((reason as Error).message)}
    finally{if(jobRequests.isCurrent(request)){jobsLoading.value=false;jobFlight=undefined}}
  })();
  jobFlight=flight;return flight;
}
async function refresh(){lastSummary=Date.now();lastDetail=Date.now();await Promise.all([load(),portfolio.refreshSummaries(),loadJobs(),loadGas()]);applyRequestedAction()}
function body(){return {...treasurySweepInput(chain.value,asset.value,destination.value,threshold.value,selected.value),...treasuryFeeSelection(chain.value,feeMode.value,customGwei.value)}}
function chooseFee(value:SweepFeeMode){if(value==='custom'&&!customGwei.value){const reference=draftPreview.value?.fee_options?.find(item=>item.id==='standard')?.gas_price_atoms||gasSnapshot.value?.gas_price_atoms;customGwei.value=reference?tokenAmount(reference,9):''}feeMode.value=value}
async function loadGas(){if(!readable()||!wallet.value||!chain.value)return;gasReader.setContext(walletID.value,chainID.value);await gasReader.read();now.value=Date.now()/1000}
async function refreshEstimate(){
 if(!readable()||busy.value||!withdrawalOpen.value||action.value||!writable.value)return;
 try{estimator.setContext({walletID:walletID.value,input:body()});await estimator.read();now.value=Date.now()/1000}catch{estimator.setContext(undefined)}
}
const draftUsable=computed(()=>{try{return !!draftPreview.value&&!draftLoading.value&&!draftError.value&&sweepEstimateCurrent(draftPreview.value,walletID.value,body(),now.value)}catch{return false}});
let appliedActionNonce:number|undefined;
function applyRequestedAction(){
 if(!readable()||!wallet.value||!settings.value||!chain.value||props.actionNonce===appliedActionNonce||!props.requestedAction)return;
 if(props.selectedWalletId&&walletID.value!==props.selectedWalletId)return;
 if(props.requestedAction==='withdraw'&&(!snapshot.value||!moduleLoaded.value))return;
 appliedActionNonce=props.actionNonce;
 if(props.requestedAction==='gas')showGas();else if(props.requestedAction==='receive')showReceive();else if(props.requestedAction==='withdraw')void showWithdrawal();else workspace.value=props.requestedAction==='activity'?'tasks':'overview';
}
async function open(kind:'sweep'|'retry'|'cancel',job?:SweepJob){
  if(busy.value||!wallet.value||wallet.value.mode!=='hot')return;
  withdrawalError.value='';
  let frozen:SweepPreview|undefined;
  if(kind==='sweep')try{body();frozen=estimator.freeze();if(!frozen)throw new Error(t('请等待有效费用预估后再核对转账'))}catch(reason){withdrawalError.value=t((reason as Error).message);return}
  withdrawalDialog.value?.close();withdrawalOpen.value=false;
  generation++;action.value=kind;retryJob.value=job;formError.value='';password.value='';preview.value=frozen;quoteReady.value=!!frozen;pending=undefined;estimator.cancel();
  trigger=kind==='sweep'?drawerTrigger||document.activeElement as HTMLElement:document.activeElement as HTMLElement;drawerTrigger=null;await nextTick();dialog.value?.showModal();
  if(kind==='sweep'){await nextTick();dialog.value?.querySelector<HTMLElement>('.treasury-preview h4')?.focus({preventScroll:true})}
  else dialog.value?.querySelector<HTMLInputElement>('input')?.focus();
}
function close(){generation++;estimator.setContext(undefined);previewLoading.value=false;action.value='';password.value='';preview.value=undefined;quoteReady.value=false;formError.value='';dialog.value?.close();trigger?.focus();trigger=null}
async function estimate(){
  if(busy.value||previewLoading.value||!writable.value||action.value!=='sweep'||!readable())return;
  const context=generation;password.value='';quoteReady.value=false;previewLoading.value=true;formError.value='';pending=undefined;
  try{
    estimator.setContext({walletID:walletID.value,input:body()});await estimator.read();
    if(!readable()||context!==generation)return;
    const frozen=estimator.freeze();
    if(!frozen)throw new Error(t(estimator.getState().error||draftPreview.value?.unavailable_reason||'暂不满足提取条件'));
    preview.value=frozen;quoteReady.value=true;now.value=Date.now()/1000;
  }catch(reason){if(!disposed&&context===generation&&!isCancelled(reason))formError.value=t((reason as Error).message)}
  finally{if(context===generation)previewLoading.value=false}
}
async function submit(){
  if(busy.value||!action.value||!password.value)return;
  const context=generation,kind=action.value;busy.value=true;formError.value='';
  let payload:Record<string,unknown>,path:string;
  try{
    if((action.value==='retry'||action.value==='cancel')&&retryJob.value){path='/sweeps/'+encodeURIComponent(retryJob.value.id)+'/'+action.value;payload={}}
    else{
      if(!writable.value||!quoteReady.value||!preview.value?.can_submit)throw new Error(t(preview.value?.unavailable_reason||'暂不满足提取条件'));
      if(preview.value.expires_at<=Date.now()/1000)throw new Error(t('预估已过期，请更新费用后确认'));
      if(!treasuryPreviewMatches(preview.value,walletID.value,body()))throw new Error(t('提取预估与当前选择不一致，请重新预估'));
      path='/wallets/'+encodeURIComponent(walletID.value)+'/sweeps';payload={quote:preview.value.quote};
    }
    const key=path+JSON.stringify(payload);if(pending?.key!==key)pending={key,id:operationID()};
    const job=await api<SweepJob>(path,'POST',{...payload,password:password.value,operation_id:pending.id});
    if(disposed)return;
    if(walletID.value===job.wallet_id)jobs.value=[job,...jobs.value.filter(j=>j.id!==job.id)];
    pending=undefined;if(context===generation)close();
    notice.value=kind==='cancel'?t('任务已安全结束，历史交易与实际费用记录会保留。'):t('提取任务已提交，链上最终确认后才会显示完成。');
    await loadJobs();
  }catch(reason){if(!disposed&&context===generation&&!isCancelled(reason)){formError.value=t((reason as Error).message);if(reason instanceof ApiError&&reason.status<500)pending=undefined;void loadJobs()}}
  finally{password.value='';busy.value=false}
}
async function copy(value:string){try{await navigator.clipboard.writeText(value);copyError.value='';notice.value=t('已复制')}catch{copyError.value=t('复制失败，请手动选择内容')}}
function assetFor(job:SweepJob){return settings.value?.assets.find(a=>a.id===job.asset_id)}
function chainName(id:string|number){return settings.value?.chains.find(c=>c.id===id||c.chain_id===id)?.name||id}
watch(moduleEnabled,value=>{if(!value&&action.value!=='cancel')close()});
watch([assetID,destination,threshold,feeMode,customGwei,()=>selected.value.join('|')],()=>{estimator.setContext(undefined);preview.value=undefined;quoteReady.value=false;previewLoading.value=false;pending=undefined;password.value='';withdrawalError.value='';void refreshEstimate()});
watch(chains,values=>{chainID.value=treasuryChainID(chainID.value,values)},{immediate:true});
watch(assets,values=>{if(!values.some(value=>value.id===assetID.value))assetID.value=values[0]?.id||''},{immediate:true});
watch(walletID,()=>{jobRequests.cancel();jobFlight=undefined;jobsLoading.value=false;jobs.value=[];jobsError.value='';destination.value='';threshold.value='0.1'});
watch([walletID,chainID],()=>{feeMode.value='standard';customGwei.value='';gasReader.setContext(walletID.value,chainID.value);selected.value=[];notice.value='';hideWithdrawal();hideGas();close();void refresh()});
watch(()=>props.wallets,values=>{if(!values.some(w=>w.id===walletID.value))walletID.value=values.find(w=>w.id===props.selectedWalletId)?.id||values[0]?.id||''},{immediate:true});
watch(()=>props.selectedWalletId,id=>{if(id&&props.wallets.some(w=>w.id===id))walletID.value=id},{immediate:true});
watch(walletID,id=>{if(id!==props.selectedWalletId)emit('update:selectedWalletId',id)});
watch([()=>props.requestedAction,()=>props.actionNonce,()=>snapshot.value?.wallet_id,()=>moduleLoaded.value],()=>{void nextTick(applyRequestedAction)},{immediate:true});
watch(()=>props.active,active=>{close();if(active){void loadSettings()}else cancelReads()});
watch(autoRefresh,enabled=>{if(enabled)void refresh()});
function visibilityChanged(){if(document.hidden){password.value='';hideWithdrawal();hideGas();close();cancelReads();previewLoading.value=false}else if(props.active){void loadGas();if(!settings.value)void loadSettings();else if(autoRefresh.value)void refresh()}}
const clear=()=>{suspended=true;hideWithdrawal();hideGas();close();cancelReads();portfolio.clear();jobs.value=[]};
const unsub=onSessionExpired(clear),unsubAccess=onAccessDenied(clear);
const poll=serialPoll(async()=>{now.value=Date.now()/1000;if(!readable()||busy.value||!autoRefresh.value)return;if(!settings.value){await loadSettings();return}const time=Date.now(),work:Promise<void>[]=[];if((workspace.value==='addresses'||gasDialog.value?.open)&&time-lastDetail>=30000){lastDetail=time;work.push(load())}if(time-lastSummary>=15000){lastSummary=time;work.push(portfolio.refreshSummaries())}if(time-lastJobs>=30000){work.push(loadJobs())}await Promise.all(work)},()=>5000);
const marketPoll=serialPoll(async()=>{now.value=Date.now()/1000;if(readable()&&!busy.value)await loadGas()},()=>WALLET_GAS_POLL_MS);
const estimatePoll=serialPoll(refreshEstimate,()=>WALLET_GAS_POLL_MS);
onMounted(()=>{void loadSettings();poll.start();marketPoll.start();estimatePoll.start();document.addEventListener('visibilitychange',visibilityChanged)});
onUnmounted(()=>{disposed=true;hideWithdrawal();hideGas();cancelReads();close();poll.stop();marketPoll.stop();estimatePoll.stop();document.removeEventListener('visibilitychange',visibilityChanged);unsub();unsubAccess()});
</script>
<template>
<section class="crypto-treasury">
  <div class="wallet-account-toolbar">
   <div class="wallet-account-select"><span class="wallet-account-avatar"><WalletCards :size="19"/></span><label><span class="wallet-field-label">{{t('当前钱包')}}</span><select v-model="walletID" :aria-label="t('钱包')" :disabled="busy"><option v-for="w in wallets" :key="w.id" :value="w.id">{{w.name}}</option></select></label><ChevronDown :size="14"/></div>
   <span v-if="wallet" class="wallet-account-mode">{{wallet.mode==='watch_only'?t('只读'):t('热钱包')}}</span>
   <div class="wallet-account-actions"><button type="button" :aria-label="t('刷新余额')" :title="t('刷新余额')" :disabled="loading||jobsLoading" @click="settings?refresh():loadSettings()"><RefreshCw :size="17" :class="{spin:loading}"/></button><button type="button" :aria-label="t('钱包管理')" :title="t('钱包管理')" @click="emit('manage-wallets')"><Settings2 :size="17"/></button></div>
  </div>
  <nav class="wallet-network-filter" :aria-label="t('网络')"><button v-for="network in chains" :key="network.id" type="button" :aria-pressed="chainID===network.id" :class="{active:chainID===network.id}" :disabled="busy" @click="chainID=network.id"><WalletAssetMark :chain-id="network.chain_id" network :size="20"/><span>{{network.name}}</span></button></nav>
  <p v-if="error" class="wallet-feedback error" role="alert">{{error}}</p><p v-if="notice" class="wallet-feedback" role="status">{{notice}}</p>
  <div class="wallet-data-toolbar"><span v-if="snapshot" :class="portfolioStatus(snapshot)"><span class="wallet-data-dot"/>{{t(statusLabels[portfolioStatus(snapshot)])}}</span><span v-else>{{t('等待链上查询')}}</span><details class="wallet-view-options"><summary>{{t('更新选项')}}<ChevronDown :size="13"/></summary><div><label class="treasury-check"><input v-model="autoRefresh" type="checkbox"/>{{t('自动更新')}}</label><p>{{t('资产总览每 15 秒、地址详情每 30 秒；Gas 与有效转账预估每 5 秒查询，仅在页面可见时运行。')}}</p><p v-if="snapshot">{{t('已核对')}} {{snapshot.checked_addresses}} / {{snapshot.total_addresses}} · #{{snapshot.block_number}}<br/>{{t(snapshot.finality==='finalized'?'最终确认':'最新区块读数')}}</p></div></details></div>
  <details v-if="walletAccessNote" class="wallet-access-note"><summary>{{t('当前钱包操作提示')}}<ChevronDown :size="13"/></summary><p>{{walletAccessNote}}</p></details>
  <nav ref="tabs" class="treasury-workspace-tabs" role="tablist" :aria-label="t('钱包工作区')" @keydown="navigateTab"><button v-for="item in workspaceTabs" :id="'wallet-tab-'+item.id" :key="item.id" type="button" role="tab" :aria-selected="workspace===item.id" :aria-controls="'wallet-panel-'+item.id" :tabindex="workspace===item.id?0:-1" :class="{active:workspace===item.id}" @click="workspace=item.id">{{t(item.label)}}<span v-if="item.id==='tasks'&&jobs.some(job=>!['complete','cancelled'].includes(job.state))" class="wallet-task-count">{{jobs.filter(job=>!['complete','cancelled'].includes(job.state)).length}}</span></button></nav>
  <template v-if="wallet">
    <p v-if="settings&&!chains.length" class="treasury-empty">{{t('此钱包没有可用于归集的网络，请核对钱包支持的网络。')}}</p>
    <section v-show="workspace==='overview'" id="wallet-panel-overview" role="tabpanel" aria-labelledby="wallet-tab-overview"><WalletAssetOverview :chains="chains" :assets="settings?.assets||[]" :summaries="summaries" :errors="summaryErrors" :selected-chain="chainID" :selected-asset="assetID" :wallet-name="wallet.name" :loading="loading" :writable="writable" :busy="busy" :gas="gasSnapshot" :gas-error="gasError" :gas-loading="gasLoading" :now="now" @chain="!busy&&(chainID=$event)" @asset="chooseAsset" @receive="showReceive" @gas="showGas" @withdraw="showWithdrawal"/></section>
    <section v-show="workspace==='addresses'" id="wallet-panel-addresses" role="tabpanel" aria-labelledby="wallet-tab-addresses" tabindex="-1"><WalletAddressList v-model:filter="filter" v-model:selected="selected" :chain="chain" :assets="assets" :rows="balances" :snapshot="snapshot" :writable="writable" :busy="busy||!!action" :loading="loading" :loaded="loaded" :has-more="nextAfter!=null" @copy="copy" @more="load(true)" @withdraw="showWithdrawal"/></section>
    <dialog ref="withdrawalDialog" class="wallet-drawer treasury-entry-dialog" aria-labelledby="wallet-withdrawal-title" @cancel.prevent="hideWithdrawal" @close="withdrawalOpen&&hideWithdrawal()"><form v-if="wallet.mode==='hot'&&withdrawalOpen" class="treasury-withdrawal" @submit.prevent="open('sweep')">
      <div class="treasury-heading"><div><h4 id="wallet-withdrawal-title">{{t('归集提现')}}</h4><p>{{t('填写收款地址，系统自动估算费用，核对后用管理员密码确认。')}}</p></div><button type="button" :aria-label="t('关闭')" @click="hideWithdrawal"><X :size="20"/></button></div>
      <fieldset :disabled="busy">
        <label>{{t('提现收款地址')}}<input id="treasury-withdrawal-address" v-model.trim="destination" :placeholder="chain?.chain_id===SOLANA_CHAIN_ID?t('Solana 收款地址'):'0x…'" required spellcheck="false" autocomplete="off"/></label>
        <div class="treasury-selectors"><label>{{t('提取币种')}}<select v-model="assetID" required><option v-for="a in assets" :key="a.id" :value="a.id">{{a.symbol}}</option></select></label><label>{{t('每地址最低余额')}}<input v-model.trim="threshold" inputmode="decimal" required/></label></div>
        <p class="treasury-note">{{selected.length?t('仅提取已勾选地址中符合最低余额的资产。'):t('提取此钱包全部历史地址中符合最低余额的指定币种，不会兑换其他资产。')}}</p>
        <details class="treasury-note"><summary>{{t('归集与 Gas 规则')}}</summary><p>{{t('只有确认归集后才会支付 Gas；订单地址的当前稳定币余额须严格大于 0.1，才会自动补 Gas 或由手续费钱包代付。非订单地址请自行准备手续费。')}}</p><p>{{t('费用超过已批准上限时暂停并提示，等待管理员核对；不会因价格回落自动恢复，也不会自动扩大预算。')}}</p></details>
        <WalletCollectionFees :preview="draftPreview" :mode="feeMode" :custom-gwei="customGwei" :solana="chain?.chain_id===SOLANA_CHAIN_ID" :loading="draftLoading" :error="feeInputError" :now="now" @update:mode="chooseFee" @update:custom-gwei="customGwei=$event"/>
        <section class="treasury-live-estimate" aria-live="polite">
         <header><strong>{{t('转账费用预估')}}</strong><span v-if="draftLoading"><LoaderCircle :size="13" class="spin"/>{{t('正在估算')}}</span><span v-else>{{t('每 5 秒刷新')}}</span></header>
         <template v-if="draftPreview"><dl><div><dt>{{t('预计提取')}}</dt><dd>{{tokenAmount(draftPreview.total_atoms,asset?.decimals??18)}} {{asset?.symbol}}</dd></div><div><dt>{{t(draftPreview.fee_model==='solana'?'预计总成本':'预计总手续费')}}</dt><dd>{{tokenAmount(previewTotalCost(draftPreview),previewDecimals(draftPreview))}} {{draftPreview.native_symbol}}<WalletFeeFiat :atoms="previewTotalCost(draftPreview)" :decimals="previewDecimals(draftPreview)" :quote="draftPreview" :now="now" details/></dd></div><div v-if="draftPreview.fee_model==='solana'"><dt>{{t('其中交易手续费')}}</dt><dd>{{tokenAmount(draftPreview.total_gas_atoms,previewDecimals(draftPreview))}} {{draftPreview.native_symbol}}</dd></div><div v-else><dt>{{t('补 Gas 差额')}}</dt><dd>{{tokenAmount(draftPreview.total_topup_atoms,previewDecimals(draftPreview))}} {{draftPreview.native_symbol}}</dd></div><div v-if="draftPreview.total_rent_atoms&&draftPreview.total_rent_atoms!=='0'"><dt>{{t('新代币账户租金')}}</dt><dd>{{tokenAmount(draftPreview.total_rent_atoms,previewDecimals(draftPreview))}} {{draftPreview.native_symbol}}</dd></div></dl><p>{{t('最近预估')}} {{stamp(draftPreview.checked_at)}} · #{{draftPreview.block_number}}</p><p v-if="!draftPreview.can_submit" class="error">{{t(draftPreview.unavailable_reason)}}</p><p v-if="draftPreview.expires_at<=now" class="error">{{t('预估已过期，请更新费用后确认')}}</p></template>
         <p v-else>{{t('填写有效收款地址后自动估算费用。')}}</p><p v-if="draftError" class="error" role="status">{{t('查询失败，保留上次估计；恢复前不能确认转账。')}} {{t(draftError)}}</p><p>{{t('仅查询费用，不会转账或补 Gas。地址较多时预估可能超过 5 秒，请等待本次查询完成。')}}</p>
        </section>
        <p v-if="withdrawalError" class="error" role="alert">{{withdrawalError}}</p>
        <button type="submit" class="primary" :disabled="!writable||!asset||busy||!draftUsable"><ArrowUpRight :size="16"/>{{t('核对转账与费用')}}</button>
      </fieldset>
    </form></dialog>
    <dialog ref="gasDialog" class="wallet-drawer wallet-gas-drawer" aria-labelledby="wallet-gas-title" @cancel.prevent="hideGas"><header><h3 id="wallet-gas-title">{{t('充值 Gas')}}</h3><button type="button" :aria-label="t('关闭')" @click="hideGas"><X :size="20"/></button></header><WalletGasPanel :chain="chain" :snapshot="snapshot" :funding-address="fundingAddress" :watch-only="wallet.mode==='watch_only'" :writable="writable" :gas="gasSnapshot" :gas-error="gasError" :gas-loading="gasLoading" :now="now" @copy="copy" @withdraw="showWithdrawal"/><p v-if="copyError" class="error" role="alert">{{copyError}}</p><p v-else-if="notice" class="treasury-note" role="status">{{notice}}</p></dialog>
  </template>
  <section v-show="workspace==='tasks'" id="wallet-panel-tasks" role="tabpanel" aria-labelledby="wallet-tab-tasks" class="treasury-jobs"><div class="treasury-heading"><h4>{{t('提取任务')}}</h4><button type="button" :disabled="jobsLoading" @click="loadJobs"><RefreshCw :size="15"/>{{t('刷新进度')}}</button></div><p v-if="!jobs.length" class="treasury-empty">{{jobsLoading?t('正在读取归集任务…'):t('暂无归集任务，发起提现后在这里跟踪交易进度。')}}</p><article v-for="job in jobs" :key="job.id" class="treasury-job"><header><div><strong>{{chainName(job.chain_id)}} · {{assetFor(job)?.symbol}}</strong><small>{{stamp(job.created)}}</small></div><span :class="{complete:sweepFinished(job)}"><Check v-if="sweepFinished(job)" :size="15"/><LoaderCircle v-else-if="['queued','running'].includes(job.state)" :size="15" class="spin"/>{{sweepJobStateText(job)}}</span></header><p>{{t('完成地址')}} {{job.items.filter(i=>i.state==='complete').length}} / {{job.items.length}}</p><progress :value="job.items.filter(i=>i.state==='complete').length" :max="Math.max(1,job.items.length)" :aria-label="t('提取进度')"/><p v-if="job.error" class="error">{{t(job.error)}}</p><p v-if="job.state==='fee_review_required'" class="treasury-note" role="status">{{t('费用上涨，已暂停自动操作。核对并重试需要管理员密码，仍以原预算为限；若需提高预算，请安全结束剩余任务后重新预估并确认。')}}</p><p v-if="['waiting','fee_review_required','review_required'].includes(job.state)" class="treasury-note">{{t('任务保留原交易记录。排除余额或网络问题后可重试，已广播交易会继续核对，不会重复转账。')}}</p><details><summary>{{t('交易明细')}}</summary><p class="treasury-note">{{t('收款地址')}}<code>{{job.destination}}</code></p><div v-for="item in job.items" :key="item.id" class="treasury-job-item"><code>{{item.address}}</code><strong>{{tokenAmount(item.amount_atoms,assetFor(job)?.decimals??18)}} {{assetFor(job)?.symbol}}</strong><span>{{sweepStateText(item.state)}}</span><p v-if="item.error" class="error">{{t(item.error)}}</p><p v-if="item.gas_tx_hash">{{t('补 Gas 交易')}}<a v-if="chainExplorer(job.chain_id,item.gas_tx_hash,'tx')" :href="chainExplorer(job.chain_id,item.gas_tx_hash,'tx')" target="_blank" rel="noopener noreferrer"><code>{{item.gas_tx_hash}}</code></a><code v-else>{{item.gas_tx_hash}}</code></p><p v-if="item.sweep_tx_hash">{{t('转出交易')}}<a v-if="chainExplorer(job.chain_id,item.sweep_tx_hash,'tx')" :href="chainExplorer(job.chain_id,item.sweep_tx_hash,'tx')" target="_blank" rel="noopener noreferrer"><code>{{item.sweep_tx_hash}}</code></a><code v-else>{{item.sweep_tx_hash}}</code></p></div></details><button v-if="moduleEnabled&&wallet?.mode==='hot'&&job.can_retry===true" type="button" :disabled="busy" @click="open('retry',job)">{{t('核对并重试')}}</button><button v-if="wallet?.mode==='hot'&&job.can_cancel" type="button" :disabled="busy" @click="open('cancel',job)">{{t('结束此任务')}}</button></article></section>

  <dialog ref="dialog" class="treasury-dialog" aria-labelledby="treasury-dialog-title" @cancel.prevent="close" @close="action&&close()">
    <form v-if="action" @submit.prevent="submit">
      <header><div><span>{{wallet?.name}} · {{action!=='sweep'?chainName(retryJob?.chain_id||''):chain?.name}}</span><h3 id="treasury-dialog-title">{{action==='cancel'?t('结束提取任务'):action==='retry'?t('重试提取任务'):t('确认归集提现')}}</h3></div><button type="button" :aria-label="t('关闭')" @click="close"><X :size="20"/></button></header>
      <p v-if="formError" class="error" role="alert">{{formError}}</p>
      <fieldset :disabled="busy">
        <template v-if="action==='sweep'">
          <div class="treasury-recipient"><span>{{t('提现收款地址')}}</span><code>{{preview?.destination||destination}}</code><small>{{chain?.name}} · {{asset?.symbol}}</small></div>
          <p class="treasury-note">{{asset?.symbol}} · {{t('代币合约')}}<code>{{asset?.contract}}</code></p>
          <p v-if="previewLoading" class="treasury-note" role="status"><LoaderCircle :size="16" class="spin"/> {{t('正在估算链上费用…')}}</p>
          <button v-else type="button" @click="estimate"><RefreshCw :size="16"/>{{preview?t('更新费用预估'):t('重新估算费用')}}</button>
          <article v-if="preview" class="treasury-preview">
            <h4 tabindex="-1">{{t('确认提取明细')}}</h4>
            <dl>
              <div><dt>{{t('预计提取')}}</dt><dd>{{tokenAmount(preview.total_atoms,asset?.decimals??18)}} {{asset?.symbol}}</dd></div>
              <div><dt>{{t('代币转账笔数')}}</dt><dd>{{preview.items.length}}</dd></div>
              <div v-if="preview.fee_model!=='solana'"><dt>{{t('补 Gas 差额')}}</dt><dd>{{tokenAmount(preview.total_topup_atoms,previewDecimals(preview))}} {{preview.native_symbol}}</dd></div>
              <div v-if="preview.fee_model!=='solana'"><dt>{{t('补款交易手续费')}}</dt><dd>{{tokenAmount(previewFundingFees(preview),previewDecimals(preview))}} {{preview.native_symbol}}</dd></div>
              <div><dt>{{t('代币转账手续费')}}</dt><dd>{{tokenAmount(previewGasFees(preview),previewDecimals(preview))}} {{preview.native_symbol}}</dd></div>
              <div v-if="preview.total_rent_atoms&&preview.total_rent_atoms!=='0'"><dt>{{t('新代币账户租金')}}</dt><dd>{{tokenAmount(preview.total_rent_atoms,previewDecimals(preview))}} {{preview.native_symbol}}</dd></div>
              <div><dt>{{t(preview.fee_model==='solana'?'预计总成本':'预计总手续费')}}</dt><dd>{{tokenAmount(previewTotalCost(preview),previewDecimals(preview))}} {{preview.native_symbol}}<WalletFeeFiat :atoms="previewTotalCost(preview)" :decimals="previewDecimals(preview)" :quote="preview" :now="now" details/></dd></div>
              <div><dt>{{t('Gas 资金余额')}}</dt><dd>{{tokenAmount(preview.funding_balance_atoms,previewDecimals(preview))}} {{preview.native_symbol}}</dd></div>
              <div><dt>{{t('Gas 资金所需总额')}}</dt><dd>{{tokenAmount(preview.required_funding_atoms,previewDecimals(preview))}} {{preview.native_symbol}}</dd></div>
              <div v-if="previewFundingShortfall(preview)!=='0'"><dt>{{t('还需补充 Gas 资金')}}</dt><dd>{{tokenAmount(previewFundingShortfall(preview),previewDecimals(preview))}} {{preview.native_symbol}}</dd></div>
              <div><dt>{{t('批准费用上限')}}</dt><dd>{{tokenAmount(preview.max_gas_atoms,previewDecimals(preview))}} {{preview.native_symbol}}</dd></div>
            </dl>
            <p v-if="!preview.can_submit" class="error" role="alert">{{t(preview.unavailable_reason)}}</p>
            <p>{{preview.fee_model==='solana'?t('Solana 手续费由固定 SOL 地址支付；新代币账户租金计入预估总额，最终费用以链上收据为准。'):t('费用估算含有限余量，实际手续费以链上收据为准，可能有少量 Gas 留在来源地址。')}}</p><p>{{t('美元和人民币为行情近似值，行情时间独立于 Gas 查询时间，实际费用以原生币结算。')}}</p>
            <p>{{t('预估有效至')}} {{stamp(preview.expires_at)}}</p>
            <p>{{t('确认后按本次预估锁定费用上限；报价过期需重新预估并确认。')}}</p><p>{{t('此页已冻结本次收款地址、币种和报价，后台刷新不会替换您正在核对的交易。')}}</p>
            <p v-if="preview.expires_at<=now" class="error">{{t('预估已过期，请更新费用后确认')}}</p>
          </article>
        </template>
        <template v-else><p class="treasury-note">{{action==='cancel'?t('结束尚未执行的后续操作，已发生的手续费不会退回。存在未确认交易时不能结束任务。'):t('继续核对原任务。已签名或广播的交易不会重新生成，只有链上最终确认后才会完成。')}}</p><code>{{retryJob?.id}}</code></template>
        <label v-if="action!=='sweep'||preview?.can_submit">{{t('管理员当前密码')}}<input v-model="password" type="password" autocomplete="current-password" required/></label>
        <footer><button v-if="action!=='sweep'||preview?.can_submit" type="submit" class="primary" :disabled="busy||previewLoading||!password||(action==='sweep'&&(!quoteReady||!preview?.can_submit||preview.expires_at<=now||!preview.items.length))"><LoaderCircle v-if="busy" :size="16" class="spin"/>{{busy?t('提交中…'):action==='cancel'?t('确认结束'):action==='retry'?t('确认重试'):t('确认提取')}}</button><button type="button" @click="close">{{t('关闭')}}</button></footer>
      </fieldset>
    </form>
  </dialog>
</section>
</template>
<style scoped>
.crypto-treasury{display:grid;gap:17px;padding:20px;border:1px solid var(--border);border-radius:13px;min-width:0}.treasury-heading{display:flex;justify-content:space-between;gap:14px;align-items:flex-start}.treasury-heading h3{display:flex;align-items:center;gap:8px;font-size:17px;margin:0}.treasury-heading p,.treasury-note{font-size:12px;line-height:1.8;color:var(--secondary);margin:7px 0 0}.treasury-heading button{flex-shrink:0}.treasury-entry-dialog .treasury-heading{position:sticky;top:0;z-index:2;background:var(--surface);background:rgb(from var(--surface) r g b / 1);backdrop-filter:blur(12px);padding-block:6px;flex-wrap:nowrap;border-bottom:1px solid var(--border)}.treasury-heading h4,.gas-funding h4{font-size:14px;margin:0}.treasury-selectors{display:grid;grid-template-columns:1fr 1fr;gap:14px}.crypto-treasury label{font-size:12px;display:grid;gap:8px;margin:0;min-width:0}.crypto-treasury select,.crypto-treasury input{min-width:0;width:100%;min-height:44px}.gas-funding{display:grid;grid-template-columns:1fr auto;gap:14px;padding:17px;background:var(--accent-soft);border:1px solid var(--border);border-radius:10px;min-width:0}.gas-funding p{font-size:12px;line-height:1.8;margin:7px 0 0;color:var(--secondary)}.gas-balance{font-size:20px;align-self:center;overflow-wrap:anywhere}.gas-balance small{font-size:12px}.treasury-copy{display:flex;gap:7px;grid-column:1/-1;min-width:0}.treasury-copy input{font:12px monospace}.treasury-copy button{flex-shrink:0}.gas-funding>.treasury-note{grid-column:1/-1;margin:0}.treasury-balance-toolbar{display:flex;align-items:center;gap:15px;flex-wrap:wrap}.treasury-balance-toolbar>span{font-size:12px;color:var(--secondary)}.treasury-balance-toolbar>.primary{margin-left:auto}.crypto-treasury .treasury-check{display:flex;gap:8px;align-items:center}.treasury-check input,.treasury-address>input{width:16px;min-height:16px;flex-shrink:0}.treasury-empty{padding:20px;font-size:12px;color:var(--secondary);border:1px dashed var(--border);border-radius:9px;text-align:center}.treasury-balances{display:grid;gap:11px}.treasury-balances>article{padding:14px;border:1px solid var(--border);border-radius:10px;min-width:0}.treasury-address{display:flex;align-items:center;gap:10px}.treasury-address>div{flex:1;min-width:0;font-size:12px}.treasury-order-link{display:inline-block;margin-top:6px;font-size:12px;overflow-wrap:anywhere}.treasury-address code{display:block;overflow-wrap:anywhere;line-height:1.7;margin-top:4px}.treasury-balances dl{display:flex;gap:15px;flex-wrap:wrap;margin:12px 0 0;padding-top:10px;border-top:1px solid var(--border)}.treasury-balances dl>div{flex:1;min-width:100px}.treasury-balances dt{font-size:12px;color:var(--secondary)}.treasury-balances dd{font-size:13px;overflow-wrap:anywhere;margin:4px 0 0;font-variant-numeric:tabular-nums}.treasury-jobs{display:grid;gap:13px;border-top:1px solid var(--border);padding-top:18px}.treasury-job{padding:16px;border:1px solid var(--border);border-radius:10px;font-size:12px;min-width:0}.treasury-job header{display:flex;gap:12px;align-items:flex-start;justify-content:space-between}.treasury-job header>div{display:grid;gap:6px}.treasury-job header small{color:var(--secondary)}.treasury-job header>span{display:flex;gap:5px;align-items:center}.treasury-job .complete{color:var(--accent-text)}.treasury-job progress{width:100%;height:6px;accent-color:var(--accent)}.treasury-job details{margin:12px 0}.treasury-job summary{cursor:pointer;min-height:44px}.treasury-job code,.treasury-dialog code{display:block;overflow-wrap:anywhere;font-size:12px;line-height:1.8}.treasury-job-item{display:grid;gap:7px;margin-top:14px;padding-top:12px;border-top:1px solid var(--border)}.treasury-job-item p{margin:0;color:var(--secondary)}.treasury-dialog{width:min(650px,calc(100vw - 28px));max-height:calc(100dvh - 32px);overflow:auto;border:1px solid var(--border);border-radius:16px;padding:25px;background:var(--surface);color:var(--text);box-shadow:0 24px 80px #0005}.treasury-dialog::backdrop{background:#081a38cc;backdrop-filter:blur(4px)}.treasury-dialog form,.treasury-dialog fieldset{display:grid;gap:18px;min-width:0}.treasury-dialog fieldset{padding:0;margin:0;border:0}.treasury-dialog header{display:flex;justify-content:space-between;gap:14px;align-items:flex-start}.treasury-dialog header span{font-size:12px;color:var(--secondary)}.treasury-dialog h3{font-size:21px;margin:6px 0 0}.treasury-dialog button{min-height:44px}.treasury-preview{background:var(--surface-raised);border:1px solid var(--border);padding:16px;border-radius:11px;min-width:0}.treasury-preview h4{margin:0;font-size:14px}.treasury-preview dl{display:grid;gap:11px;margin:16px 0;font-size:12px}.treasury-preview dl>div{display:flex;justify-content:space-between;gap:20px;align-items:baseline}.treasury-preview dt{color:var(--secondary)}.treasury-preview dd{margin:0;text-align:right;overflow-wrap:anywhere;font-variant-numeric:tabular-nums}.treasury-preview p{font-size:12px;color:var(--secondary);line-height:1.8}.treasury-dialog footer{display:flex;gap:10px}.crypto-treasury .error{font-size:12px;line-height:1.8;overflow-wrap:anywhere}@media(max-width:600px){.crypto-treasury{padding:15px}.treasury-heading{flex-wrap:wrap}.treasury-selectors{grid-template-columns:1fr}.gas-funding{grid-template-columns:1fr}.gas-balance{font-size:18px}.treasury-balance-toolbar>.primary{margin:0;width:100%}.treasury-dialog{padding:18px}.treasury-dialog footer button{flex:1}.treasury-job header{flex-wrap:wrap}.treasury-preview dl>div{gap:12px}.treasury-preview dd{max-width:60%}}

.treasury-withdrawal{display:grid;gap:14px;padding:18px;border:1px solid var(--border);border-radius:12px;background:var(--surface-raised);min-width:0}.treasury-withdrawal fieldset{display:grid;gap:14px;border:0;margin:0;padding:0;min-width:0}.treasury-withdrawal h4{margin:0}.treasury-withdrawal .primary{justify-self:start;min-height:44px}.treasury-recipient{display:grid;gap:9px;padding:14px;border:1px solid var(--border);border-radius:10px;background:var(--accent-soft);min-width:0}.treasury-recipient span,.treasury-recipient small{color:var(--secondary);font-size:12px}.treasury-recipient code{font-size:13px}.treasury-preview .error{color:var(--danger)}@media(max-width:600px){.treasury-withdrawal{padding:14px}.treasury-withdrawal .primary{justify-self:stretch}.treasury-dialog footer{flex-wrap:wrap}}

.treasury-workspace-tabs{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:7px;padding:5px;background:var(--surface-raised);border:1px solid var(--border);border-radius:12px}.treasury-workspace-tabs button{display:flex;align-items:center;justify-content:center;gap:7px;min-width:0;min-height:44px;border:0;border-radius:8px;background:transparent;font-size:12px;color:var(--secondary)}.treasury-workspace-tabs button.active{background:var(--surface);color:var(--accent-text);box-shadow:0 2px 7px #1437650d}.treasury-workspace-tabs button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}.treasury-live-toolbar{display:flex;justify-content:space-between;align-items:center;gap:10px;font-size:12px;color:var(--secondary);flex-wrap:wrap}.crypto-treasury .treasury-live-toolbar>.treasury-check{display:flex;flex-direction:row;align-items:center;gap:8px;white-space:nowrap}.treasury-live-toolbar .treasury-check input{width:16px;min-height:16px}.treasury-snapshot-status{display:flex;justify-content:space-between;gap:8px;align-items:center;flex-wrap:wrap;font-size:12px;color:var(--secondary);padding:12px 14px;border:1px solid var(--border);border-radius:10px;background:var(--surface-raised)}.treasury-snapshot-status>span:first-child{display:flex;gap:5px;align-items:center}.treasury-snapshot-status.ready>span:first-child{color:var(--accent-text)}.treasury-snapshot-status.stale,.treasury-snapshot-status.partial,.treasury-snapshot-status.failed{border-color:color-mix(in srgb,var(--warning) 35%,var(--border))}.treasury-snapshot-status p{flex-basis:100%;color:var(--warning);margin:2px 0 0;line-height:1.8}.crypto-treasury [role=tabpanel]{min-width:0}@media(max-width:500px){.treasury-workspace-tabs{grid-template-columns:repeat(2,minmax(0,1fr))}.treasury-workspace-tabs button{min-height:46px}.treasury-live-toolbar>span{line-height:1.8}}
</style>
<style scoped>
.crypto-treasury{display:grid;gap:0;padding:0;border:0;border-radius:0;background:none}.wallet-account-toolbar{display:flex;align-items:center;gap:10px;min-width:0;margin:0 0 20px}.wallet-account-select{display:flex;align-items:center;gap:10px;max-width:390px;min-width:0}.wallet-account-avatar{display:grid;place-items:center;width:36px;height:36px;border-radius:50%;background:var(--accent-soft);color:var(--accent-text);flex-shrink:0}.wallet-account-select label{display:block;min-width:0;flex:1}.wallet-field-label{font-size:12px;color:var(--muted);display:block;margin:0 0 3px;letter-spacing:.3px}.wallet-account-select select{appearance:none;padding:0 22px 0 0;border:0;background:none;border-radius:0;font-size:14px;font-weight:600;min-height:25px;width:100%;text-overflow:ellipsis;color:var(--text)}.wallet-account-select>svg{flex-shrink:0;color:var(--muted);margin-left:-20px;pointer-events:none}.wallet-account-mode{font-size:12px;padding:4px 7px;border:1px solid var(--border);border-radius:5px;white-space:nowrap;color:var(--muted)}.wallet-account-actions{margin-left:auto;display:flex;align-items:center;gap:4px}.wallet-account-actions button{display:grid;place-items:center;min-height:44px;min-width:44px;padding:10px;border:0;background:none;color:var(--muted);border-radius:50%}.wallet-network-filter{display:flex;gap:8px;align-items:center;overflow:auto;scrollbar-width:none;padding-bottom:2px;margin-bottom:18px}.wallet-network-filter button{display:flex;align-items:center;gap:7px;min-height:44px;border:1px solid var(--border);background:none;padding:6px 11px;border-radius:22px;font-size:12px;white-space:nowrap;color:var(--muted)}.wallet-network-filter button.active{background:var(--surface-raised);border-color:var(--accent);color:var(--text)}.wallet-feedback{font-size:12px;line-height:1.7;padding:10px 0;overflow-wrap:anywhere;margin:0 0 5px}.wallet-data-toolbar{display:flex;align-items:center;justify-content:space-between;color:var(--muted);font-size:12px;margin-bottom:7px;min-width:0;position:relative}.wallet-data-toolbar>span{display:flex;align-items:center;gap:6px}.wallet-data-dot{width:5px;height:5px;background:var(--muted);border-radius:50%}.wallet-data-toolbar>.ready .wallet-data-dot{background:#26a17b}.wallet-view-options{position:relative}.wallet-view-options summary{display:flex;align-items:center;gap:5px;cursor:pointer;min-height:44px;list-style:none}.wallet-view-options summary::-webkit-details-marker{display:none}.wallet-view-options>div{position:absolute;right:0;top:44px;width:260px;max-width:80vw;z-index:5;border:1px solid var(--border);border-radius:12px;padding:14px 16px;background:var(--surface);box-shadow:0 14px 30px #102b491a;font-size:12px;line-height:1.8}.crypto-treasury .wallet-view-options .treasury-check{display:flex;flex-direction:row;gap:9px;align-items:center;min-height:44px}.wallet-view-options .treasury-check input{width:16px;height:16px;min-height:16px}.wallet-view-options p{margin:8px 0 0}.wallet-access-note{color:var(--muted);font-size:12px;margin-bottom:10px;border-bottom:1px solid var(--border);padding-bottom:5px}.wallet-access-note summary{display:flex;align-items:center;gap:7px;min-height:44px;cursor:pointer}.wallet-access-note p{line-height:1.8;margin:0 0 10px;color:var(--warning)}.treasury-workspace-tabs{display:flex;gap:25px;border:0;border-bottom:1px solid var(--border);border-radius:0;background:none;padding:0;margin:0 0 24px}.treasury-workspace-tabs button{justify-content:flex-start;position:relative;min-height:43px;font-size:12px;padding:0 0 11px;border-radius:0;color:var(--muted);background:none}.treasury-workspace-tabs button.active{background:none;box-shadow:none;color:var(--text);font-weight:550}.treasury-workspace-tabs button.active:after{content:'';position:absolute;left:0;right:0;height:2px;background:var(--accent);bottom:-1px}.wallet-task-count{font-size:12px;min-width:16px;padding:2px 4px;border-radius:5px;background:var(--accent-soft);color:var(--accent-text)}.treasury-jobs{border:0;padding:0;gap:0}.treasury-jobs>.treasury-heading{padding:0 0 15px;align-items:center}.treasury-jobs>.treasury-heading h4{font-size:15px}.treasury-job{border:0;border-bottom:1px solid var(--border);border-radius:0;padding:20px 0;margin:0}.treasury-job header strong{font-size:13px}.treasury-job header span{font-size:12px;background:var(--surface-raised);padding:4px 7px;border-radius:14px}.treasury-job p{font-size:12px;color:var(--muted)}.treasury-job progress{height:4px;margin:5px 0 7px}.treasury-job details{margin:8px 0}.treasury-job summary{min-height:44px;align-content:center;color:var(--secondary)}.treasury-job>button{min-height:44px;font-size:12px;margin:5px 6px 0 0}.wallet-drawer{width:min(490px,calc(100vw - 28px));max-height:calc(100dvh - 28px);box-sizing:border-box;overflow:auto;padding:24px;border:1px solid var(--border);border-radius:20px;background:var(--surface);color:var(--text);box-shadow:0 28px 90px #0005}.wallet-drawer::backdrop{background:#07152888;backdrop-filter:blur(6px)}.wallet-drawer>header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:22px}.wallet-drawer>header h3{font-size:18px;font-weight:600;margin:0}.wallet-drawer>header button{display:grid;place-items:center;min-height:44px;min-width:44px;border:0;background:none;padding:10px;color:var(--muted)}.wallet-drawer .treasury-withdrawal{padding:0;border:0;background:none;border-radius:0;gap:22px}.wallet-drawer .treasury-heading h4{font-size:19px;font-weight:600}.wallet-drawer .treasury-heading p{font-size:12px;line-height:1.8;margin-top:9px}.wallet-drawer .treasury-heading>button{border:0;background:none;min-height:44px;min-width:44px}.wallet-drawer .treasury-selectors{grid-template-columns:1fr 1fr}.wallet-drawer .treasury-withdrawal .primary{justify-self:stretch;width:100%;justify-content:center;min-height:46px}.wallet-drawer .treasury-withdrawal input,.wallet-drawer .treasury-withdrawal select{min-height:47px;border-radius:10px}.treasury-dialog{border-radius:20px;max-height:calc(100dvh - 28px)}.treasury-dialog h3{font-weight:600}.treasury-preview{border:0;background:var(--surface-raised);border-radius:14px;padding:18px}.treasury-preview dl{gap:14px}.treasury-recipient{border:0;border-radius:12px;background:var(--surface-raised)}@media(max-width:650px){.wallet-account-select{max-width:calc(100% - 125px)}.wallet-account-toolbar{gap:7px;margin-bottom:15px}.wallet-account-mode{font-size:8px;padding:4px 5px}.wallet-network-filter{margin-bottom:10px}.wallet-network-filter button{min-height:44px;font-size:12px;padding:8px 10px}.wallet-account-actions{gap:0}.wallet-account-actions button{min-width:44px;width:40px}.wallet-account-select select{font-size:13px}.wallet-drawer{padding:21px;width:calc(100vw - 24px)}.wallet-drawer .treasury-selectors{grid-template-columns:1fr}.treasury-workspace-tabs{gap:23px;margin-bottom:22px}.wallet-data-toolbar{margin-bottom:0}.wallet-view-options>div{right:0}.treasury-job{padding:17px 0}.treasury-jobs .treasury-heading{flex-wrap:wrap}}
</style>

<style scoped>
.treasury-live-estimate{display:grid;gap:10px;background:var(--surface-raised);padding:15px 16px;border-radius:12px;font-size:12px;min-width:0}.treasury-live-estimate header{display:flex;align-items:center;justify-content:space-between;gap:10px}.treasury-live-estimate header strong{font-size:12px;font-weight:550}.treasury-live-estimate header span{display:flex;align-items:center;gap:5px;color:var(--muted);font-size:12px}.treasury-live-estimate dl{display:grid;gap:10px;margin:3px 0}.treasury-live-estimate dl>div{display:flex;justify-content:space-between;gap:10px;align-items:baseline}.treasury-live-estimate dt{color:var(--muted)}.treasury-live-estimate dd{margin:0;text-align:right;max-width:65%;overflow-wrap:anywhere;font-variant-numeric:tabular-nums}.treasury-live-estimate p{line-height:1.8;color:var(--muted);font-size:12px;margin:0}.treasury-live-estimate p.error{color:var(--danger);font-size:12px}
</style>
