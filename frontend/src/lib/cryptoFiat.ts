/** Prices are decimal strings from the server. They never change the sealed native fee budget. */
export interface NativeFiatQuote {
 native_price_usd?:string;native_price_cny?:string;price_checked_at?:number;price_expires_at?:number;fiat_source?:string;fiat_error?:string;
}
export function nativeFiatKnown(value:NativeFiatQuote|undefined):boolean{
 return [value?.native_price_usd,value?.native_price_cny].some(price=>!!price&&/^\d{1,30}(?:\.\d{1,24})?$/.test(price)&&BigInt(price.replace('.',''))>0n);
}
export function fiatPriceCurrent(value:NativeFiatQuote|undefined,now=Date.now()/1000):boolean {
 return nativeFiatKnown(value)&&!value?.fiat_error&&!!value?.price_checked_at&&value.price_checked_at<=now+5&&!!value.price_expires_at&&value.price_expires_at>now;
}
export function approximateFiat(atoms:string|undefined,decimals:number,price:string|undefined):string {
 if(!atoms||!/^\d{1,100}$/.test(atoms)||!Number.isInteger(decimals)||decimals<0||decimals>36||!price||!/^\d{1,30}(?:\.\d{1,24})?$/.test(price))return '—';
 const [whole,fraction='']=price.split('.'),coefficient=BigInt(whole+fraction),quantity=BigInt(atoms);
 if(coefficient<=0n)return '—';
 const denominator=10n**BigInt(decimals+fraction.length),numerator=quantity*coefficient;
 if(numerator>0n&&numerator*10000n<denominator)return '<0.0001';
 const precision=numerator>0n&&numerator*100n<denominator?4:2,scale=10n**BigInt(precision);
 const rounded=(numerator*scale+denominator/2n)/denominator;
 return (rounded/scale).toString()+'.'+(rounded%scale).toString().padStart(precision,'0');
}
export function feeFiat(atoms:string|undefined,decimals:number,quote:NativeFiatQuote|undefined):string {
 const usd=approximateFiat(atoms,decimals,quote?.native_price_usd),cny=approximateFiat(atoms,decimals,quote?.native_price_cny);
 return '≈ $'+usd+' / ¥'+cny;
}
