import type { CryptoChain, CryptoConfiguredAsset } from './cryptoPayments';
import { portfolioAmount, type PortfolioBalance, type PortfolioSnapshot } from './cryptoPortfolio';
import {nativeDecimals,solanaAddress} from './cryptoNetworks';

export interface WalletAssetRow {
  key: string;
  chain: CryptoChain;
  assetID: string;
  symbol: string;
  name: string;
  decimals: number;
  atoms?: string;
  amount: string;
  native: boolean;
}

/** Asset identity includes its network. Native coin balances never become a cross-chain total. */
export function walletAssetRows(chains: CryptoChain[], summaries: Record<string, PortfolioSnapshot>, assets: CryptoConfiguredAsset[]): WalletAssetRow[] {
  return chains.flatMap(chain => {
    const snapshot = summaries[chain.id];
    const tokens = assets.filter(asset => asset.chain_id === chain.chain_id).map(asset => {
      const value = snapshot?.tokens.find(token => token.asset_id === asset.id);
      const atoms = value && /^\d+$/.test(value.balance_atoms) ? value.balance_atoms : undefined;
      return { key: chain.id + ':' + asset.id, chain, assetID: asset.id, symbol: asset.symbol, name: asset.name || asset.symbol, decimals: asset.decimals, atoms, amount: portfolioAmount(atoms, asset.decimals), native: false };
    });
    const atoms = snapshot && /^\d+$/.test(snapshot.native_balance_atoms) ? snapshot.native_balance_atoms : undefined;
    return [...tokens, { key: chain.id + ':native', chain, assetID: 'native', symbol: chain.native_symbol, name: chain.name, decimals: nativeDecimals(chain), atoms, amount: portfolioAmount(atoms,nativeDecimals(chain)), native: true }];
  });
}

export function selectedWalletAsset(rows: WalletAssetRow[], chainID: string, assetID: string): WalletAssetRow | undefined {
  const networkRows = rows.filter(row => row.chain.id === chainID);
  return networkRows.find(row => row.assetID === assetID) || networkRows.find(row => ['USDT','USDT0'].includes(row.symbol)) || networkRows.find(row => !row.native) || networkRows[0];
}

export function walletAmountParts(amount: string) {
  const dot = amount.indexOf('.');
  return dot < 0 ? { whole: amount, fraction: '' } : { whole: amount.slice(0, dot), fraction: amount.slice(dot) };
}

export function displayWalletAddress(address: string): string {
  return /^0x[0-9a-fA-F]{40}$/.test(address)||solanaAddress(address) ? address.slice(0, 8) + '…' + address.slice(-4) : address;
}

/** The default address amount must not conceal a funded token behind an empty USDT balance. */
export function walletAddressAsset(row: Pick<PortfolioBalance, 'tokens' | 'native_atoms'>, nativeSymbol: string, assetID = '', configuredAssets: CryptoConfiguredAsset[] = [],nativeCurrencyDecimals=nativeSymbol==='SOL'?9:18): { symbol: string; amount: string } {
  const native = { symbol: nativeSymbol, decimals: nativeCurrencyDecimals, balance_atoms: row.native_atoms };
  const known = (atoms: string) => /^\d+$/.test(atoms);
  const positive = (atoms: string) => known(atoms) && BigInt(atoms) > 0n;
  const format = (value: { symbol: string; decimals: number; balance_atoms: string }) => ({ symbol: value.symbol, amount: portfolioAmount(known(value.balance_atoms) ? value.balance_atoms : undefined, value.decimals) });
  if (assetID === 'native') return format(native);
  if (assetID) {
    const token = row.tokens.find(value => value.asset_id === assetID);
    return token ? format(token) : { symbol: configuredAssets.find(value => value.id === assetID)?.symbol || '—', amount: '—' };
  }
  const funded = row.tokens.find(value => ['USDT','USDT0'].includes(value.symbol) && positive(value.balance_atoms)) || row.tokens.find(value => positive(value.balance_atoms));
  if (funded) return format(funded);
  if (positive(native.balance_atoms)) return format(native);
  const unknown = row.tokens.find(value => ['USDT','USDT0'].includes(value.symbol) && !known(value.balance_atoms)) || row.tokens.find(value => !known(value.balance_atoms));
  if (unknown) return format(unknown);
  if (!known(native.balance_atoms)) return format(native);
  return format(row.tokens.find(value => ['USDT','USDT0'].includes(value.symbol)) || row.tokens[0] || native);
}
