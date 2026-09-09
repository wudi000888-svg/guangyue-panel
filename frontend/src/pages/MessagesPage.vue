<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, site, owner, refresh } = usePanelContext();
import Inbox from "../Inbox.vue";
</script>
<template>
<Inbox v-if="state" :owner="owner" :users="state.users" :me="state.me.id" :support-email="site.support_email" :unread="state.unread_messages" @refresh="refresh(true)"/>
</template>
