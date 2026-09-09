import { createApp } from 'vue'
import App from './App.vue'
import { createPinia } from 'pinia'
import { router } from './router'
import './style.css'
import './enterprise.css'
import './management.css'
createApp(App).use(createPinia()).use(router).mount('#app')
