<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, simpleMode, refresh, go } = usePanelContext();
import Dashboard from "../Dashboard.vue";
</script>
<template>
<Dashboard v-if="state" :state="state" :simple="simpleMode" @navigate="go" @refresh="refresh(true)"/>
</template>
