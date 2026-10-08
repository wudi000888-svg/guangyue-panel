import { clearHotWallet, createHotWallet, type HotWalletMaterial } from './walletCore';

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
}
export interface CryptoAddress { id: string; wallet_id: string; index: number; path: string; address: string; label: string; created: number }
export interface CryptoAddressPage { wallet: CryptoWallet; items: CryptoAddress[]; next_after: number | null }
export interface CryptoBackup { format: 'guangyue-crypto-wallet-v1'; data: string }
export type WalletAPI = <T>(path: string, method?: string, body?: unknown) => Promise<T>;
// Matches the server's 16 MiB restore request envelope; exported 8 MiB payloads expand to <15 MiB.
export const CRYPTO_BACKUP_FILE_LIMIT = 16 * 1024 * 1024;
export const MAX_WALLET_NEXT_INDEX = 2 ** 31;

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
  input: { name: string; password: string; operation_id: string; risk_ack: boolean },
  generate: () => Promise<HotWalletMaterial> = createHotWallet,
): Promise<CryptoWallet> {
  let material: HotWalletMaterial | undefined;
  let payload: Record<string, unknown> | undefined;
  try {
    if (!input.risk_ack) throw new Error('请先确认热钱包的资金保管方式');
    material = await generate();
    payload = { ...input, ...material };
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
