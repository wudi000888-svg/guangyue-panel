import { defineStore } from 'pinia';
import { ref } from 'vue';
export const useAccessStore = defineStore('access',()=>{const role=ref<string|null>(null),edition=ref('lite');return {role,edition};});
