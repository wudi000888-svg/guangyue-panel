import { mergeCryptoWallets, type CryptoWallet } from './cryptoWallet';

/** State transition for a successful list read. A valid response clears only
 * the list transport error, leaving action feedback intact. */
export function applyWalletList(result: { items: CryptoWallet[] }, current: CryptoWallet[]) {
  return { wallets: mergeCryptoWallets(current, result.items), loaded: true, listError: '' };
}

export function shouldResumeWalletList(hidden: boolean, active: boolean, disposed: boolean, pending: boolean) {
  return pending && !hidden && active && !disposed;
}
