import type { WalletCore } from '@trustwallet/wallet-core';

export const WALLET_CORE_VERSION = '4.8.4';
export const HOT_WALLET_PATH = "m/44'/60'/0'";
type CoreFactory = () => Promise<WalletCore>;
let pending: Promise<WalletCore> | undefined;

/** Keep the official loader intact; Vite emits both files as same-origin assets. */
export function loadWalletCore(): Promise<WalletCore> {
  if (!globalThis.isSecureContext || !globalThis.crypto?.getRandomValues) {
    return Promise.reject(new Error('请通过 HTTPS 或本机安全连接创建钱包'));
  }
  if (pending) return pending;
  pending = new Promise<WalletCore>((resolve, reject) => {
    const globals = globalThis as typeof globalThis & { Module?: CoreFactory };
    const previous = globals.Module;
    const script = document.createElement('script');
    script.src = `${import.meta.env.BASE_URL}assets/wallet-core/${WALLET_CORE_VERSION}/wallet-core.js`;
    script.async = true;
    const cleanup = () => { globals.Module = previous; script.remove(); };
    script.onerror = () => { cleanup(); reject(new Error('钱包组件加载失败，请重试')); };
    script.onload = () => {
      const factory = globals.Module;
      cleanup();
      if (typeof factory !== 'function') { reject(new Error('钱包组件加载失败，请重试')); return; }
      factory()
        .then(resolve, () => reject(new Error('钱包组件初始化失败，请检查浏览器支持后重试')));
    };
    document.head.appendChild(script);
  }).catch(error => { pending = undefined; throw error; });
  return pending;
}

export interface HotWalletMaterial {
  mnemonic: string;
  xpub: string;
  path: string;
  first_address: string;
  engine_version: string;
}

/** Explicit creation sends the mnemonic to the encrypted server vault; other Core handles stay local. */
export function describeCoreWallet(core: WalletCore, wallet: InstanceType<WalletCore['HDWallet']>): HotWalletMaterial {
  return {
    mnemonic: wallet.mnemonic(),
    xpub: wallet.getExtendedPublicKeyAccount(core.Purpose.bip44, core.CoinType.ethereum, core.Derivation.default, core.HDVersion.xpub, 0),
    path: HOT_WALLET_PATH,
    first_address: wallet.getAddressForCoin(core.CoinType.ethereum),
    engine_version: WALLET_CORE_VERSION,
  };
}

export async function createHotWallet(): Promise<HotWalletMaterial> {
  const core = await loadWalletCore();
  const wallet = core.HDWallet.create(256, '');
  try { return describeCoreWallet(core, wallet); }
  finally { wallet.delete(); }
}

export function clearHotWallet(material: HotWalletMaterial | undefined): void {
  if (material) material.mnemonic = '';
}

/** Imported phrases stay in memory and use the same official derivation as generated wallets. */
export async function importHotWallet(mnemonic:string,load:()=>Promise<WalletCore>=loadWalletCore):Promise<HotWalletMaterial>{
 mnemonic=mnemonic.trim().replace(/\s+/g,' ');
 if(mnemonic.length>400||![12,24].includes(mnemonic.split(' ').length))throw new Error('请输入12或24个英文助记词，不支持附加密码');
 const core=await load();
 if(!core.Mnemonic.isValid(mnemonic))throw new Error('助记词校验失败，请核对单词和顺序');
 const wallet=core.HDWallet.createWithMnemonic(mnemonic,'');
 try{return describeCoreWallet(core,wallet)}finally{wallet.delete();mnemonic=''}
}
