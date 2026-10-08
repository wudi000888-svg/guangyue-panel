import { beforeAll, expect, it } from 'vitest';
import { initWasm, type WalletCore } from '@trustwallet/wallet-core';
import { describeCoreWallet } from './walletCore';

let core: WalletCore;
// Public BIP-39 fixture only: never use these widely known words to hold funds.
const mnemonic = 'abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about';
const xpub = 'xpub6DCoCpSuQZB2jawqnGMEPS63ePKWkwWPH4TU45Q7LPXWuNd8TMtVxRrgjtEshuqpK3mdhaWHPFsBngh5GFZaM6si3yZdUsT8ddYM3PwnATt';
beforeAll(async () => { core = await initWasm(); }, 30000);

it('official Wallet Core matches independent Ethereum account xpub and address vectors', () => {
  const wallet = core.HDWallet.createWithMnemonic(mnemonic, '');
  try {
    expect(describeCoreWallet(core, wallet)).toEqual({ mnemonic, xpub, path: "m/44'/60'/0'", first_address: '0x9858EfFD232B4033E47d90003D41EC34EcaEda94', engine_version: '4.8.4' });
    const key = wallet.getDerivedKey(core.CoinType.ethereum, 0, 0, 1);
    try { expect(core.CoinTypeExt.deriveAddress(core.CoinType.ethereum, key)).toBe('0x6Fac4D18c912343BF86fa7049364Dd4E424Ab9C0'); }
    finally { key.delete(); }
  } finally { wallet.delete(); }
});

it('the same public account xpub derives receiving addresses without mnemonic access', () => {
  for (const [index, address] of [[0, '0x9858EfFD232B4033E47d90003D41EC34EcaEda94'], [1, '0x6Fac4D18c912343BF86fa7049364Dd4E424Ab9C0']] as const) {
    const publicKey = core.HDWallet.getPublicKeyFromExtended(xpub, core.CoinType.ethereum, `m/44'/60'/0'/0/${index}`);
    try { expect(core.CoinTypeExt.deriveAddressFromPublicKey(core.CoinType.ethereum, publicKey)).toBe(address); }
    finally { publicKey.delete(); }
  }
});
