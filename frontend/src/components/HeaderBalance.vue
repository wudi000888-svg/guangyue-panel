<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { Wallet as WalletIcon, ChevronRight } from 'lucide-vue-next';
import { usePanelContext } from '../composables/panelContext';
import { useApi, isCancelled } from '../lib/api';
import { money, walletRevision, type Wallet } from '../lib/commerce';
import { latestRequest, serialPoll } from '../lib/requests';
import { t } from '../i18n';

const props = defineProps<{ userId: number }>();
const { go } = usePanelContext();
const route = useRoute(), api = useApi('/commerce'), request = latestRequest();
const wallet = ref<Wallet|null>(null), loading = ref(true), failed = ref(false);
const amount = computed(() => wallet.value ? money(wallet.value.available) : '—');
const label = computed(() => failed.value ? t('余额暂不可用，点击查看钱包') :
  loading.value && !wallet.value ? t('正在加载余额') :
  `${t('可用余额')} ${amount.value} · ${t('冻结余额')} ${money(wallet.value?.held)} · ${t('查看钱包')}`);

async function refresh() {
  const current = request.start();
  loading.value = true;
  try {
    // Omit user_id so the server selects the signed-in account, independently
    // of the member an administrator is inspecting on the wallet page.
    const value = await api<Wallet>('/wallet', 'GET', undefined, { signal: current.signal });
    if (!request.isCurrent(current)) return;
    if (value.user_id !== props.userId || value.currency !== 'CNY' ||
      !/^\d+$/.test(value.available) || !/^\d+$/.test(value.held)) throw new Error('Invalid wallet');
    wallet.value = value;
    failed.value = false;
  } catch (error) {
    if (request.isCurrent(current) && !isCancelled(error)) {
      wallet.value = null;
      failed.value = true;
    }
  } finally {
    if (request.isCurrent(current)) loading.value = false;
  }
}
const refreshVisible = async () => { if (!document.hidden) await refresh(); };
const poll = serialPoll(refreshVisible, () => 30000);
watch([() => route.fullPath, walletRevision], refresh);
onMounted(() => {
  void refresh();
  poll.start();
  window.addEventListener('focus', refreshVisible);
  document.addEventListener('visibilitychange', refreshVisible);
});
onBeforeUnmount(() => {
  poll.stop();
  request.cancel();
  window.removeEventListener('focus', refreshVisible);
  document.removeEventListener('visibilitychange', refreshVisible);
});
</script>

<template>
  <button class="header-balance" :class="{ unavailable: failed }" :title="label"
    :aria-label="label" :aria-busy="loading && !wallet" @click="go('wallet')">
    <WalletIcon :size="18" aria-hidden="true"/>
    <span class="header-balance-copy">
      <span class="header-balance-label">{{ t('可用余额') }}</span>
      <strong class="header-balance-amount" aria-live="polite" aria-atomic="true">{{ amount }}</strong>
    </span>
    <ChevronRight :size="14" aria-hidden="true"/>
  </button>
</template>
