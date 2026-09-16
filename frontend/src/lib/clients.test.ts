import { describe, expect, it } from 'vitest';
import clients from '../data/clients.json';
describe('official client platform mapping',()=>{
  it('matches package types and architectures to operating systems',()=>{
    for(const client of clients)for(const d of client.downloads){
      const u=new URL(d.url);
      expect(u.protocol).toBe('https:');
      expect(['github.com','apps.apple.com','f-droid.org']).toContain(u.hostname);
      if(u.hostname==='github.com'){
        const ext=u.pathname.split('.').at(-1);
        expect(({Windows:['exe','zip'],macOS:['dmg'],Linux:['deb'],Android:['apk']} as Record<string,string[]>)[d.platform]).toContain(ext);
        const file=u.pathname.split('/').at(-1)!;
        if(d.arch.startsWith('ARM64')||d.arch==='Apple Silicon')expect(file).toMatch(/arm64|aarch64/);
        if(d.arch==='ARMv7')expect(file).toContain('armeabi-v7a');
        if(d.arch.startsWith('x64')||d.arch==='Intel')expect(file).toMatch(/x64|amd64|x86_64|-64/);
      }
      if(d.platform==='iOS / iPadOS')expect(u.hostname).toBe('apps.apple.com');
    }
  });
});
