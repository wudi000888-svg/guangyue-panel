<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, publicResources, publicTab, selectPublicTab, refresh, go } = usePanelContext();
import PublicPool from "../PublicPool.vue";
</script>
<template>
<PublicPool v-if="state" :resources="publicResources" :active-tab="publicTab" @update:active-tab="selectPublicTab" @refresh="refresh()" @nodes="go('public-nodes')" />
</template>
