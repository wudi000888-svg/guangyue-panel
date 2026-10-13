import fs from 'node:fs';
import {afterEach,expect,it,vi} from 'vitest';
import * as Vue from 'vue';
import {parse} from '@vue/compiler-sfc';
import {compile} from '@vue/compiler-dom';
import {transpileModule,ScriptTarget,ModuleKind} from 'typescript';
import {tokenAmount} from '../lib/cryptoPayments';
import {previewFundingFees,previewGasFees,previewTotalCost,previewFundingShortfall} from '../lib/cryptoTreasury';
const template=parse(fs.readFileSync(new URL('./CryptoTreasury.vue',import.meta.url),'utf8')).descriptor.template!.content;
const compiled=transpileModule(compile(template,{mode:'function'}).code,{compilerOptions:{target:ScriptTarget.ES2022,module:ModuleKind.None}}).outputText;
const render=new Function('Vue',compiled)(Vue) as (context:Record<string,unknown>,cache:unknown[])=>Vue.VNode;
function nodes(node:unknown):Vue.VNode[]{if(Array.isArray(node))return node.flatMap(nodes);if(!node||typeof node!=='object'||!('type'in node))return [];return [node as Vue.VNode,...nodes((node as Vue.VNode).children)]}
function text(node:unknown):string{if(typeof node==='string')return node;if(Array.isArray(node))return node.map(text).join('');if(node&&typeof node==='object'&&'children'in node)return text(node.children);return ''}
afterEach(()=>vi.restoreAllMocks());
it('the compiled live and frozen confirmation templates pass their own sealed fee and fiat quote to the real display component',()=>{
 vi.spyOn(console,'warn').mockImplementation(()=>{});
 const chain={id:'solana',chain_id:101,name:'Solana',native_decimals:9,native_symbol:'SOL'},asset={id:'solana-usdc',symbol:'USDC',decimals:6,contract:'mint'};
 const preview={wallet_id:'w',chain_id:'solana',native_decimals:9,fee_model:'solana',destination:'recipient',total_atoms:'1230000',total_topup_atoms:'0',total_gas_atoms:'15000',total_rent_atoms:'2039280',funding_balance_atoms:'1000000000',required_funding_atoms:'2054280',max_gas_atoms:'3000000',can_submit:true,expires_at:200,checked_at:100,native_symbol:'SOL',items:[{gas_fee_atoms:'15000',funding_gas_atoms:'0'}],native_price_usd:'150',native_price_cny:'1000',price_checked_at:100,price_expires_at:160};
 const draftPreview={...preview,total_gas_atoms:'10000',native_price_usd:'151'};
 const context:Record<string,unknown>={walletID:'w',wallet:{id:'w',name:'Account',mode:'hot'},wallets:[],chainID:'solana',chain,chains:[chain],assets:[asset],asset,assetID:asset.id,settings:{assets:[asset]},workspace:'overview',workspaceTabs:[],jobs:[],balances:[],selected:[],snapshot:undefined,summaries:{},summaryErrors:{},error:'',notice:'',walletAccessNote:'',copyError:'',formError:'',withdrawalError:'',autoRefresh:true,busy:false,jobsLoading:false,loading:false,loaded:true,nextAfter:null,filter:'all',withdrawalOpen:true,draftPreview,draftLoading:false,draftError:'',draftUsable:true,preview,previewLoading:false,quoteReady:true,now:100,action:'sweep',retryJob:undefined,password:'',destination:'recipient',threshold:'0',feeMode:'standard',customGwei:'',feeInputError:'',gasSnapshot:undefined,gasError:'',gasLoading:false,fundingAddress:'',writable:true,moduleEnabled:true,SOLANA_CHAIN_ID:101,tokenAmount,previewDecimals:()=>9,previewFundingFees,previewGasFees,previewTotalCost,previewFundingShortfall,t:(value:string)=>value,stamp:(value:number)=>String(value),portfolioStatus:()=> 'ready',sweepFinished:()=>true,sweepJobStateText:()=>'',chainName:()=> 'Solana',assetFor:()=>asset};
 Object.assign(context,Object.fromEntries(['navigateTab','chooseFee','chooseAsset','showReceive','showGas','showWithdrawal','copy','loadJobs','hideWithdrawal','hideGas','open','close','estimate','submit','load','refresh','loadSettings','emit'].map(name=>[name,vi.fn()])));
 const vnode=render(context,[]),all=nodes(vnode),fiat=all.filter(node=>node.type==='WalletFeeFiat');
 expect(fiat).toHaveLength(2);expect(fiat[0]?.props?.atoms).toBe('2049280');expect(fiat[0]?.props?.decimals).toBe(9);expect(fiat[0]?.props?.quote).toBe(draftPreview);expect(fiat[1]?.props?.atoms).toBe('2054280');expect(fiat[1]?.props?.quote).toBe(preview);
 const content=text(vnode);expect(content).toContain('预计总成本');expect(content).toContain('其中交易手续费');expect(content).toContain('0.00205428');expect(content).toContain('新代币账户租金');expect(content).not.toContain('补款交易手续费');expect(content).not.toContain('补 Gas 差额');
 expect(content).toContain('严格大于 0.1');expect(content).toContain('不会因价格回落自动恢复');
 const paused={id:'job',state:'fee_review_required',can_retry:true,can_cancel:true,chain_id:101,asset_id:asset.id,created:100,items:[],destination:'recipient'};
 context.jobs=[paused];
 const retryButtons=(value:unknown)=>nodes(value).filter(node=>node.type==='button'&&text(node)==='核对并重试');
 expect(retryButtons(render(context,[]))).toHaveLength(1);
 context.jobs=[{...paused,state:'review_required',can_retry:false}];
 expect(retryButtons(render(context,[]))).toHaveLength(0);
});
