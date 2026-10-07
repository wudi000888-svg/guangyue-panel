<script setup lang="ts">
import "../styles/admin-ui.css";
import { usePanelContext } from "../composables/panelContext";
const { state, publicResources, publicTab, selectPublicTab, refresh, go } = usePanelContext();
import PublicPool from "../PublicPool.vue";
</script>
<template>
<PublicPool class="admin-page" v-if="state" :resources="publicResources" :active-tab="publicTab" @update:active-tab="selectPublicTab" @refresh="refresh()" @nodes="go('public-nodes')" />
</template>
