import { clearHotWallet, createHotWallet, importHotWallet, type HotWalletMaterial } from './walletCore';

export interface CryptoWallet {
  id: string;
  name: string;
  mode: 'hot' | 'watch_only';
  xpub: string;
  path: string;
  first_address: string;
  engine: 'trust-wallet-core' | 'external-xpub';
  engine_version: string;
  enabled: boolean;
  revision: number;
  next_index: number;
  created: number;
  backup_confirmed: boolean;
  recovery_required: boolean;
  supported_chain_ids: number[];
  funding_address: string;
  funding_path: string;
}
export interface CryptoAddress { id: string; wallet_id: string; index: number; path: string; address: string; label: string; created: number }
export interface CryptoAddressPage { wallet: CryptoWallet; items: CryptoAddress[]; next_after: number | null }
export interface CryptoBackup { format: 'guangyue-crypto-wallet-v1'; data: string }
export type WalletAPI = <T>(path: string, method?: string, body?: unknown) => Promise<T>;
// Matches the server's 16 MiB restore request envelope; exported 8 MiB payloads expand to <15 MiB.
export const CRYPTO_BACKUP_FILE_LIMIT = 16 * 1024 * 1024;
export const MAX_WALLET_NEXT_INDEX = 2 ** 31;
export const CRYPTO_WALLET_CHAINS = [
  { chain_id: 56, name: 'BNB Smart Chain', native_symbol: 'BNB' },
  { chain_id: 1, name: 'Ethereum', native_symbol: 'ETH' },
] as const;
export function walletSupportsChain(wallet: CryptoWallet | undefined, chainID: number): boolean {
  return !!wallet?.supported_chain_ids?.includes(chainID);
}
export function walletChains(wallet: CryptoWallet) {
  return CRYPTO_WALLET_CHAINS.filter(chain => walletSupportsChain(wallet, chain.chain_id));
}
export function validateWalletChains(ids: number[]): number[] {
  const unique = [...new Set(ids)];
  if (!unique.length || unique.some(id => !CRYPTO_WALLET_CHAINS.some(chain => chain.chain_id === id))) throw new Error('请选择钱包支持的网络');
  return unique;
}

export function mergeCryptoWallets(current: CryptoWallet[], incoming: CryptoWallet[]): CryptoWallet[] {
  const merged = new Map(current.map(wallet => [wallet.id, wallet]));
  for (const wallet of incoming) {
    const existing = merged.get(wallet.id);
    if (!existing || wallet.revision >= existing.revision) merged.set(wallet.id, wallet);
  }
  return [...merged.values()];
}

export function parseCryptoBackup(text: string): CryptoBackup {
  if (!text || text.length > CRYPTO_BACKUP_FILE_LIMIT || new TextEncoder().encode(text).byteLength > CRYPTO_BACKUP_FILE_LIMIT) throw new Error('钱包备份文件无效或过大');
  let value: unknown;
  try { value = JSON.parse(text); } catch { throw new Error('钱包备份文件无效或过大'); }
  if (!value || typeof value !== 'object' || !('format' in value) || !('data' in value)
    || value.format !== 'guangyue-crypto-wallet-v1' || typeof value.data !== 'string' || !value.data.length) {
    throw new Error('钱包备份文件无效或过大');
  }
  return { format: value.format, data: value.data };
}

/** Do not pass secrets through useCommerce.run(), whose request fingerprint retains its body. */
export async function saveGeneratedWallet(
  api: WalletAPI,
  input: { name: string; password: string; operation_id: string; risk_ack: boolean; supported_chain_ids: number[]; import?:boolean },
  generate: () => Promise<HotWalletMaterial> = createHotWallet,
): Promise<CryptoWallet> {
  let material: HotWalletMaterial | undefined;
  let payload: Record<string, unknown> | undefined;
  try {
    if (!input.risk_ack) throw new Error('请先确认热钱包的资金保管方式');
    const supported_chain_ids = validateWalletChains(input.supported_chain_ids);
    material = await generate();
    payload = { ...input, supported_chain_ids, ...material };
    return await api<CryptoWallet>('/wallets/hot', 'POST', payload);
  } finally {
    clearHotWallet(material);
    if (payload) { payload.mnemonic = ''; payload.password = ''; }
    input.password = '';
  }
}

export function validateBackupPassword(password: string, confirmation: string, adminPassword: string): void {
  if (password.length < 12) throw new Error('备份口令至少需要 12 个字符');
  if (password !== confirmation) throw new Error('两次输入的备份口令不一致');
  if (password === adminPassword) throw new Error('备份口令不能与管理员密码相同');
}

export function canAllocate(wallet: CryptoWallet): boolean {
  return wallet.enabled && wallet.next_index < MAX_WALLET_NEXT_INDEX && !wallet.recovery_required && (wallet.mode === 'watch_only' || wallet.backup_confirmed);
}

export interface WalletFormOperation {publicKey:string;id:string;revision:number;enabled:boolean}
/** The target is the copy reviewed when the dialog opened, never a newer list row.
 * Preserve the same intent and CAS revision when retrying an uncertain response. */
export function walletFormOperation(publicKey:string,target:Pick<CryptoWallet,'revision'|'enabled'>|undefined,generate:()=>string,current?:WalletFormOperation,newSeed=false):WalletFormOperation{
 if(current?.publicKey===publicKey&&!newSeed)return current;
 return {publicKey,id:generate(),revision:target?.revision??0,enabled:target?.enabled??false};
}


export interface CryptoWalletDeletion {id:string;deleted:true}
/** Permanent deletion never sends keys and reuses one public operation ID after uncertainty. */
export async function deleteCryptoWallet(api:WalletAPI,input:{wallet:Pick<CryptoWallet,'id'|'revision'>;password:string;operation_id:string;delete_ack:boolean}):Promise<CryptoWalletDeletion>{
 let payload:Record<string,unknown>|undefined;
 try{
  if(!input.password||!input.operation_id||!Number.isSafeInteger(input.wallet.revision)||input.wallet.revision<0)throw new Error('请刷新钱包并输入管理员密码');
  if(input.delete_ack!==true)throw new Error('请确认永久删除将清除面板中的钱包和加密密钥');
  payload={password:input.password,revision:input.wallet.revision,operation_id:input.operation_id,delete_ack:true,permanent_delete_ack:true};
  const result=await api<CryptoWalletDeletion>('/wallets/'+encodeURIComponent(input.wallet.id)+'/delete','POST',payload);
  if(result.id!==input.wallet.id||result.deleted!==true)throw new Error('删除结果与所选钱包不一致，请刷新核对');
  return result;
 }finally{input.password='';if(payload)payload.password=''}
}

export async function saveImportedWallet(api:WalletAPI,input:{name:string;password:string;operation_id:string;risk_ack:boolean;supported_chain_ids:number[];mnemonic:string},derive:(value:string)=>Promise<HotWalletMaterial>=importHotWallet):Promise<CryptoWallet>{
 try{return await saveGeneratedWallet(api,{name:input.name,password:input.password,operation_id:input.operation_id,risk_ack:input.risk_ack,supported_chain_ids:input.supported_chain_ids,import:true},()=>derive(input.mnemonic))}finally{input.mnemonic='';input.password=''}
}
