<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, simpleMode, selectedSite, publicFeaturesEnabled, refresh } = usePanelContext();
import CommerceSettings from "../components/CommerceSettings.vue";
import PanelSettings from "../PanelSettings.vue";
</script>
<template>
<PanelSettings v-if="state" :simple-mode="simpleMode" :remote-site="!!selectedSite" :public-features-enabled="publicFeaturesEnabled" @update-public-features-enabled="publicFeaturesEnabled=$event" @saved="refresh()"/>
<CommerceSettings v-if="state && !selectedSite"/>
</template>
