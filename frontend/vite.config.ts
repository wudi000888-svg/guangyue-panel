import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { walletCoreAssets } from './wallet-core-assets'
export default defineConfig({ plugins: [vue(), walletCoreAssets()], server: { port: 9191, strictPort: true, proxy: { '/api': 'http://127.0.0.1:19200', '/sub': 'http://127.0.0.1:19200', '/public-sub': 'http://127.0.0.1:19200' } } })
