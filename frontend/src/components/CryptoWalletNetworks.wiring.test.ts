import fs from 'node:fs';
import {afterEach,expect,it,vi} from 'vitest';
import * as Vue from 'vue';
import {parse} from '@vue/compiler-sfc';
import {compile} from '@vue/compiler-dom';
import {transpileModule,ScriptTarget,ModuleKind} from 'typescript';
import {CRYPTO_WALLET_CHAINS} from '../lib/cryptoWallet';
const template=parse(fs.readFileSync(new URL('./CryptoWalletSettings.vue',import.meta.url),'utf8')).descriptor.template!.content;
const compiled=transpileModule(compile(template,{mode:'function'}).code,{compilerOptions:{target:ScriptTarget.ES2022,module:ModuleKind.None}}).outputText;
const render=new Function('Vue',compiled)(Vue) as (context:Record<string,unknown>,cache:unknown[])=>Vue.VNode;
function nodes(node:unknown):Vue.VNode[]{if(Array.isArray(node))return node.flatMap(nodes);if(!node||typeof node!=='object'||!('type'in node))return [];return [node as Vue.VNode,...nodes((node as Vue.VNode).children)]}
afterEach(()=>vi.restoreAllMocks());
it('the actual network manager locks old networks and lets existing hot wallets select Solana and Plasma',()=>{
 vi.spyOn(console,'warn').mockImplementation(()=>{});
 const context={section:'wallets',wallets:[],active:true,error:'',notice:'',listError:'',loaded:true,loading:false,busy:false,action:'networks',title:'管理支持网络',target:{id:'w',name:'wallet',mode:'hot',supported_chain_ids:[56]},formError:'',phase:'',mnemonic:'',password:'',supportedChainIDs:[56],creationChains:CRYPTO_WALLET_CHAINS,SOLANA_CHAIN_ID:101,t:(value:string)=>value,open:vi.fn(),close:vi.fn(),load:vi.fn(),submit:vi.fn()};
 const inputs=nodes(render(context,[])).filter(node=>node.type==='input'&&node.props?.type==='checkbox');
 expect(inputs.find(node=>node.props?.value===56)?.props?.disabled).toBe(true);expect(inputs.find(node=>node.props?.value===101)?.props?.disabled).toBe(false);expect(inputs.find(node=>node.props?.value===9745)?.props?.disabled).toBe(false);
});
