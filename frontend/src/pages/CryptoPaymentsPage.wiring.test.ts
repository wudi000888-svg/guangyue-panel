import fs from 'node:fs';
import {afterEach,expect,it,vi} from 'vitest';
import * as Vue from 'vue';
import {parse} from '@vue/compiler-sfc';
import {compile} from '@vue/compiler-dom';
import {applyWalletListChange,type WalletListChange} from '../lib/cryptoWalletList';
import type {CryptoWallet} from '../lib/cryptoWallet';
const source=fs.readFileSync(new URL('./CryptoPaymentsPage.vue',import.meta.url),'utf8');
const template=parse(source).descriptor.template!.content;
const render=new Function('Vue',compile(template,{mode:'function'}).code)(Vue) as (context:Record<string,unknown>,cache:unknown[])=>Vue.VNode;
function find(node:Vue.VNode,type:string):Vue.VNode|undefined{
 if(node.type===type)return node;
 if(Array.isArray(node.children))for(const child of node.children){if(child&&typeof child==='object'&&'type' in child){const found=find(child as Vue.VNode,type);if(found)return found}}
}
function context(section='wallets',detailsOpen=false){
 const value:Record<string,unknown>={section,detailsOpen,wallets:[],selectedWalletId:'',requestedAction:'assets',actionNonce:0,settings:undefined,loading:false,error:'',readiness:{tone:'muted',label:'状态',detail:''},cryptoWorkspaceSections:[{id:'treasury',label:'资产'},{id:'wallets',label:'钱包'},{id:'payments',label:'收款'}],t:(text:string)=>text,navigateSection:vi.fn(),selectSection:vi.fn(),load:vi.fn(),viewAssets:vi.fn(),viewWallet:vi.fn()};
 value.acceptWallets=(change:WalletListChange)=>{value.wallets=applyWalletListChange(value.wallets as CryptoWallet[],change)};
 return value;
}
afterEach(()=>vi.restoreAllMocks());
it('the actual manager template forwards committed wallets into the Gallery even without another GET',()=>{
 vi.spyOn(console,'warn').mockImplementation(()=>{});
 const state=context(),manager=find(render(state,[]),'CryptoWalletSettings');expect(manager).toBeDefined();
 expect(manager?.props?.onWalletsChanged).toBe(state.acceptWallets);
 const created={id:'created-wallet',revision:1,name:'Imported account',enabled:false,recovery_required:true} as CryptoWallet;
 manager!.props!.onWalletsChanged({items:[created],authoritative:false});
 state.section='treasury';const gallery=find(render(state,[]),'WalletPortfolioGallery');expect(gallery?.props?.wallets).toEqual([created]);
});
it('the detail template forwards permanent removal to the same parent wallet collection',()=>{
 vi.spyOn(console,'warn').mockImplementation(()=>{});
 const state=context('treasury',true),wallet={id:'delete-me',revision:2} as CryptoWallet;state.wallets=[wallet];
 const detail=find(render(state,[]),'CryptoWalletSettings');expect(detail?.props?.onWalletsChanged).toBe(state.acceptWallets);
 detail!.props!.onWalletsChanged({items:[],authoritative:false,deletedID:wallet.id});expect(state.wallets).toEqual([]);
});
