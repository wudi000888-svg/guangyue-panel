/** Internal Solana network identity is not an EIP-155 chain ID. */
export const SOLANA_CHAIN_ID = 101;
export const PLASMA_CHAIN_ID = 9745;
export const supportedCryptoChain = (id:number) => [1,56,SOLANA_CHAIN_ID,PLASMA_CHAIN_ID].includes(id);
export function nativeDecimals(chain?:{chain_id?:number;native_decimals?:number}):number {
 return chain?.native_decimals ?? (chain?.chain_id===SOLANA_CHAIN_ID?9:18);
}
const alphabet='123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
/** Decode and count bytes, rather than accepting any similarly shaped base58 text. */
export function solanaAddress(address:string):boolean {
 if(!/^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(address))return false;
 let value=0n;for(const digit of address)value=value*58n+BigInt(alphabet.indexOf(digit));
 let bytes=0;for(let current=value;current>0n;current>>=8n)bytes++;
 const leading=address.match(/^1*/)?.[0].length||0;
 return bytes+leading===32;
}
export function cryptoAddress(chainID:number,address:string,recipient=false):boolean {
 if(chainID===SOLANA_CHAIN_ID)return solanaAddress(address)&&(!recipient||address!=='1'.repeat(32));
 return /^0x[0-9a-fA-F]{40}$/.test(address)&&(!recipient||!/^0x0{40}$/i.test(address));
}
export const sameCryptoAddress=(chainID:number,a:string,b:string)=>chainID===SOLANA_CHAIN_ID?a===b:a.toLowerCase()===b.toLowerCase();
