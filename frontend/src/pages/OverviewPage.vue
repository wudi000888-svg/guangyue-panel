<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, publicFeaturesEnabled, refresh, go } = usePanelContext();
import Dashboard from "../Dashboard.vue";
</script>
<template>
<Dashboard v-if="state" :state="state" :public-features-enabled="publicFeaturesEnabled" @navigate="go" @refresh="refresh(true)"/>
</template>
