import fs from 'node:fs';
import {afterEach,expect,it,vi} from 'vitest';
import * as Vue from 'vue';
import {parse} from '@vue/compiler-sfc';
import {compile} from '@vue/compiler-dom';
import {transpileModule,ScriptTarget,ModuleKind} from 'typescript';
import {walletWithdrawAvailability} from '../lib/walletStatus';
import type {CryptoWallet} from '../lib/cryptoWallet';
const template=parse(fs.readFileSync(new URL('./WalletPortfolioGallery.vue',import.meta.url),'utf8')).descriptor.template!.content;
const compiled=transpileModule(compile(template,{mode:'function'}).code,{compilerOptions:{target:ScriptTarget.ES2022,module:ModuleKind.None}}).outputText;
const render=new Function('Vue',compiled)(Vue) as (context:Record<string,unknown>,cache:unknown[])=>Vue.VNode;
function text(node:unknown):string{if(typeof node==='string')return node;if(Array.isArray(node))return node.map(text).join('');if(node&&typeof node==='object'&&'children' in node)return text(node.children);return ''}
function findButton(node:Vue.VNode,label:string):Vue.VNode|undefined{if(node.type==='button'&&text(node.children)===label)return node;if(Array.isArray(node.children))for(const child of node.children)if(child&&typeof child==='object'&&'type' in child){const found=findButton(child as Vue.VNode,label);if(found)return found}}
afterEach(()=>vi.restoreAllMocks());
it.each([
 [false,true,{mode:'hot',backup_confirmed:true,recovery_required:false},'支付服务状态尚未确认'],
 [true,false,{mode:'hot',backup_confirmed:true,recovery_required:false},'支付模块已关闭'],
 [true,true,{mode:'watch_only',backup_confirmed:true,recovery_required:false},'只读钱包'],
 [true,true,{mode:'hot',backup_confirmed:false,recovery_required:false},'钱包备份'],
 [true,true,{mode:'hot',backup_confirmed:true,recovery_required:true},'恢复索引'],
])('the actual card disables withdrawal and visibly associates its reason (%s/%s)',(loaded,enabled,changes,reason)=>{
 vi.spyOn(console,'warn').mockImplementation(()=>{});
 const wallet={id:'one',name:'Account',enabled:true,next_index:0,...changes} as CryptoWallet;
 const gate=walletWithdrawAvailability(wallet,loaded,enabled);
 const context={wallets:[wallet],selectedWalletId:'',state:{loading:false,snapshots:{},errors:{}},settings:undefined,error:'',autoRefresh:true,paymentLoaded:loaded,paymentEnabled:enabled,t:(value:string)=>value,chains:()=>[],withdrawGate:()=>gate,walletStatus:()=>({label:'状态',tone:'muted'}),emit:vi.fn(),manualRefresh:vi.fn()};
 const vnode=render(context,[]),button=findButton(vnode,'归集提现');expect(button?.props?.disabled).toBe(true);expect(button?.props?.['aria-describedby']).toBe('wallet-withdraw-reason-one');expect(text(vnode)).toContain(reason);
});
