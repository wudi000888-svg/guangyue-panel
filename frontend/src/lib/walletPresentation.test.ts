import { describe, expect, it } from 'vitest';
import { selectedWalletAsset, walletAmountParts, walletAssetRows, displayWalletAddress, walletAddressAsset } from './walletPresentation';
import type { CryptoChain, CryptoConfiguredAsset } from './cryptoPayments';
import type { PortfolioSnapshot } from './cryptoPortfolio';

const chains = [{ id: 'bsc', chain_id: 56, name: 'BNB Smart Chain', native_symbol: 'BNB' }, { id: 'ethereum', chain_id: 1, name: 'Ethereum', native_symbol: 'ETH' }] as CryptoChain[];
const assets = [{ id: 'bsc-usdt', chain_id: 56, name: 'Binance-Peg USDT', symbol: 'USDT', decimals: 18 }, { id: 'ethereum-usdt', chain_id: 1, name: 'USDT', symbol: 'USDT', decimals: 6 }] as CryptoConfiguredAsset[];
const summaries = {
  bsc: { native_balance_atoms: '100000000000000001', tokens: [{ asset_id: 'bsc-usdt', symbol: 'USDT', decimals: 18, balance_atoms: '12345678901234567890' }] },
  ethereum: { native_balance_atoms: '900719925474099300000000000000', tokens: [{ asset_id: 'ethereum-usdt', symbol: 'USDT', decimals: 6, balance_atoms: '999999999999999999' }] },
} as unknown as Record<string, PortfolioSnapshot>;

describe('wallet asset presentation', () => {
  it('keeps identically named assets on different networks as distinct precise rows', () => {
    const rows = walletAssetRows(chains, summaries, assets);
    expect(rows.find(row => row.key === 'bsc:bsc-usdt')?.amount).toBe('12.34567890123456789');
    expect(rows.find(row => row.key === 'ethereum:ethereum-usdt')?.amount).toBe('999999999999.999999');
    expect(rows.find(row => row.key === 'bsc:native')?.amount).toBe('0.100000000000000001');
    expect(rows).toHaveLength(4);
  });
  it('uses the selected network for the main amount and never substitutes another network balance', () => {
    const rows = walletAssetRows(chains, summaries, assets);
    expect(selectedWalletAsset(rows, 'bsc', 'bsc-usdt')?.key).toBe('bsc:bsc-usdt');
    expect(selectedWalletAsset(rows, 'bsc', 'ethereum-usdt')?.key).toBe('bsc:bsc-usdt');
    expect(selectedWalletAsset(rows, 'missing', 'bsc-usdt')).toBeUndefined();
  });
  it('shows unknown balances as an unknown value rather than zero', () => {
    const rows = walletAssetRows(chains, {}, assets);
    expect(rows.every(row => row.amount === '—' && row.atoms === undefined)).toBe(true);
    expect(walletAmountParts('—')).toEqual({ whole: '—', fraction: '' });
    expect(walletAmountParts('0.000000000000000001')).toEqual({ whole: '0', fraction: '.000000000000000001' });
  });
  it('shortens a valid address for a row while leaving malformed data visible for diagnosis', () => {
    expect(displayWalletAddress('0x1234567890123456789012345678901234567890')).toBe('0x123456…7890');
    expect(displayWalletAddress('unavailable')).toBe('unavailable');
  });
  it('shows a funded stablecoin instead of an empty preferred token', () => {
    const row = { native_atoms: '0', tokens: [{ asset_id: 'usdt', symbol: 'USDT', decimals: 6, balance_atoms: '0' }, { asset_id: 'usdc', symbol: 'USDC', decimals: 6, balance_atoms: '123456789' }] };
    expect(walletAddressAsset(row, 'BNB')).toEqual({ symbol: 'USDC', amount: '123.456789' });
    expect(walletAddressAsset(row, 'BNB', 'usdt')).toEqual({ symbol: 'USDT', amount: '0' });
  });
  it('shows a gas-only funded address with the precise native balance', () => {
    const row = { native_atoms: '100000000000000001', tokens: [{ asset_id: 'usdt', symbol: 'USDT', decimals: 18, balance_atoms: '0' }] };
    expect(walletAddressAsset(row, 'BNB')).toEqual({ symbol: 'BNB', amount: '0.100000000000000001' });
  });
  it('keeps unavailable balances unknown and only shows zero when it is known', () => {
    const row = { native_atoms: '', tokens: [{ asset_id: 'usdt', symbol: 'USDT', decimals: 6, balance_atoms: '' }] };
    expect(walletAddressAsset(row, 'ETH')).toEqual({ symbol: 'USDT', amount: '—' });
    expect(walletAddressAsset(row, 'ETH', 'native')).toEqual({ symbol: 'ETH', amount: '—' });
    expect(walletAddressAsset({ native_atoms: '0', tokens: [{ ...row.tokens[0]!, balance_atoms: '0' }] }, 'ETH')).toEqual({ symbol: 'USDT', amount: '0' });
  });
});
