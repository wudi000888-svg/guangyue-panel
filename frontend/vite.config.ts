import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { walletCoreAssets } from './wallet-core-assets'
const vendorPackages = ['/node_modules/vue/', '/node_modules/@vue/', '/node_modules/vue-router/', '/node_modules/pinia/', '/node_modules/lucide-vue-next/'];

export default defineConfig({
  plugins: [vue(), walletCoreAssets()],
  server: { port: 9191, strictPort: true, proxy: { '/api': 'http://127.0.0.1:19200', '/sub': 'http://127.0.0.1:19200', '/public-sub': 'http://127.0.0.1:19200' } },
  build: {
    // Keep the framework and icon runtime in a stable, cacheable chunk. The
    // application entry stays small and can be revalidated independently on
    // every panel release.
    rollupOptions: { output: { manualChunks(id) { return vendorPackages.some(path => id.includes(path)) ? 'vendor' : undefined; } } },
  },
})
