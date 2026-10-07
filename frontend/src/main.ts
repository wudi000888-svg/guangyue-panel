import { createApp } from 'vue'
import App from './App.vue'
import { createPinia } from 'pinia'
import { router } from './router'
import './styles/index.css'
createApp(App).use(createPinia()).use(router).mount('#app')
