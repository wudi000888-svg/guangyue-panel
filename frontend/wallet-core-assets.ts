import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import type { Plugin } from 'vite';

// The upstream loader requires its unchanged sibling filename and accepts no locateFile hook.
export function walletCoreAssets(): Plugin {
  const prefix = 'assets/wallet-core/4.8.4/';
  const files = ['wallet-core.js', 'wallet-core.wasm'] as const;
  const source = (name: string) => fileURLToPath(new URL(`./node_modules/@trustwallet/wallet-core/dist/lib/${name}`, import.meta.url));
  return {
    name: 'local-trust-wallet-core-assets',
    generateBundle() {
      for (const name of files) this.emitFile({ type: 'asset', fileName: prefix + name, source: readFileSync(source(name)) });
    },
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const pathname = request.url?.split('?')[0];
        const name = files.find(file => pathname === `${server.config.base}${prefix}${file}`);
        if (!name) { next(); return; }
        response.setHeader('Content-Type', name.endsWith('.wasm') ? 'application/wasm' : 'application/javascript');
        response.setHeader('Cache-Control', 'no-cache');
        response.end(readFileSync(source(name)));
      });
    },
  };
}
