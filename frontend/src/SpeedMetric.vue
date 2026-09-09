<script setup lang="ts">
import { t } from "./i18n";
import { Gauge, LoaderCircle } from "lucide-vue-next";
defineProps<{
  result?: {
    at: number;
    latency_ms: number;
    mbps: number;
    bytes: number;
    partial: boolean;
    error?: string;
  };
  running?: boolean;
}>();
</script>
<template>
  <div
    class="speed-metric"
    :title="
      result?.at
        ? new Date(result.at * 1000).toLocaleString('zh-CN') +
          t(' · VPS 经所选出口 → Cloudflare')
        : t('由 VPS 通过所选出口测量 HTTPS 首字节延迟与下载速度')
    "
  >
    <template v-if="running"
      ><strong class="speed-pending"
        ><LoaderCircle :size="14" class="spin" />{{ t("测速中…") }}</strong
      ><small>{{ t("单次最多 8 MiB · 18 秒") }}</small></template
    >
    <template v-else-if="result?.error"
      ><strong class="table-warning">{{ t("测速失败") }}</strong
      ><small>{{ result.error }}</small></template
    >
    <template v-else-if="result?.mbps"
      ><strong>{{ result.mbps.toFixed(1) }} <span>Mbps</span></strong
      ><small
        ><i class="latency-dot" />{{ result.latency_ms }} ms
        <span v-if="result.partial">{{ t("· 限时采样") }}</span></small
      ></template
    >
    <template v-else
      ><strong class="speed-empty"><Gauge :size="14" />{{ t("尚未测速") }}</strong
      ><small>{{ t("按需测量出口性能") }}</small></template
    >
  </div>
</template>
