import fs from 'node:fs';
const en=JSON.parse(fs.readFileSync(new URL('./src/locales/en.json',import.meta.url),'utf8'));
const missing=new Set();
for(const file of fs.readdirSync(new URL('./src/',import.meta.url)).filter(x=>x.endsWith('.vue'))){
 const text=fs.readFileSync(new URL('./src/'+file,import.meta.url),'utf8');
 for(const m of text.matchAll(/\bt\(\s*(['"])([^'"\n]+)\1\s*\)/g))if(/[\u3400-\u9fff]/u.test(m[2])&&!en[m[2]])missing.add(file+': '+m[2]);
}
if(missing.size)throw new Error('Missing English copy:\n'+[...missing].join('\n'));
console.log('English catalogue:',Object.keys(en).length,'entries; all literal UI keys covered');
