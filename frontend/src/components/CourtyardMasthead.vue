<script setup lang="ts">
defineProps<{title:string;description:string;eyebrow:string}>();
</script>
<template>
<header class="courtyard-masthead">
 <div class="courtyard-masthead-copy"><span class="courtyard-kicker">{{eyebrow}}</span><h1>{{title}}</h1><p>{{description}}</p><div v-if="$slots.default" class="courtyard-masthead-actions"><slot/></div></div>
 <div class="courtyard-masthead-art" aria-hidden="true"><img src="/design/courtyard-day.svg" alt="" width="1000" height="960"/></div>
</header>
</template>
<style scoped>
.courtyard-masthead{position:relative;display:flex;align-items:center;justify-content:space-between;gap:24px;overflow:hidden;padding:30px 32px;background:var(--surface);border:1px solid var(--border);border-radius:12px;min-height:188px}.courtyard-masthead-copy{position:relative;z-index:1;max-width:calc(100% - 250px);min-width:0}.courtyard-kicker{display:block;color:var(--accent-text);font-size:12px;font-weight:650;letter-spacing:.06em;margin-bottom:10px}.courtyard-masthead h1{font-size:28px;line-height:1.35;margin:0 0 10px;font-weight:650;letter-spacing:-.025em}.courtyard-masthead p{font-size:14px;line-height:1.7;color:var(--secondary);margin:0}.courtyard-masthead-actions{display:flex;align-items:center;gap:16px;flex-wrap:wrap;margin-top:20px}.courtyard-masthead-art{position:absolute;right:0;top:0;width:250px;height:100%;overflow:hidden;opacity:.85;mask-image:linear-gradient(to right,transparent,#000 25%)}.courtyard-masthead-art img{width:100%;height:100%;object-fit:cover;object-position:center 48%;display:block}[data-theme=dark] .courtyard-masthead-art{opacity:.35}@media(max-width:700px){.courtyard-masthead{padding:24px;min-height:180px}.courtyard-masthead-copy{max-width:100%}.courtyard-masthead h1{font-size:24px}.courtyard-masthead-art{width:170px;opacity:.14}.courtyard-masthead-actions{gap:12px;margin-top:16px}}
</style>
