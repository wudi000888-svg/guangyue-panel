<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, simpleMode, refresh } = usePanelContext();
import PanelSettings from "../PanelSettings.vue";
</script>
<template>
<PanelSettings v-if="state" :simple-mode="simpleMode" @saved="refresh()"/>
</template>
