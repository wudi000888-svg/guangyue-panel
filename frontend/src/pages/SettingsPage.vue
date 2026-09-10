<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, simpleMode, selectedSite, refresh } = usePanelContext();
import PanelSettings from "../PanelSettings.vue";
</script>
<template>
<PanelSettings v-if="state" :simple-mode="simpleMode" :remote-site="!!selectedSite" @saved="refresh()"/>
</template>
