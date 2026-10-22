import { createApp } from 'vue'
import App from './App.vue'
import { createPinia } from 'pinia'
import { router } from './router'
import { loadLocaleMessages } from './i18n'
import './styles/index.css'
import './styles/sidebar-customization.css'
import './styles/pearl-river-theme.css'
// Saved English users wait for the table so the first paint is not Chinese.
loadLocaleMessages().catch(() => {}).finally(() => createApp(App).use(createPinia()).use(router).mount('#app'))
