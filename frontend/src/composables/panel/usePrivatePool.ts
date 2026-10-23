import { computed, nextTick, reactive, ref, watch, type Ref } from "vue";
import type { Router } from "vue-router";
import { ApiError, isCancelled, errorMessage, type useApi } from "../../lib/api";
import { t } from "../../i18n";
import type { IPResource, State } from "../../types";

/** Private IP pool: list filters, subscription sources and the import dialog. */
export function usePrivatePool({ state, owner, selectedSite, api, router, error, modal, task, refresh, go, toast }: {
  state: Ref<State | null>; owner: Ref<boolean>; selectedSite: Ref<string>; api: ReturnType<typeof useApi>; router: Router;
  error: Ref<string>; modal: Ref<string>; task: (fn: () => Promise<void>) => Promise<void>; refresh: (silent?: boolean) => Promise<boolean>;
  go: (id: string) => void; toast: (text: string) => void;
}) {
const importMode = ref<'url' | 'content' | 'file'>("url");
const importForm = reactive({ url: "", content: "", plain_protocol: "http" });
const importFile = ref<{ name: string; size: number; lines: number } | null>(null);
const importFileReading = ref(false);
const importIssues = ref<{ index: number; reason: string }[]>([]);
let importFileContent = "", importFileSequence = 0;
function clearPrivateFile() {
  importFileSequence++;
  importFileContent = "";
  importFile.value = null;
  importFileReading.value = false;
}
async function readPrivateFile(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;
  clearPrivateFile();
  const sequence = importFileSequence;
  importFileReading.value = true;
  importIssues.value = [];
  error.value = "";
  try {
    if (file.size > 4 * 1024 * 1024) throw new Error(t("文件不能超过 4 MiB"));
    const content = await file.text();
    if (sequence !== importFileSequence || modal.value !== "import") return;
    if (!content.trim()) throw new Error(t("文件为空，请选择包含代理配置的文件。"));
    importFileContent = content;
    importFile.value = { name: file.name, size: file.size, lines: content.split(/\r?\n/).filter(line => line.trim() && !line.trim().startsWith('#')).length };
    importMode.value = "file";
  } catch (e) {
    if (sequence === importFileSequence) error.value = e instanceof Error && [t("文件不能超过 4 MiB"), t("文件为空，请选择包含代理配置的文件。")].includes(e.message) ? e.message : t("读取文件失败，请重新选择文件。");
  } finally {
    input.value = "";
    if (sequence === importFileSequence) importFileReading.value = false;
  }
}
const importReport = ref<{
  queued?: boolean;
  added: number;
  duplicates: number;
  warnings: { index: number; reason: string }[];
} | null>(null);
function openImport() {
  Object.assign(importForm, { url: "", content: "", plain_protocol: "http" });
  clearPrivateFile();
  importMode.value = "url";
  importIssues.value = [];
  error.value = "";
  modal.value = "import";
}
watch(modal, value => {
  if (value !== "import") {
    clearPrivateFile();
    importForm.url = "";
    importForm.content = "";
    importIssues.value = [];
  }
}, { flush: 'sync' });
watch(importMode, () => { importIssues.value = []; error.value = ""; });
async function importSubscription() {
  if (importFileReading.value || (importMode.value === 'file' && !importFileContent)) return;
  await task(async () => {
    importIssues.value = [];
    let result;
    try {
      result = await api("/ips/import", "POST", importMode.value === "url"
        ? { url: importForm.url }
        : { content: importMode.value === "file" ? importFileContent : importForm.content, plain_protocol: importForm.plain_protocol });
    } catch (error) {
      if (error instanceof ApiError) importIssues.value = error.data.warnings || [];
      throw error;
    }
    importReport.value = { ...result, warnings: Array.isArray(result.warnings) ? result.warnings : [] };
    importForm.url = "";
    importForm.content = "";
    modal.value = "";
    await refresh();
    if(importReport.value?.queued){go("sources");toast(t("更新已排队，后台将依次执行"));}else {go("ips/manual");toast(t("已导入独立出口，可检测质量、测速或分配至节点。"));}
  });
}
const ipSearch = ref(""),
  ipType = ref("all"),
  ipState = ref("all");
const ipPool = computed(() => (state.value?.ip_pool || []).filter(p => p.pool_group !== 'public'));
const publicResources = computed(() => (state.value?.ip_pool || []).filter(p => p.pool_group === 'public'));
const privateTab = ref("subscriptions");
type PrivateSourceSummary = { source: { id: string; name: string }; resource_ids: string[] };
const privateSources = ref<PrivateSourceSummary[]>([]);
const privateSourcesReady = ref(false);
const privateSourcesLoading = ref(false), privateSourcesError = ref('');
let privateSourcesRequest: Promise<void> | null = null;
function receivePrivateSources(rows: PrivateSourceSummary[]) {
  privateSources.value = rows;
  privateSourcesReady.value = true;
  privateSourcesError.value = '';
}
async function loadPrivateSources() {
  if (!owner.value) return;
  if (privateSourcesRequest) return privateSourcesRequest;
  privateSourcesLoading.value = true;
  privateSourcesRequest = (async () => {
    try { receivePrivateSources((await api('/import-sources')).items); }
    catch (e) { if (!isCancelled(e)) privateSourcesError.value = errorMessage(e); }
    finally { privateSourcesLoading.value = false; privateSourcesRequest = null; }
  })();
  return privateSourcesRequest;
}
const publicTab = ref('resources');
function selectPublicTab(value: string) {
  if (!['resources', 'sources', 'policy'].includes(value)) return;
  publicTab.value = value;
  void router.replace('/public' + (value === 'resources' ? '' : '/' + value));
}
const subscriptionResourceIDs = computed(() => new Set(privateSources.value.flatMap(row => row.resource_ids || [])));
const isSubscribedResource = (p: IPResource) => !!p.subscription_id || subscriptionResourceIDs.value.has(p.id);
const subscriptionIPs = computed(() => ipPool.value.filter(isSubscribedResource));
const manualIPs = computed(() => ipPool.value.filter(p => !isSubscribedResource(p)));
const canMountSubsites = computed(() => owner.value && state.value?.system.edition === 'pro' && state.value?.system.role === 'controller' && !selectedSite.value);
const privateTabs = computed(() => [
  ...(canMountSubsites.value ? [{id:'subsites',label:t('子站 IP 池')}] : []),
  { id: 'subscriptions', label: t('订阅出口'), ...(privateSourcesReady.value ? { count: subscriptionIPs.value.length } : {}) },
  { id: 'manual', label: t('独立出口'), ...(privateSourcesReady.value ? { count: manualIPs.value.length } : {}) },
  { id: 'sources', label: t('订阅来源'), ...(privateSourcesReady.value ? { count: privateSources.value.length } : {}) },
]);
function sourceLabel(p: IPResource) {
  const names = privateSources.value.filter(row => row.source.id === p.subscription_id || row.resource_ids?.includes(p.id)).map(row => row.source.name);
  return names.join(' · ') || (p.subscription_id ? t('订阅来源') : t('独立添加'));
}
function selectPrivateTab(value: string) {
  if (value === 'subsite-egress') value = 'subsites';
  if (!['subscriptions','manual','sources','subsites'].includes(value)) return;
  if (value === 'subsites' && !canMountSubsites.value) value = 'subscriptions';
  privateTab.value = value;
  sourceFilter.value = null;
  ipSearch.value = '';
  ipType.value = 'all';
  ipState.value = 'all';
  void router.replace('/ips/' + value);
}
function viewSourceResources(ids: string[], name: string) {
  selectPrivateTab('subscriptions');
  sourceFilter.value = { ids, name };
  nextTick(() => window.scrollTo({ top: 0, behavior: 'instant' }));
}
const poolStats = computed(() => ({
  total: ipPool.value.length,
  ready: ipPool.value.filter((p) => p.enabled && p.reachable).length,
  used: ipPool.value.filter((p) => p.node_ids?.length).length,
  attention: ipPool.value.filter((p) => p.enabled && !p.reachable).length,
}));
const ipStatus = (p: IPResource) =>
  !p.enabled
    ? t("已停用")
    : !p.checked_at
      ? t("待检测")
      : !p.reachable
        ? t("不可用")
        : p.probe_error
          ? t("国家待识别")
          : t("可用");
const ipBadge = (p: IPResource) =>
  !p.enabled || !p.checked_at ? "neutral" : p.reachable ? "success" : "danger";
const sourceFilter=ref<{ids:string[];name:string}|null>(null);
const filteredIPs = computed(() =>
  (privateSourcesReady.value ? privateTab.value === 'manual' ? manualIPs.value : subscriptionIPs.value : []).filter(
    (p) =>
      (!sourceFilter.value||sourceFilter.value.ids.includes(p.id)) &&
      [p.label, p.name, p.probe_ip, p.host, p.notes, ...(p.quality?.tags || []).map(t => t.label), ...(p.quality?.streams || []).map(t => t.label)]
        .join(" ")
        .toLowerCase()
        .includes(ipSearch.value.toLowerCase()) &&
      (ipType.value === "all" || p.exit === ipType.value) &&
      (ipState.value === "all" ||
        (ipState.value === "ready"
          ? p.enabled && p.reachable
          : ipState.value === "unassigned"
            ? !p.node_ids?.length
            : ipState.value === "disabled"
              ? !p.enabled
              : p.enabled && !p.reachable)),
  ),
);
function resetPrivatePool() {
  privateSources.value = [];
  privateSourcesReady.value = false;
  privateSourcesError.value = "";
  sourceFilter.value = null;
  importReport.value = null;
}
return { importMode, importForm, importFile, importFileReading, importIssues, clearPrivateFile, readPrivateFile, importReport, openImport, importSubscription, ipSearch, ipType, ipState, ipPool, publicResources, privateTab, privateSources, privateSourcesReady, privateSourcesLoading, privateSourcesError, receivePrivateSources, loadPrivateSources, publicTab, selectPublicTab, subscriptionResourceIDs, isSubscribedResource, subscriptionIPs, manualIPs, canMountSubsites, privateTabs, sourceLabel, selectPrivateTab, viewSourceResources, poolStats, ipStatus, ipBadge, sourceFilter, filteredIPs, resetPrivatePool };
}
