<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import {
  Bell,
  Mail,
  Activity,
  ArrowUpRight,
  Check,
  ChevronRight,
  CircleHelp,
  Copy,
  Download,
  Gauge,
  Globe2,
  KeyRound,
  LayoutDashboard,
  LoaderCircle,
  LogOut,
  Menu,
  Moon,
  Sun,
  PanelLeftClose,
  PanelLeftOpen,
  Building2,
  Database,
  Pencil,
  Plus,
  Power,
  QrCode,
  RadioTower,
  RefreshCw,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Trash2,
  Users,
  X,
} from "lucide-vue-next";
import QRCode from "qrcode";
import { useApi, ApiError, downloadBlob, invalidateSession, onSessionExpired, isCancelled } from './lib/api';
import { latestRequest, serialPoll } from './lib/requests';
import { readPreference, writePreference } from './lib/preferences';
const api = useApi();
const stateRequest = latestRequest();
import { t, locale, applyDefaultLocale } from "./i18n";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import ViewModeSwitcher, { type ViewMode } from "./ViewModeSwitcher.vue";
import "./view-mode.css";
import Inbox from "./Inbox.vue";
import ImportSources from "./ImportSources.vue";
import ResourceTabs from "./ResourceTabs.vue";
import PanelSettings, { type SiteSettings } from "./PanelSettings.vue";
import CountryMark from "./CountryMark.vue";
import SpeedMetric from "./SpeedMetric.vue";
import QualityTags from "./QualityTags.vue";
import NodeSubscriptionCards from "./NodeSubscriptionCards.vue";
import PublicPool from "./PublicPool.vue";
import Dashboard from "./Dashboard.vue";
import type { IPQuality } from "./quality";

import type { User, Node, IPResource, State, Sub, SpeedResult } from "./types";
const state = ref<State | null>(null),
  ready = ref(false),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  page = ref("overview"),
  mobileNav = ref(false);
const site = ref<SiteSettings>({panel_name:'广月面板',organization:'跨境电商工作区',default_locale:'zh-CN',support_email:'',login_notice:'',revision:''});
watch([site, locale], () => { document.title=site.value.panel_name+' · '+t('企业控制台'); }, {deep:true,immediate:true});
const login = reactive({ username: "", password: "" });
const userSearch = ref(""),
  userFilter = ref("all"),
  userRole = ref("all"),
  modal = ref(""),
  editingID = ref(0);
const userForm = reactive({
  username: "",
  password: "",
  enabled: true,
  vless: true,
  hy2: true,
  expiresDate: "",
  quotaGB: 0,
});
const nodeForm = reactive<Node>({
  default_direct: false,
  reality_sni: "",
  dns: {mode:"secure", doh:"https://1.1.1.1/dns-query", ipv6:"block"},
  exit_id: "",
  id: "",
  name: "",
  protocol: "vless",
  enabled: true,
  exit: "direct",
  host: "",
  port: 0,
  username: "",
  password: "",
  has_password: false,
  probe_ip: "",
  probed_at: 0,
  country: "",
  country_code: "",
  checked_at: 0,
  probe_error: "",
});
const ipForm = reactive<IPResource>({
  ...nodeForm,
  label: "",
  notes: "",
  revision: "",
  reachable: false,
  node_ids: [],
});
const speedRunning = ref("");
const qualityRunning = ref("");
async function qualityTest(n: Node, pool = false) {
  await task(async () => {
    qualityRunning.value = n.id;
    try { const result: IPQuality = await api((pool ? '/ips/' : '/nodes/') + n.id + '/quality', 'POST', {}); toast(result.error || t('质量检测完成，标签已更新')); }
    finally { qualityRunning.value = ''; await refresh(); }
  });
}
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
async function speedTest(n: Node, pool = false) {
  await task(async () => {
    speedRunning.value = n.id;
    try {
      const result: SpeedResult = await api(
        (pool ? "/ips/" : "/nodes/") + n.id + "/speed",
        "POST",
        {},
      );
      toast(
        result.error || t("出口测速完成 · ") + result.mbps.toFixed(1) + " Mbps",
      );
    } finally {
      speedRunning.value = "";
      await refresh();
    }
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
    catch (e) { if (!isCancelled(e)) privateSourcesError.value = (e as Error).message; }
    finally { privateSourcesLoading.value = false; privateSourcesRequest = null; }
  })();
  return privateSourcesRequest;
}
const publicTab = ref('resources');
function selectPublicTab(value: string) {
  if (!['resources', 'sources', 'policy'].includes(value)) return;
  publicTab.value = value;
  history.replaceState(null, '', '#public' + (value === 'resources' ? '' : '/' + value));
}
const subscriptionResourceIDs = computed(() => new Set(privateSources.value.flatMap(row => row.resource_ids || [])));
const isSubscribedResource = (p: IPResource) => !!p.subscription_id || subscriptionResourceIDs.value.has(p.id);
const subscriptionIPs = computed(() => ipPool.value.filter(isSubscribedResource));
const manualIPs = computed(() => ipPool.value.filter(p => !isSubscribedResource(p)));
const privateTabs = computed(() => [
  { id: 'subscriptions', label: t('订阅出口'), ...(privateSourcesReady.value ? { count: subscriptionIPs.value.length } : {}) },
  { id: 'manual', label: t('独立出口'), ...(privateSourcesReady.value ? { count: manualIPs.value.length } : {}) },
  { id: 'sources', label: t('订阅来源'), ...(privateSourcesReady.value ? { count: privateSources.value.length } : {}) },
]);
function sourceLabel(p: IPResource) {
  const names = privateSources.value.filter(row => row.source.id === p.subscription_id || row.resource_ids?.includes(p.id)).map(row => row.source.name);
  return names.join(' · ') || (p.subscription_id ? t('订阅来源') : t('独立添加'));
}
function selectPrivateTab(value: string) {
  privateTab.value = value;
  sourceFilter.value = null;
  ipSearch.value = '';
  ipType.value = 'all';
  ipState.value = 'all';
  history.replaceState(null, '', '#ips/' + value);
}
function viewSourceResources(ids: string[], name: string) {
  selectPrivateTab('subscriptions');
  sourceFilter.value = { ids, name };
  nextTick(() => window.scrollTo({ top: 0, behavior: 'instant' }));
}
async function sourceChanged() { await refresh(true); await loadPrivateSources(); }
watch(page, value => { if (value === 'ips') loadPrivateSources(); });

const selectedIP = computed(() =>
  ipPool.value.find((p) => p.id === nodeForm.exit_id),
);
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
const nodePoolLabel = (n: Node) =>
  state.value?.ip_pool.find((p) => p.id === n.exit_id)?.label ||
  (n.exit === "direct" ? t("本机直连") : exitName(n.exit));
const expiringUsers = computed(
  () =>
    (state.value?.users || []).filter(
      (u) =>
        u.expires > Date.now() / 1000 &&
        u.expires < Date.now() / 1000 + 7 * 86400,
    ).length,
);
const passwordForm = reactive({ current: "", password: "", confirm: "" });
const sub = ref<Sub | null>(null),
  subUser = ref(0),
  format = ref("mihomo"),
  subProtocol = ref(""),
  qr = ref(""),
  qrError = ref(""),
  subLoading = ref(false),
  subError = ref(""),
  probeResult = ref("");
const detecting = ref("");
const theme = ref(
  readPreference("guangyue-theme") === "light" ? "light" : "dark",
);
const sideCollapsed = ref(
  readPreference("guangyue-sidebar") === "collapsed",
);
const viewMode = ref<ViewMode>(
  readPreference("guangyue-ui-mode") === "simple" ? "simple" : "professional",
);
const simpleMode = computed(() => viewMode.value === "simple");
watch(viewMode, (value) => writePreference("guangyue-ui-mode", value));
const nodeSearch = ref(""),
  nodeProtocol = ref("all"),
  nodeStatus = ref("all");
const publicNodePage=computed(()=>page.value==="public-nodes");
const publicSubPage=computed(()=>page.value==="public-subscription");
const filteredNodes = computed(() =>
  (state.value?.nodes || []).filter(
    (n) =>
      (n.managed_by === "public_proxy") === publicNodePage.value &&
      [n.name, n.probe_ip, n.country, n.host]
        .join(" ")
        .toLowerCase()
        .includes(nodeSearch.value.toLowerCase()) &&
      (nodeProtocol.value === "all" || n.protocol === nodeProtocol.value) &&
      (nodeStatus.value === "all" ||
        n.enabled === (nodeStatus.value === "enabled")),
  ),
);
watch(
  theme,
  (value) => {
    document.documentElement.dataset.theme = value;
    writePreference("guangyue-theme", value);
  },
  { immediate: true },
);
watch(sideCollapsed, (value) =>
  writePreference("guangyue-sidebar", value ? "collapsed" : "expanded"),
);
const toggleTheme = () => {
  theme.value = theme.value === "dark" ? "light" : "dark";
};
const originalExit = ref("");
const exitFingerprint = () =>
  JSON.stringify([
    nodeForm.exit_id,
    nodeForm.exit,
    nodeForm.host,
    nodeForm.port,
    nodeForm.username,
    nodeForm.password,
  ]);
const displayedNodeName = computed(
  () =>
    probeResult.value ||
    selectedIP.value?.name ||
    (originalExit.value === exitFingerprint() ? nodeForm.name : "") ||
    (!nodeForm.exit_id ? t("本机直连 · 保存后自动检测") : t("请选择 节点出口")),
);
const confirmation = ref<{
  title: string;
  detail: string;
  action: () => Promise<void>;
} | null>(null);
const owner = computed(() => state.value?.me.role === "owner");
const defaultRealitySNI = computed(() => state.value?.system.reality_sni || "www.cloudflare.com");
const editingDefaultDirect = computed(() => !!nodeForm.default_direct || !!state.value?.nodes.find(n => n.id === nodeForm.id)?.default_direct);
const pendingHY = computed(
  () =>
    (
      state.value?.system as
        | (State["system"] & { hy2_old_connections?: number })
        | undefined
    )?.hy2_old_connections || 0,
);
const titles: Record<string, string> = {
  overview: "仪表盘",
  users: "用户管理",
  ips: "私有 IP 池",
  public: "公共 IP 池",
  nodes: "普通节点",
  "public-nodes": "公共节点",
  "public-subscription": "公共订阅",
  subscription: "普通订阅",
  system: "运维状态",
  messages: "站内信",
  settings: "系统设置",
  sources: "订阅来源",
};
const pageDescriptions: Record<string, string> = {
  overview: "集中查看企业网络用量、出口与运行状态",
  users: "管理成员访问权限、流量配额与有效期",
  ips: "集中维护出口资源，按需分配给接入节点",
  public: "自动发现、筛选和维护全球公开代理出口",
  nodes: "管理普通接入节点、出口代理与公网地址",
  "public-nodes": "独立管理自动公共节点，随代理质量筛选而更新",
  "public-subscription": "独立公共订阅，仅分发当前合格的自动节点",
  subscription: "分发成员连接配置，管理订阅与访问凭据",
  system: "查看服务状态、证书与最近操作记录",
  messages: "集中接收维护通知与账户消息",
  settings: "统一管理企业品牌、访问偏好与成员支持",
  sources: "管理上游订阅链接与每日错峰更新",
};
const allNavGroups = computed(() => [
  {id:'workspace',label:t('工作台'),items:[{id:'overview',label:t('仪表盘'),icon:LayoutDashboard},...(owner.value?[{id:'users',label:t('用户管理'),icon:Users}]:[]),{id:'messages',label:t('站内信'),icon:Mail}]},
  {id:'business',label:t('业务资源'),items:[...(owner.value?[{id:'ips',label:t('私有 IP 池'),icon:Database},{id:'nodes',label:t('普通节点'),icon:RadioTower}]:[]),{id:'subscription',label:t('普通订阅'),icon:QrCode}]},
  {id:'public',label:t('公共代理'),items:[...(owner.value?[{id:'public',label:t('公共 IP 池'),icon:Database},{id:'public-nodes',label:t('公共节点'),icon:Globe2}]:[]),{id:'public-subscription',label:t('公共订阅'),icon:QrCode}]},
  ...(owner.value?[{id:'admin',label:t('系统管理'),items:[{id:'system',label:t('运维状态'),icon:Server},{id:'settings',label:t('系统设置'),icon:Settings2}]}]:[]),
]);
const navGroups = computed(() => {
  if (!simpleMode.value) return allNavGroups.value;
  const allowed = new Set(owner.value
    ? ["overview", "users", "ips", "nodes", "subscription", "settings"]
    : ["subscription"]);
  return allNavGroups.value
    .map(group => ({ ...group, items: group.items.filter(item => allowed.has(item.id)) }))
    .filter(group => group.items.length);
});
const nav = computed(()=>navGroups.value.flatMap(group=>group.items));
watch(() => nav.value.map(item => item.id).join(","), () => {
  if (state.value && !nav.value.some(item => item.id === page.value)) go(owner.value ? "overview" : "subscription");
});
const currentGroup = computed(()=>navGroups.value.find(g=>g.items.some(item=>item.id===page.value))?.label || '');
const active = (u: User) =>
  u.enabled &&
  (!u.expires || u.expires > Date.now() / 1000) &&
  (!u.quota || u.upload + u.download < u.quota);
const userStatus = (u: User) =>
  !u.enabled
    ? t("已停用")
    : u.expires && u.expires <= Date.now() / 1000
      ? t("已到期")
      : u.quota && u.upload + u.download >= u.quota
        ? t("额度用尽")
        : t("正常");
const users = computed(() =>
  (state.value?.users || []).filter(
    (u) =>
      u.username.toLowerCase().includes(userSearch.value.toLowerCase()) &&
      (userRole.value === "all" || u.role === userRole.value) &&
      (userFilter.value === "all" ||
        (userFilter.value === "active"
          ? active(u)
          : userFilter.value === "disabled"
            ? !u.enabled
            : userFilter.value === "expired"
              ? !!u.expires && u.expires <= Date.now() / 1000
              : !!u.quota && u.upload + u.download >= u.quota)),
  ),
);
const subURL = computed(() =>
  sub.value
    ? sub.value.url +
      "?format=" +
      format.value +
      (subProtocol.value ? "&protocol=" + subProtocol.value : "")
    : "",
);
const usage = (u: User) =>
  u.quota ? Math.min(100, ((u.upload + u.download) / u.quota) * 100) : 0;
function bytes(n = 0) {
  if (n < 1024) return n + " B";
  const p = Math.min(4, Math.floor(Math.log(n) / Math.log(1024)));
  return (
    (n / 1024 ** p).toFixed(p > 1 ? 2 : 0) +
    " " +
    ["B", "KB", "MB", "GB", "TB"][p]
  );
}
function date(n: number, time = false) {
  return n
    ? new Date(n * 1000).toLocaleString(
        locale.value,
        time
          ? {
              month: "2-digit",
              day: "2-digit",
              hour: "2-digit",
              minute: "2-digit",
            }
          : { year: "numeric", month: "2-digit", day: "2-digit" },
      )
    : t("不限");
}
function duration(n: number) {
  return n >= 86400
    ? Math.floor(n / 86400) + t(" 天")
    : n >= 3600
      ? Math.floor(n / 3600) + t(" 小时")
      : Math.floor(n / 60) + t(" 分钟");
}
function exitName(v: string) {
  return (
    (
      {
        direct: t("直连"),
        http: "HTTP",
        socks5: "SOCKS5",
        subscription: t("机场节点"),
      } as Record<string, string>
    )[v] || v
  );
}
async function refresh(silent = false) {
  const request = stateRequest.start();
  try {
    const result = await api<State>("/state", "GET", undefined, { signal: request.signal });
    if (!stateRequest.isCurrent(request)) return;
    state.value = result;
    site.value = result.site;
    applyDefaultLocale(site.value.default_locale);
    if (page.value === "ips" && privateTab.value !== "sources") await loadPrivateSources();
    if (stateRequest.isCurrent(request) && !subUser.value && state.value) subUser.value = state.value.me.id;
  } catch (e) {
    if (!isCancelled(e) && !silent && state.value) error.value = (e as Error).message;
  } finally {
    if (stateRequest.isCurrent(request)) ready.value = true;
  }
}
function clearSession() {
  stateRequest.cancel();
  state.value = null;
  privateSources.value = [];
  privateSourcesReady.value = false;
  privateSourcesError.value = "";
  sourceFilter.value = null;
  selectedIPIDs.value = [];
  selectedNodeIDs.value = [];
  importReport.value = null;
  clearSubscription();
  subUser.value = 0;
  qr.value = "";
  login.password = "";
  modal.value = "";
  confirmation.value = null;
  ready.value = true;
}
const removeSessionListener = onSessionExpired(clearSession);
async function task(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await fn();
  } catch (e) {
    if (!isCancelled(e)) error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
let toastTimer: ReturnType<typeof setTimeout> | undefined;
function toast(text: string) {
  clearTimeout(toastTimer);
  notice.value = text;
  toastTimer = setTimeout(() => {
    notice.value = "";
  }, 4000);
}
async function signIn() {
  await task(async () => {
    await api("/login", "POST", login);
    login.password = "";
    await refresh();
    subUser.value = state.value?.me.id || 0;
    go(owner.value ? "overview" : "subscription");
    toast(t("已登录"));
  });
}
async function signOut() {
  await task(async () => {
    await api("/logout", "POST", {});
    invalidateSession();
    clearSession();
  });
}
function go(id: string) {
  const target = id === "sources" ? "ips/sources" : id;
  const [section, nested] = target.split("/");
  id = section;
  if (!nav.value.some(item=>item.id===id)) id = owner.value ? "overview" : "subscription";
  if (id === "ips" && ["subscriptions", "manual", "sources"].includes(nested)) selectPrivateTab(nested);
  if (id === "public") publicTab.value = ["resources", "sources", "policy"].includes(nested) ? nested : "resources";
  page.value = id;
  history.replaceState(null,"","#"+id+(id === "ips" ? "/"+privateTab.value : id === "public" && publicTab.value !== "resources" ? "/"+publicTab.value : ""));
  nextTick(() => window.scrollTo({ top: 0, behavior: "instant" }));
  mobileNav.value = false;
  error.value = "";
}
function editUser(u?: User) {
  editingID.value = u?.id || 0;
  Object.assign(userForm, {
    username: u?.username || "",
    password: "",
    enabled: u?.enabled ?? true,
    vless: u?.vless ?? true,
    hy2: u?.hy2 ?? true,
    expiresDate: u?.expires
      ? new Date(u.expires * 1000).toISOString().slice(0, 10)
      : "",
    quotaGB: u?.quota ? u.quota / 1024 ** 3 : 0,
  });
  modal.value = "user";
  error.value = "";
}
async function saveUser() {
  await task(async () => {
    await api(
      editingID.value ? "/users/" + editingID.value : "/users",
      editingID.value ? "PUT" : "POST",
      {
        username: userForm.username,
        password: userForm.password,
        enabled: userForm.enabled,
        vless: userForm.vless,
        hy2: userForm.hy2,
        quota: Math.round(userForm.quotaGB * 1024 ** 3),
        expires: userForm.expiresDate
          ? Math.floor(
              new Date(userForm.expiresDate + "T23:59:59").getTime() / 1000,
            )
          : 0,
      },
    );
    modal.value = "";
    await refresh();
    toast(t("成员已保存，正在同步协议"));
  });
}
async function toggleUser(u: User) {
  await task(async () => {
    await api("/users/" + u.id, "PUT", {
      username: u.username,
      password: "",
      enabled: !u.enabled,
      vless: u.vless,
      hy2: u.hy2,
      quota: u.quota,
      expires: u.expires,
    });
    await refresh();
    toast(u.enabled ? t("已停用，正在撤销连接权限") : t("已启用"));
  });
}
function confirmUser(u: User, action: string) {
  const detail = (
    {
      revoke:
        t("旧订阅、UUID 和 HY2 凭据将全部失效。HY2 现有连接会被断开；VLESS 现有连接可能持续到自然结束。"),
      "rotate-sub": t("普通订阅旧地址将失效，公共订阅地址不变。已导入节点凭据仍然有效。"),
      "rotate-public-sub": t("公共订阅旧地址将失效，普通订阅地址不变。已导入节点凭据仍然有效。"),
      "reset-traffic": t("已用流量将归零，达到额度上限的账号会恢复可用。"),
      delete: t("将删除此账号并撤销新连接权限，此操作不可恢复。"),
    } as Record<string, string>
  )[action];
  confirmation.value = {
    title:
      (
        {
          revoke: t("撤销全部旧配置"),
          "rotate-sub": t("重置普通订阅地址"),
          "rotate-public-sub": t("重置公共订阅地址"),
          "reset-traffic": t("重置流量"),
          delete: t("删除成员"),
        } as Record<string, string>
      )[action] +
      " · " +
      u.username,
    detail,
    action: async () => {
      await api(
        "/users/" + u.id + (action === "delete" ? "" : "/" + action),
        action === "delete" ? "DELETE" : "POST",
        {},
      );
      modal.value = "";
      await refresh();
      if (sub.value?.user.id === u.id) await loadSub();
      toast(t("操作已保存，正在同步"));
    },
  };
}
async function confirmed() {
  await task(async () => {
    await confirmation.value?.action();
    confirmation.value = null;
  });
}
function editNode(n?: Node, protocol = "vless") {
  if (n?.managed_by) { go('public'); return; }
  Object.assign(
    nodeForm,
    n
      ? { ...n, dns: n.dns ? {...n.dns} : {mode:"system"}, password: "", default_direct: !!n.default_direct, reality_sni: n.protocol === "vless" ? n.reality_sni || "" : "", ...(n.default_direct ? { enabled: true, exit: "direct", exit_id: "" } : {}) }
      : {
          id: "",
          exit_id: "",
          name: "",
          protocol,
          reality_sni: "",
          dns: {mode:"secure", doh:"https://1.1.1.1/dns-query", ipv6:"block"},
          default_direct: false,
          enabled: true,
          exit: "direct",
          host: "",
          port: 0,
          username: "",
          password: "",
          has_password: false,
          probe_ip: "",
          probed_at: 0,
          country: "",
          country_code: "",
          checked_at: 0,
          probe_error: "",
        },
  );
  probeResult.value = "";
  originalExit.value = exitFingerprint();
  modal.value = "node";
  error.value = "";
}
async function probe() {
  await task(async () => {
    const r = await api("/nodes/probe", "POST", {
      id: nodeForm.id,
      protocol: nodeForm.protocol,
      exit_id: editingDefaultDirect.value ? "" : nodeForm.exit_id,
    });
    probeResult.value = r.name;
    if (r.warning) toast(r.warning);
  });
}
async function detectNode(n: Node) {
  await task(async () => {
    detecting.value = n.id;
    try {
      const result = await api("/nodes/" + n.id + "/detect", "POST", {});
      toast(result.probe_error || t("出口名称已更新"));
    } finally {
      detecting.value = "";
      await refresh();
    }
  });
}
watch(
  () => [
    nodeForm.exit,
    nodeForm.host,
    nodeForm.port,
    nodeForm.username,
    nodeForm.password,
  ],
  () => {
    probeResult.value = "";
  },
);
watch(
  () => nodeForm.exit,
  (value) => {
    if (value !== "direct" && nodeForm.port < 1)
      nodeForm.port = value === "http" ? 8080 : 1080;
  },
);
watch(
  () => nodeForm.exit_id,
  () => {
    probeResult.value = "";
  },
);
watch(
  () => ipForm.exit,
  (value) => {
    if (value !== "direct" && ipForm.port < 1)
      ipForm.port = value === "http" ? 8080 : 1080;
  },
);
function editIP(p?: IPResource) {
  Object.assign(
    ipForm,
    p
      ? { ...p, password: "" }
      : {
          id: "",
          exit_id: "",
          name: t("待检测出口"),
          protocol: "vless",
          enabled: true,
          exit: "socks5",
          host: "",
          port: 1080,
          username: "",
          password: "",
          has_password: false,
          probe_ip: "",
          probed_at: 0,
          country: "",
          country_code: "",
          checked_at: 0,
          probe_error: "",
          label: "",
          notes: "",
          revision: "",
          reachable: false,
          node_ids: [],
        },
  );
  error.value = "";
  modal.value = "ip";
}
async function saveIP() {
  await task(async () => {
    const creating = !ipForm.id;
    const p: IPResource = await api("/ips", "POST", ipForm);
    modal.value = "";
    if (creating) go("ips/manual");
    await refresh();
    toast(
      p.enabled && p.probe_error
        ? t("IP 已保存，检测结果：") + p.probe_error
        : t("IP 资源已保存"),
    );
  });
}
async function detectIP(p: IPResource) {
  await task(async () => {
    detecting.value = p.id;
    try {
      const result: IPResource = await api(
        "/ips/" + p.id + "/detect",
        "POST",
        {},
      );
      await refresh();
      toast(result.probe_error || t("出口 IP 与国家已更新"));
    } finally {
      detecting.value = "";
    }
  });
}
function deleteIP(p: IPResource) {
  confirmation.value = {
    title: t("删除 IP 资源"),
    detail: t("删除 ") + (p.label || p.name) + " · " + t("同时删除绑定节点") + " " + (p.node_ids?.length || 0) + " · " + t("对应链接将从所有用户订阅移除。"),
    action: async () => {
      await api("/ips/" + p.id, "DELETE", {});
      await refresh();
      toast(t("IP 资源已删除"));
    },
  };
}
const selectedIPIDs = ref<string[]>([]), selectedNodeIDs = ref<string[]>([]);
const selectableNodes = computed(() => filteredNodes.value.filter(n => !n.default_direct));
watch([filteredIPs, filteredNodes, page, privateTab], () => {
  const ips=new Set(filteredIPs.value.map(p=>p.id)), nodes=new Set(selectableNodes.value.map(n=>n.id));
  selectedIPIDs.value=selectedIPIDs.value.filter(id=>ips.has(id));
  selectedNodeIDs.value=selectedNodeIDs.value.filter(id=>nodes.has(id));
});
function selectAll(kind: 'ips'|'nodes', event: Event) {
  const checked=(event.target as HTMLInputElement).checked;
  if (kind==='ips') selectedIPIDs.value=checked?filteredIPs.value.map(p=>p.id):[];
  else selectedNodeIDs.value=checked?selectableNodes.value.map(n=>n.id):[];
}
function batchAction(kind: 'ips'|'nodes', action: 'delete'|'enable'|'disable') {
  const ids=[...(kind==='ips'?selectedIPIDs.value:selectedNodeIDs.value)];
  if (!ids.length) return;
  const bound=kind==='ips' ? new Set(ipPool.value.filter(p=>ids.includes(p.id)).flatMap(p=>p.node_ids||[])).size : ids.length;
  confirmation.value={
    title: (action==='delete'?t('批量删除'):action==='enable'?t('批量启用'):t('批量停用'))+' · '+ids.length,
    detail: action==='delete' ? t('同时删除绑定节点')+' '+bound+' · '+t('对应链接将从所有用户订阅移除。') : action==='disable'&&kind==='ips' ? t('停用出口会同时停用绑定节点。') : kind==='ips'?t('启用出口后，可按需启用对应节点。'):t('将一次性应用所选节点状态。'),
    action: async()=>{
      const result=await api('/'+kind+'/batch','POST',{ids,action});
      if(kind==='ips')selectedIPIDs.value=[];else selectedNodeIDs.value=[];
      await refresh();toast(t('批量操作已完成')+' · '+result.selected);
    }
  };
}
async function saveNode() {
  await task(async () => {
    
    if (!editingDefaultDirect.value && nodeForm.exit_id && !selectedIP.value?.enabled)
      throw new Error(t("请选择已启用的 IP 池资源"));
    const realitySNI = (nodeForm.reality_sni || "").trim().toLowerCase();
    if (nodeForm.protocol === "vless" && realitySNI && (realitySNI.length > 63 || !/^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(realitySNI)))
      throw new Error(t("SNI 请输入最多 63 字符的域名，不含协议、端口、路径或 IP 地址。"));
    const sniChanged = nodeForm.protocol === "vless" && realitySNI !== (state.value?.nodes.find(n => n.id === nodeForm.id)?.reality_sni || "");
    await api("/nodes", "POST", {
      id: nodeForm.id,
      protocol: nodeForm.protocol,
      enabled: editingDefaultDirect.value ? true : nodeForm.enabled,
      exit_id: editingDefaultDirect.value ? "" : nodeForm.exit_id,
      ...(nodeForm.protocol === "vless" ? { reality_sni: realitySNI } : {}),
      dns: {...nodeForm.dns},
    });
    modal.value = "";
    await refresh();
    toast(sniChanged ? t("节点已应用，请更新客户端订阅以使用新的 SNI。") : t("节点已应用，出口名称已更新"));
  });
}
function deleteNode(n: Node) {
  if (n.default_direct || state.value?.nodes.find(node => node.id === n.id)?.default_direct) {
    error.value = t("默认直连节点不能删除；绑定 IP 池出口请新增节点。");
    return;
  }
  confirmation.value = {
    title: t("删除节点 · ") + n.name,
    detail:
      t("该节点将从全部订阅移除，") + n.protocol.toUpperCase() + t(" 连接将重连。"),
    action: async () => {
      await api("/nodes/" + n.id, "DELETE", {});
      modal.value = "";
      await refresh();
      toast(t("节点已删除"));
    },
  };
}
let subscriptionRequest: AbortController | null = null, subscriptionSequence = 0;
function clearSubscription() {
  subscriptionSequence++;
  subscriptionRequest?.abort();
  subscriptionRequest = null;
  sub.value = null;
  subLoading.value = false;
  subError.value = "";
}
async function loadSub(clear = false) {
  if (!state.value || !["subscription", "public-subscription"].includes(page.value)) {
    clearSubscription();
    return;
  }
  subscriptionRequest?.abort();
  const controller = new AbortController(), sequence = ++subscriptionSequence;
  subscriptionRequest = controller;
  const requestPage = page.value, requestUser = owner.value ? subUser.value || state.value.me.id : state.value.me.id;
  const requestPool = publicSubPage.value ? "public" : "private", requestProtocol = subProtocol.value;
  if (clear) sub.value = null;
  subLoading.value = true;
  subError.value = "";
  try {
    const query = new URLSearchParams({ user_id: String(requestUser), pool: requestPool });
    if (requestProtocol) query.set("protocol", requestProtocol);
    const loaded = await api<Sub>("/subscription?" + query, "GET", undefined, { signal: controller.signal });
    if (controller.signal.aborted || sequence !== subscriptionSequence) return;
    if (page.value !== requestPage || (owner.value ? subUser.value || state.value?.me.id : state.value?.me.id) !== requestUser || subProtocol.value !== requestProtocol) return;
    if (loaded.user?.id !== requestUser || loaded.pool !== requestPool || !Array.isArray(loaded.nodes)) throw new Error(t("订阅节点明细暂不可用，请刷新重试。"));
    sub.value = loaded as Sub;
  } catch (e) {
    if (!controller.signal.aborted && sequence === subscriptionSequence) {
      subError.value = (e as Error).message;
      sub.value = null;
    }
  } finally {
    if (sequence === subscriptionSequence) {
      subLoading.value = false;
      subscriptionRequest = null;
    }
  }
}
function showSub(u: User) {
  subUser.value = u.id;
  go("subscription");
}
async function copy(s: string) {
  try {
    await navigator.clipboard.writeText(s);
    toast(t("已复制"));
  } catch {
    error.value = t("剪贴板不可用，请选择内容复制");
  }
}
async function downloadSub() {
  await task(async () => {
    download(
      await downloadBlob(subURL.value),
      (publicSubPage.value ? "guangyue-public." : "guangyue.") + (format.value === "mihomo" ? "yaml" : "txt"),
    );
  });
}
function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
async function backup() {
  confirmation.value = {
    title: t("下载完整备份"),
    detail: t("备份含账号、节点凭据与恢复密钥，请仅保存在可信设备。"),
    action: async () => {
      download(
        await downloadBlob("/api/backup"),
        "guangyue-backup-" + new Date().toISOString().slice(0, 10) + ".tar.gz",
      );
      toast(t("备份已下载"));
    },
  };
}
async function changePassword() {
  await task(async () => {
    if (passwordForm.password !== passwordForm.confirm)
      throw new Error(t("两次密码不一致"));
    await api("/password", "POST", {
      current: passwordForm.current,
      password: passwordForm.password,
    });
    modal.value = "";
    state.value = null;
    Object.assign(passwordForm, { current: "", password: "", confirm: "" });
    toast(t("密码已修改，请重新登录"));
  });
}
const actionName = (s: string) =>
  (
    ({
      initialize: t("初始化"),
      create_user: t("创建成员"),
      save_ip: t("保存 IP 资源"),
      delete_ip: t("删除 IP 资源"),
      ip_apply_failed: t("IP 出口应用失败"),
      delete_user: t("删除成员"),
      save_node: t("应用节点"),
      delete_node: t("删除节点"),
      node_apply_failed: t("节点应用失败"),
      download_backup: t("下载备份"),
      change_password: t("修改密码"),
      import_subscription: t("导入订阅或节点"),
      speed_test: t("出口测速"),
    }) as Record<string, string>
  )[s] || (s.startsWith("update_user") ? t("更新成员") : s);
watch([() => {
  const userID = owner.value ? subUser.value || state.value?.me.id : state.value?.me.id;
  const user = state.value?.users.find(u => u.id === userID) || (state.value?.me.id === userID ? state.value?.me : undefined);
  return JSON.stringify([state.value?.nodes.map(n => [n.id, n.enabled, n.reality_sni, n.dns]), user?.id, user?.vless, user?.hy2, user ? active(user) : false]);
}, () => JSON.stringify(state.value?.nodes.map(n => [n.id, n.checked_at, n.quality?.at]))], ([authorization], [previousAuthorization]) => {
  if (page.value === 'subscription' || page.value === 'public-subscription') loadSub(authorization !== previousAuthorization);
});
watch([page, subUser, subProtocol], () => { loadSub(true); }, { flush: 'sync' });
let subscriptionQRSequence = 0;
watch(subURL, async (v) => {
  const sequence = ++subscriptionQRSequence;
  qr.value = "";
  qrError.value = "";
  if (!v) return;
  try {
    const image = await QRCode.toDataURL(v, {
        width: 240,
        margin: 2,
        color: { dark: "#172c29", light: "#ffffff" },
      });
    if (sequence === subscriptionQRSequence && subURL.value === v) qr.value = image;
  } catch {
    if (sequence === subscriptionQRSequence) qrError.value = t("订阅二维码生成失败，请使用复制地址。");
  }
});
const poll = serialPoll(async () => {
  if (state.value && !busy.value && !document.hidden) await refresh(true);
}, () => 10000);
let mounted = true;
const handleHashChange = () => { if (state.value) go(location.hash.slice(1)); };
onMounted(async () => {
  const initial = location.hash.slice(1);
  try { site.value=await api<SiteSettings>('/site'); applyDefaultLocale(site.value.default_locale); } catch {}
  if (!mounted) return;
  await refresh(true);
  if (state.value) go(initial || (owner.value ? "overview" : "subscription"));
  if (!mounted) return;
  window.addEventListener("hashchange", handleHashChange);
  poll.start();
});
onUnmounted(() => { mounted = false; poll.stop(); stateRequest.cancel(); removeSessionListener(); clearTimeout(toastTimer); window.removeEventListener("hashchange", handleHashChange); clearSubscription(); subscriptionQRSequence++; });
</script>

<template>
  <div v-if="!ready" class="loading-screen">
    <LoaderCircle class="spin" :size="28" />
  </div>
  <main v-else-if="!state" class="login-screen">
    <div class="login-language"><LanguageSwitcher/></div>
    <div class="login-brand">
      <Building2 :size="32" /><span
        >{{site.panel_name}}<small>{{site.organization}}</small></span
      >
    </div>
    <form class="login-form" @submit.prevent="signIn">
      <div class="eyebrow">GUANGYUE PANEL</div>
      <h1>{{ t("登录企业控制台") }}</h1>
      <p v-if="site.login_notice" class="login-notice">{{site.login_notice}}</p>
      <label
        >{{ t("账号") }}<input
          v-model="login.username"
          autocomplete="username"
          required
          autofocus
          :placeholder="t('用户名')"
          maxlength="32"
      /></label>
      <label
        >{{ t("密码") }}<input
          v-model="login.password"
          type="password"
          autocomplete="current-password"
          required
          :placeholder="t('密码')"
          maxlength="72"
      /></label>
      <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
      <button class="primary full" :disabled="busy">
        <LoaderCircle v-if="busy" class="spin" :size="18" /><span>{{ t("登录") }}</span
        ><ArrowUpRight :size="18" />
      </button>
      <div class="login-security">
        <ShieldCheck :size="15" />{{ t("企业成员授权访问") }}</div>
    </form>
    <footer>{{site.panel_name}} · {{site.organization}}</footer>
  </main>
  <div
    v-else
    :class="['app-shell', { collapsed: sideCollapsed, 'simple-mode': simpleMode }]"
    :inert="!!modal || !!confirmation"
  >
    <div v-if="mobileNav" class="nav-shade" @click="mobileNav = false"></div>
    <aside :class="['sidebar', { open: mobileNav }]">
      <a
        class="brand"
        href="#"
        :aria-label="site.panel_name + ' · ' + t('仪表盘')"
        @click.prevent="go('overview')"
        ><span class="brand-icon"><RadioTower :size="25" /></span
        ><span class="brand-name"
          >{{site.panel_name}}<small
            >{{ t("企业控制台 ·") }}{{ state.system.version.split("-")[0] }}</small
          ></span
        ></a
      >
      <div class="workspace">
        <span class="workspace-symbol"><Building2 :size="16" /></span>
        <div>
          {{site.organization}}<small>{{
            state.system.panel_host.replace(/^https?:\/\//, "")
          }}</small>
        </div>
        <ShieldCheck :size="17" />
      </div>
      <nav :aria-label="t('主导航')" class="grouped-nav">
        <div v-for="group in navGroups" :key="group.id" :class="['nav-group','nav-group-'+group.id]">
          <div class="nav-caption">{{group.label}}</div>
          <button v-for="item in group.items" :key="item.id" :class="{selected:page===item.id}" :title="item.label" :aria-label="item.label" :aria-current="page===item.id?'page':undefined" @click="go(item.id)">
            <component :is="item.icon" :size="18"/><span>{{item.label}}</span>
            <span v-if="item.id==='messages' && state.unread_messages" class="nav-count unread-count">{{state.unread_messages>99?'99+':state.unread_messages}}</span>
          </button>
        </div>
      </nav>
      <div class="sidebar-bottom">
        <button class="sidebar-setting mobile-account" :title="t('账户与密码')" @click="modal='password';mobileNav=false"><KeyRound :size="18"/><span>{{t('账户与密码')}}</span></button>
        <div class="edge-status">
          <span
            :class="['dot', { warning: state.system.status !== 'applied' }]"
          />{{
            state.system.status === "applied"
              ? t("配置已同步")
              : state.system.status === "pending"
                ? t("等待同步")
                : t("同步异常")
          }}<span>443</span>
        </div>
        <button
          class="sidebar-setting"
          :title="theme === 'dark' ? t('切换浅色模式') : t('切换深色模式')"
          @click="toggleTheme"
        >
          <Sun v-if="theme === 'dark'" :size="18" /><Moon
            v-else
            :size="18"
          /><span>{{ theme === "dark" ? t("浅色模式") : t("深色模式") }}</span>
        </button>
        <button
          class="sidebar-setting collapse-control"
          :title="sideCollapsed ? t('展开导航') : t('收起导航')"
          @click="sideCollapsed = !sideCollapsed"
        >
          <PanelLeftOpen v-if="sideCollapsed" :size="18" /><PanelLeftClose
            v-else
            :size="18"
          /><span>{{ sideCollapsed ? t("展开导航") : t("收起导航") }}</span>
        </button>
      </div>
    </aside>
    <div class="main-area">
      <header class="topbar">
        <button
          class="icon mobile-menu"
          :title="t('打开导航')"
          @click="mobileNav = !mobileNav"
        >
          <Menu :size="21" />
        </button>
        <div class="page-context">
          <h1>
            <span class="breadcrumb-group">{{currentGroup}} <ChevronRight :size="13"/></span>{{ t(titles[page]) }}
          </h1>
          <p>{{ t(pageDescriptions[page]) }}</p>
        </div>
        <div class="top-actions">
          <ViewModeSwitcher v-model="viewMode"/>
          <LanguageSwitcher/>
          <button v-if="!simpleMode" class="icon inbox-bell" :title="t('站内信')" :aria-label="t('站内信')" @click="go('messages')"><Bell :size="18"/><span v-if="state.unread_messages" class="bell-count">{{state.unread_messages>99?'99+':state.unread_messages}}</span></button>
          <span class="live-label"
            ><span class="dot" />{{ date(state.system.applied_at, true) }}</span
          ><button class="icon" :title="t('刷新')" @click="refresh()">
            <RefreshCw :size="17" /></button
          ><button
            class="icon header-theme"
            :title="theme === 'dark' ? t('切换浅色模式') : t('切换深色模式')"
            @click="toggleTheme"
          >
            <Sun v-if="theme === 'dark'" :size="18" /><Moon
              v-else
              :size="18"
            /></button
          ><button
            class="header-account"
            :title="t('账户与密码')"
            @click="modal = 'password'"
          >
            <span class="avatar small">{{
              state.me.username.slice(0, 1).toUpperCase()
            }}</span
            ><span
              >{{ state.me.username
              }}<small>{{ owner ? t("管理员") : t("企业成员") }}</small></span
            ></button
          ><button class="icon" :title="t('退出登录')" @click="signOut">
            <LogOut :size="17" />
          </button>
        </div>
      </header>
      <div
        v-if="error && !modal && !confirmation"
        class="page-error"
        role="alert"
      >
        {{ t(error)
        }}<button class="icon" :title="t('关闭提示')" @click="error = ''">
          <X :size="16" />
        </button>
      </div>
      <div
        v-if="state.system.status === 'error'"
        class="sync-alert"
        role="alert"
      >{{ t("协议同步异常：") }}{{ state.system.error
        }}<button
          v-if="owner"
          @click="
            task(async () => {
              await api('/apply', 'POST', {});
              await refresh();
            })
          "
        >{{ t("重试") }}</button>
      </div>
      <div v-if="pendingHY" class="sync-alert" role="status">{{ t("正在关闭") }}{{ pendingHY }}{{ t("个旧 HY2 连接") }}</div>
      <div class="content">
        <Inbox v-if="page==='messages'" :owner="owner" :users="state.users" :me="state.me.id" :support-email="site.support_email" :unread="state.unread_messages" @refresh="refresh(true)"/>
        <PanelSettings v-else-if="page==='settings' && owner" :simple-mode="simpleMode" @saved="refresh()"/>
        <Dashboard v-else-if="page==='overview'" :state="state" :simple="simpleMode" @navigate="go" @refresh="refresh(true)"/>
        <section v-else-if="page === 'users'">
          <div class="page-heading">
            <div>
              <div class="eyebrow">ACCESS</div>
              <h1>{{ t("用户管理") }}<span class="count">{{ state.users.length }}</span>
              </h1>
            </div>
            <button class="primary" @click="editUser()">
              <Plus :size="17" />{{ t("添加成员") }}</button>
          </div>
          <div class="user-stat-grid">
            <div>
              <span>{{ t("全部用户") }}</span><strong>{{ state.users.length }}</strong>
            </div>
            <div>
              <span>{{ t("当前可用") }}</span><strong>{{ state.totals.active }}</strong>
            </div>
            <div>
              <span>{{ t("7 天内到期") }}</span><strong>{{ expiringUsers }}</strong>
            </div>
            <div>
              <span>{{ t("累计流量") }}</span
              ><strong>{{
                bytes(state.totals.upload + state.totals.download)
              }}</strong>
            </div>
          </div>
          <div class="toolbar user-toolbar">
            <div class="search">
              <Search :size="17" /><input
                v-model="userSearch"
                :placeholder="t('搜索成员账号')"
                :aria-label="t('搜索成员')"
              />
            </div>
            <select v-model="userFilter" :aria-label="t('账号状态')">
              <option value="all">{{ t("全部状态") }}</option>
              <option value="active">{{ t("正常") }}</option>
              <option value="disabled">{{ t("已停用") }}</option>
              <option value="expired">{{ t("已到期") }}</option>
              <option value="exhausted">{{ t("额度用尽") }}</option></select
            ><select v-model="userRole" :aria-label="t('用户角色')">
              <option value="all">{{ t("全部角色") }}</option>
              <option value="owner">{{ t("管理员") }}</option>
              <option value="user">{{ t("企业成员") }}</option></select
            ><span class="spacer"></span
            ><span class="muted">{{ users.length }}{{ t("位成员") }}</span>
          </div>
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>{{ t("成员") }}</th>
                  <th>{{ t("状态") }}</th>
                  <th>{{ t("协议") }}</th>
                  <th class="usage-cell">{{ t("流量使用") }}</th>
                  <th>{{ t("到期时间") }}</th>
                  <th>{{ t("最近订阅") }}</th>
                  <th class="right">{{ t("操作") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="u in users" :key="u.id">
                  <td>
                    <div class="user-cell">
                      <span class="avatar small">{{
                        u.username.slice(0, 1).toUpperCase()
                      }}</span>
                      <div>
                        <strong>{{ u.username }}</strong
                        ><small>{{
                          u.role === "owner" ? t("管理员") : t("成员 #") + u.id
                        }}</small>
                      </div>
                    </div>
                  </td>
                  <td>
                    <span
                      :class="['badge', active(u) ? 'success' : 'danger']"
                      >{{ userStatus(u) }}</span
                    >
                  </td>
                  <td>
                    <div class="protocol-tags">
                      <span v-if="u.vless" class="tag vless">VLESS</span
                      ><span v-if="u.hy2" class="tag hy2">HY2</span
                      ><span v-if="!u.vless && !u.hy2" class="muted">{{ t("无") }}</span>
                    </div>
                  </td>
                  <td>
                    <div class="usage-text">
                      {{ bytes(u.upload + u.download)
                      }}<span>/ {{ u.quota ? bytes(u.quota) : t("不限") }}</span>
                    </div>
                    <div class="progress">
                      <span
                        :style="{ width: usage(u) + '%' }"
                        :class="{ exhausted: usage(u) >= 100 }"
                      />
                    </div>
                  </td>
                  <td>{{ date(u.expires) }}</td>
                  <td>
                    {{ u.last_sub ? date(u.last_sub, true) : t("尚未获取") }}
                  </td>
                  <td>
                    <div class="row-actions">
                      <button class="icon" :title="t('查看订阅')" @click="showSub(u)">
                        <QrCode :size="17" /></button
                      ><button
                        class="icon"
                        :title="t('编辑成员')"
                        @click="editUser(u)"
                      >
                        <Pencil :size="16" /></button
                      ><button
                        class="icon"
                        :title="u.enabled ? t('停用成员') : t('启用成员')"
                        :disabled="u.role === 'owner' || busy"
                        @click="toggleUser(u)"
                      >
                        <Power :size="17" />
                      </button>
                    </div>
                  </td>
                </tr>
                <tr v-if="!users.length">
                  <td colspan="7" class="empty">{{ t("没有匹配的成员") }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <section v-else-if="page === 'ips' && owner" class="private-pool-page">
          <div class="page-heading">
            <div><h1>{{ t("私有 IP 池") }}</h1><p class="resource-page-description">{{t("按来源管理企业出口，统一检测与分配")}}</p></div>
            <div class="heading-actions">
              <button @click="openImport()">
                <Download :size="17" />{{ t("导入订阅 / 节点") }}</button
              ><button class="primary" @click="editIP()">
                <Plus :size="17" />{{ t("添加 IP 资源") }}</button>
            </div>
          </div>
          <div class="user-stat-grid">
            <div>
              <span>{{ t("IP 池资源") }}</span><strong>{{ poolStats.total }}</strong>
            </div>
            <div>
              <span>{{ t("检测可用") }}</span><strong>{{ poolStats.ready }}</strong>
            </div>
            <div>
              <span>{{ t("已分配") }}</span><strong>{{ poolStats.used }}</strong>
            </div>
            <div>
              <span>{{ t("未分配") }}</span
              ><strong>{{ poolStats.total - poolStats.used }}</strong>
            </div>
          </div>
          <ResourceTabs id="private-pool" :model-value="privateTab" :items="privateTabs" :label="t('私有 IP 池分类')" @update:model-value="selectPrivateTab"/>
          <div id="private-pool-panel" role="tabpanel" :aria-labelledby="'private-pool-tab-' + privateTab">
          <ImportSources v-if="privateTab === 'sources'" embedded @refresh="sourceChanged" @resources="viewSourceResources" @loaded="receivePrivateSources"/>
          <div v-else-if="!privateSourcesReady" class="resource-membership-state" :aria-busy="privateSourcesLoading" role="status"><LoaderCircle v-if="privateSourcesLoading" :size="23" class="spin"/><Database v-else :size="23"/><h2>{{privateSourcesLoading ? t('正在确认出口来源…') : t('暂时无法确认出口分类')}}</h2><p>{{privateSourcesLoading ? t('读取订阅关联后展示出口分类，避免将订阅出口误列为独立出口。') : t(privateSourcesError)}}</p><button v-if="!privateSourcesLoading" @click="loadPrivateSources"><RefreshCw :size="14"/>{{t('重新读取来源')}}</button><button class="text-button" @click="selectPrivateTab('sources')">{{t('管理订阅来源')}}</button></div>
          <template v-else>
          <div v-if="privateSourcesError" class="resource-membership-notice" role="status"><span>{{t('来源信息刷新失败，当前保留上次确认的分类。')}}</span><button class="text-button" :disabled="privateSourcesLoading" @click="loadPrivateSources"><RefreshCw :size="13" :class="{spin:privateSourcesLoading}"/>{{t('重试')}}</button></div>
          <div class="resource-section-heading"><div><h2>{{privateTab === 'subscriptions' ? t('订阅出口') : t('独立出口')}}</h2><p>{{privateTab === 'subscriptions' ? t('由已管理的上游订阅同步，更新时保留节点绑定') : t('单独添加或一次性导入的出口，由你独立维护')}}</p></div><button class="text-button" @click="go('nodes')">{{t('分配至节点')}}<ArrowUpRight :size="15"/></button></div>
          <div v-if="sourceFilter" class="resource-source-filter"><Database :size="15"/><strong>{{sourceFilter.name}}</strong><button class="text-button" @click="sourceFilter=null"><X :size="13"/>{{t('清除来源筛选')}}</button></div>
          <div v-if="importReport && !importReport.queued && privateTab === 'manual'" class="import-report" role="status">
            <div>
              <strong>{{ t("独立出口导入结果") }}</strong>
              <p>{{ t("新增") }} {{ importReport.added }} {{ t("项 · 重复跳过") }} {{ importReport.duplicates }} {{ t("项 · 错误跳过") }} {{ importReport.warnings.length }} {{ t("项") }}</p>
              <details v-if="importReport.warnings.length">
                <summary>{{ t("查看跳过原因") }}</summary>
                <p v-for="w in importReport.warnings" :key="w.index">{{ t("行 / 项") }} {{ w.index }} · {{ t(w.reason) }}
                </p>
              </details>
            </div>
            <button
              class="icon"
              :title="t('关闭导入结果')"
              @click="importReport = null"
            >
              <X :size="16" />
            </button>
          </div>
          <div class="toolbar pool-toolbar">
            <div class="search">
              <Search :size="17" /><input
                v-model="ipSearch"
                :placeholder="t('搜索国家、IP、备注或代理地址')"
                :aria-label="t('搜索 IP')"
              />
            </div>
            <select v-model="ipType" :aria-label="t('IP 出口类型')">
              <option value="all">{{ t("全部类型") }}</option>
              <option value="subscription">{{ t("机场节点") }}</option>

              <option value="http">HTTP CONNECT</option>
              <option value="socks5">SOCKS5</option></select
            ><select v-model="ipState" :aria-label="t('IP 资源状态')">
              <option value="all">{{ t("全部状态") }}</option>
              <option value="ready">{{ t("检测可用") }}</option>
              <option value="attention">{{ t("待检测 / 异常") }}</option>
              <option value="unassigned">{{ t("未分配") }}</option>
              <option value="disabled">{{ t("已停用") }}</option></select
            ><span class="spacer"></span
            ><span class="muted">{{ filteredIPs.length }}{{ t("项资源") }}</span>
          </div>
          <div v-if="selectedIPIDs.length" class="batch-toolbar" role="region" :aria-label="t('批量操作')">
            <strong>{{t('已选择')}} {{selectedIPIDs.length}}</strong>
            <button :disabled="busy" @click="batchAction('ips','enable')">{{t('批量启用')}}</button>
            <button :disabled="busy" @click="batchAction('ips','disable')">{{t('批量停用')}}</button>
            <button v-if="privateTab === 'manual'" class="danger-button" :disabled="busy" @click="batchAction('ips','delete')"><Trash2 :size="14"/>{{t('批量删除')}}</button>
            <button class="text-button" :disabled="busy" @click="selectedIPIDs=[]">{{t('清空选择')}}</button>
          </div>
          <div class="table-scroll pool-table">
            <table>
              <thead>
                <tr>
                  <th class="select-cell"><input type="checkbox" :aria-label="t('选择全部当前结果')" :disabled="busy || !filteredIPs.length" :checked="!!filteredIPs.length && selectedIPIDs.length === filteredIPs.length" :indeterminate="!!selectedIPIDs.length && selectedIPIDs.length < filteredIPs.length" @change="selectAll('ips',$event)"/></th>
                  <th>{{ t("出口 IP / 国家") }}</th>
                  <th>{{ t("备注名称") }}</th>
                  <th>{{ t("出口类型 / 地址") }}</th>
                  <th>{{ t("检测状态") }}</th>
                  <th>{{ t("绑定节点") }}</th>
                  <th>{{ t("出口测速 · VPS") }}</th>
                  <th class="right">{{ t("操作") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="p in filteredIPs" :key="p.id">
                  <td class="select-cell"><input type="checkbox" v-model="selectedIPIDs" :value="p.id" :aria-label="t('选择')+' '+ p.name" :disabled="busy"/></td>
                  <td>
                    <div class="user-cell">
                      <CountryMark
                        :code="p.country_code"
                        :country="p.country"
                      />
                      <div>
                        <strong>{{ p.probe_ip || t("等待检测") }}</strong
                        ><small
                          >{{ p.country || t("国家待识别")
                          }}<span v-if="p.country_code">
                            · {{ p.country_code }}</span
                          ></small
                        >
                      </div>
                    </div>
                  </td>
                  <td>
                    <strong>{{ p.label || t("未命名资源") }}</strong
                    ><small class="table-detail pool-note" :title="p.notes">{{
                      p.notes || "—"
                    }}</small>
                    <small class="table-detail resource-origin">{{sourceLabel(p)}}</small>
                    <span v-if="p.source_stale" class="badge neutral">{{t('来源已移除')}}</span>
                    <QualityTags :value="p.quality" :name="p.label || p.name" :running="qualityRunning === p.id" @run="qualityTest(p, true)" />
                  </td>
                  <td>
                    <span class="badge neutral">{{
                      p.upstream_type?.toUpperCase() || exitName(p.exit)
                    }}</span
                    ><small class="table-detail mono">{{
                      p.exit === "direct"
                        ? t("VPS 本机出口")
                        : p.host + ":" + p.port
                    }}</small>
                  </td>
                  <td>
                    <span :class="['badge', ipBadge(p)]"
                      ><i :class="['dot', { warning: !p.reachable }]" />{{
                        ipStatus(p)
                      }}</span
                    ><small
                      v-if="p.probe_error"
                      class="table-warning"
                      :title="p.probe_error"
                      >{{
                        p.reachable ? t("地理信息待更新") : t("检测失败，请检查出口")
                      }}</small
                    >
                  </td>
                  <td>
                    <span
                      :class="[
                        'badge',
                        p.node_ids?.length ? 'neutral' : 'unassigned',
                      ]"
                      >{{ p.node_ids?.length || 0 }}{{ t("个节点") }}</span
                    ><small class="table-detail">{{
                      [
                        ...new Set(
                          state.nodes
                            .filter((n) => n.exit_id === p.id)
                            .map((n) => n.protocol.toUpperCase()),
                        ),
                      ].join(" · ") || t("可供分配")
                    }}</small>
                  </td>
                  <td>
                    <SpeedMetric
                      :result="p.speed"
                      :running="speedRunning === p.id"
                    />
                  </td>
                  <td>
                    <div class="row-actions">
                      <button
                        class="icon speed-action"
                        :title="t('测速 IP ') + (p.label || p.name)"
                        :disabled="busy || !p.enabled"
                        @click="speedTest(p, true)"
                      >
                        <Gauge :size="16" />
                      </button>
                      <button
                        class="icon"
                        :title="t('检测 IP ') + (p.label || p.name)"
                        :disabled="busy"
                        @click="detectIP(p)"
                      >
                        <RefreshCw
                          :size="16"
                          :class="{ spin: detecting === p.id }"
                        /></button
                      ><button
                        class="icon"
                        :title="t('编辑 IP ') + (p.label || p.name)"
                        @click="editIP(p)"
                      >
                        <Pencil :size="16" /></button
                      ><button
                        class="icon danger-button"
                        :title="
                          isSubscribedResource(p) ? t('订阅出口请通过订阅来源管理删除') : t('删除 IP ') + (p.label || p.name)
                        "
                        :disabled="busy || isSubscribedResource(p)"
                        @click="deleteIP(p)"
                      >
                        <Trash2 :size="16" />
                      </button>
                    </div>
                  </td>
                </tr>
                <tr v-if="!filteredIPs.length">
                  <td colspan="8" class="empty">{{ t("没有匹配的 IP 资源") }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="pool-cards">
            <article v-for="p in filteredIPs" :key="p.id" class="node-card">
              <div class="node-card-head"><input class="row-select" type="checkbox" v-model="selectedIPIDs" :value="p.id" :aria-label="t('选择')+' '+ p.name" :disabled="busy"/>
                <CountryMark :code="p.country_code" :country="p.country" />
                <div>
                  <h2>{{ p.label || p.name }}</h2>
                  <small>{{ p.name }}</small><small class="resource-origin">{{sourceLabel(p)}}</small><span v-if="p.source_stale" class="badge neutral">{{t('来源已移除')}}</span>
                </div>
                <span class="spacer"></span
                ><button
                  class="icon"
                  :title="t('编辑 IP ') + (p.label || p.name)"
                  @click="editIP(p)"
                >
                  <Pencil :size="17" />
                </button>
              </div>
              <dl class="node-details">
                <div>
                  <dt>{{ t("出口方式") }}</dt>
                  <dd>{{ exitName(p.exit) }}</dd>
                </div>
                <div>
                  <dt>{{ t("代理地址") }}</dt>
                  <dd class="mono">
                    {{
                      p.exit === "direct" ? t("VPS 本机") : p.host + ":" + p.port
                    }}
                  </dd>
                </div>
                <div>
                  <dt>{{ t("检测状态") }}</dt>
                  <dd>
                    <span :class="['badge', ipBadge(p)]">{{
                      ipStatus(p)
                    }}</span>
                  </dd>
                </div>
                <div>
                  <dt>{{ t("绑定节点") }}</dt>
                  <dd>{{ p.node_ids?.length || 0 }}{{ t("个") }}</dd>
                </div>
              </dl>
              <QualityTags :value="p.quality" :name="p.label || p.name" :running="qualityRunning === p.id" @run="qualityTest(p, true)" />
              <div class="card-speed">
                <span>{{ t("出口测速 · VPS") }}</span
                ><SpeedMetric
                  :result="p.speed"
                  :running="speedRunning === p.id"
                /><button
                  class="text-button"
                  :disabled="busy || !p.enabled"
                  @click="speedTest(p, true)"
                >
                  <Gauge :size="15" />{{ t("测速") }}</button>
              </div>
              <p v-if="p.probe_error" class="node-warning">
                {{ p.probe_error }}
              </p>
              <div class="node-card-footer">
                <span>{{
                  p.checked_at ? date(p.checked_at, true) : t("尚未检测")
                }}</span
                ><button
                  class="text-button"
                  :disabled="busy"
                  @click="detectIP(p)"
                >
                  <RefreshCw :size="14" />{{ t("检测") }}</button
                ><button
                  class="icon danger-button"
                  :title="t('删除 IP 资源')"
                  :disabled="busy || isSubscribedResource(p)"
                  @click="deleteIP(p)"
                >
                  <Trash2 :size="15" />
                </button>
              </div>
            </article>
            <p v-if="!filteredIPs.length" class="empty">{{ t("没有匹配的 IP 资源") }}</p>
          </div>
          <div class="table-caption">
            <span>{{ t("共") }}{{ filteredIPs.length }}{{ t("项资源") }}</span
            ><span>{{ t("测速：VPS 经所选出口 → Cloudflare · 每次最多 8 MiB") }}</span>
          </div>
          </template>
          </div>
        </section>
        <PublicPool v-else-if="page === 'public' && owner" :resources="publicResources" :active-tab="publicTab" @update:active-tab="selectPublicTab" @refresh="refresh()" @nodes="go('public-nodes')" />
        <section v-else-if="page === 'nodes' || page === 'public-nodes'">
          <div class="page-heading">
            <div>
              <div class="eyebrow">ROUTING</div>
              <h1>{{publicNodePage ? t("公共节点") : t("普通节点")}}</h1>
            </div>
            <div v-if="!publicNodePage" class="node-create-actions">
              <button @click="editNode(undefined, 'vless')">
                <Plus :size="17" />{{ t("新建 VLESS 节点") }}</button
              ><button class="primary" @click="editNode(undefined, 'hy2')">
                <Plus :size="17" />{{ t("新建 HY2 节点") }}</button>
            </div>
          </div>
          <div v-if="publicNodePage" class="pool-intro"><Globe2 :size="21"/><div><strong>{{ t("公共节点独立维护") }}</strong><p>{{ t("节点由公共池自动创建、拉黑与剔除。普通节点和普通订阅保留在各自页面。") }}</p></div><span class="spacer"/><button @click="go('public')">{{ t("采集与黑名单") }}</button><button class="primary" @click="go('public-subscription')">{{ t("公共订阅") }}</button></div>
          <div class="endpoint-band">
            <span class="endpoint-icon"><Globe2 :size="23" /></span>
            <div>
              <strong>{{ t("公网入口") }}</strong>
              <div class="endpoint-domains">
                <span>VLESS · {{ state.system.vless_host }}</span
                ><span>HY2 · {{ state.system.hy2_host }}</span>
              </div>
            </div>
            <div class="endpoint-ports">
              <span><i class="dot" />TCP 443</span
              ><span><i class="dot blue" />UDP 443</span>
            </div>
            <span class="badge neutral">Nginx SNI</span>
          </div>
          <div class="toolbar node-toolbar">
            <div class="search">
              <Search :size="17" /><input
                v-model="nodeSearch"
                :placeholder="t('搜索国家、IP 或出口地址')"
                :aria-label="t('搜索节点')"
              />
            </div>
            <select v-model="nodeProtocol" :aria-label="t('节点协议')">
              <option value="all">{{ t("全部协议") }}</option>
              <option value="vless">VLESS</option>
              <option value="hy2">Hysteria2</option></select
            ><select v-model="nodeStatus" :aria-label="t('节点状态')">
              <option value="all">{{ t("全部状态") }}</option>
              <option value="enabled">{{ t("已启用") }}</option>
              <option value="disabled">{{ t("已停用") }}</option></select
            ><span class="spacer"></span
            ><span class="muted">{{ filteredNodes.length }}{{ t("个节点") }}</span>
          </div>
          <div v-if="selectedNodeIDs.length" class="batch-toolbar" role="region" :aria-label="t('批量操作')">
            <strong>{{t('已选择')}} {{selectedNodeIDs.length}}</strong>
            <button :disabled="busy" @click="batchAction('nodes','enable')">{{t('批量启用')}}</button>
            <button :disabled="busy" @click="batchAction('nodes','disable')">{{t('批量停用')}}</button>
            <button  class="danger-button" :disabled="busy" @click="batchAction('nodes','delete')"><Trash2 :size="14"/>{{t('批量删除')}}</button>
            <button class="text-button" :disabled="busy" @click="selectedNodeIDs=[]">{{t('清空选择')}}</button>
          </div>
          <div class="table-scroll node-table">
            <table>
              <thead>
                <tr>
                  <th class="select-cell"><input type="checkbox" :aria-label="t('选择全部当前结果')" :disabled="busy || !selectableNodes.length" :checked="!!selectableNodes.length && selectedNodeIDs.length === selectableNodes.length" :indeterminate="!!selectedNodeIDs.length && selectedNodeIDs.length < selectableNodes.length" @change="selectAll('nodes',$event)"/></th>
                  <th>{{ t("节点名称") }}</th>
                  <th>{{ t("协议") }}</th>
                  <th>{{ t("节点出口") }}</th>
                  <th>{{ t("公网 IP") }}</th>
                  <th>{{ t("状态") }}</th>
                  <th>{{ t("出口测速 · VPS") }}</th>
                  <th class="right">{{ t("操作") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="n in filteredNodes" :key="n.id">
                  <td class="select-cell"><input type="checkbox" v-model="selectedNodeIDs" :value="n.id" :aria-label="t('选择')+' '+ n.name" :disabled="busy || n.default_direct"/></td>
                  <td>
                    <div class="user-cell">
                      <CountryMark
                        :code="n.country_code"
                        :country="n.country"
                      />
                      <div>
                        <strong>{{ n.name }}</strong
                        ><small
                          >{{
                            n.protocol === "vless"
                              ? state.system.vless_host
                              : state.system.hy2_host
                          }}:443</small
                        >
                      </div>
                    </div>
                  </td>
                  <td>
                    <span :class="['tag', n.protocol]">{{
                      n.protocol === "hy2" ? "Hysteria2" : "VLESS Reality"
                    }}</span>
                    <small v-if="n.protocol === 'vless' && n.reality_sni" class="node-sni-label">SNI · {{n.reality_sni}}</small><small v-if="n.dns?.mode === 'secure'" class="node-sni-label">{{t('出口 DNS 防护')}} · {{n.dns.ipv6 === 'block' ? 'IPv4' : 'IPv4 / IPv6'}}</small>
                    <span v-if="n.default_direct" class="badge neutral default-direct-badge"><ShieldCheck :size="11"/>{{t('默认直连')}}</span>
                  </td>
                  <td>
                    <strong>{{ nodePoolLabel(n) }}</strong>
                    <small class="table-detail"
                      >{{ exitName(n.exit) }} ·
                      {{
                        n.exit === "direct"
                          ? t("服务器直连")
                          : n.host + ":" + n.port
                      }}</small
                    >
                  </td>
                  <td><span class="mono">{{ n.probe_ip || t("待检测") }}</span><QualityTags :value="n.quality" :name="n.name" :running="qualityRunning === n.id" @run="qualityTest(n)" /></td>
                  <td>
                    <span
                      :class="['badge', n.enabled ? 'success' : 'neutral']"
                      >{{ n.enabled ? t("已启用") : t("已停用") }}</span
                    ><small
                      v-if="n.probe_error"
                      class="table-warning"
                      :title="n.probe_error"
                      >{{ t("检测需重试") }}</small
                    >
                  </td>
                  <td>
                    <SpeedMetric
                      :result="n.speed"
                      :running="speedRunning === n.id"
                    />
                  </td>
                  <td>
                    <div class="row-actions">
                      <button
                        class="row-action-button speed-action"
                        :disabled="busy || !n.enabled"
                        @click="speedTest(n)"
                      >
                        <Gauge :size="15" />{{ t("测速") }}</button>
                      <button
                        class="row-action-button"
                        :disabled="busy"
                        :title="t('重新检测 ') + n.name"
                        @click="detectNode(n)"
                      >
                        <RefreshCw
                          :size="15"
                          :class="{ spin: detecting === n.id }"
                        />{{ t("检测") }}</button
                      ><button class="row-action-button" @click="n.managed_by ? go('public') : editNode(n)">
                        <Pencil :size="15" />{{ n.managed_by ? t('自动管理') : n.default_direct ? t('默认配置') : t('编辑') }}
                      </button>
                    </div>
                  </td>
                </tr>
                <tr v-if="!filteredNodes.length">
                  <td colspan="8" class="empty">{{ t("没有匹配的节点") }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="node-cards mobile-node-cards">
            <article v-for="n in filteredNodes" :key="n.id" class="node-card">
              <div class="node-card-head"><input class="row-select" type="checkbox" v-model="selectedNodeIDs" :value="n.id" :aria-label="t('选择')+' '+ n.name" :disabled="busy || n.default_direct"/>
                <CountryMark :code="n.country_code" :country="n.country" />
                <div>
                  <h2>{{ n.name }}</h2>
                  <span :class="['tag', n.protocol]">{{
                    n.protocol === "vless"
                      ? "VLESS · Reality / TCP"
                      : "Hysteria2 · QUIC / UDP"
                  }}</span>
                  <span v-if="n.protocol === 'vless' && n.reality_sni" class="node-sni-label">SNI · {{n.reality_sni}}</span>
                  <span v-if="n.default_direct" class="badge neutral default-direct-badge"><ShieldCheck :size="11"/>{{t('默认直连')}}</span>
                </div>
                <span class="spacer"></span
                ><button class="icon" :title="n.managed_by ? t('自动节点策略') : n.default_direct ? t('默认配置') : t('编辑节点')" @click="n.managed_by ? go('public') : editNode(n)">
                  <Pencil :size="17" />
                </button>
              </div>
              <dl class="node-details">
                <div>
                  <dt>{{ t("状态") }}</dt>
                  <dd>
                    <span
                      :class="['badge', n.enabled ? 'success' : 'neutral']"
                      >{{ n.enabled ? t("已启用") : t("已停用") }}</span
                    >
                  </dd>
                </div>
                <div>
                  <dt>{{ t("当前出口") }}</dt>
                  <dd>{{ nodePoolLabel(n) }} · {{ exitName(n.exit) }}</dd>
                </div>
                <div>
                  <dt>{{ t("出口地址") }}</dt>
                  <dd>
                    {{
                      n.exit === "direct" ? t("VPS 本机") : n.host + ":" + n.port
                    }}
                  </dd>
                </div>
                <div>
                  <dt>{{ t("出口国家") }}</dt>
                  <dd>
                    {{ n.country || t("待识别")
                    }}<span v-if="n.country_code" class="country-code">{{
                      n.country_code
                    }}</span>
                  </dd>
                </div>
                <div>
                  <dt>{{ t("公网 IP") }}</dt>
                  <dd class="mono">{{ n.probe_ip || t("待检测") }}</dd>
                </div>
                <div>
                  <dt>{{ t("作用范围") }}</dt>
                  <dd>
                    {{ t("此逻辑节点") }}
                  </dd>
                </div>
              </dl>
              <QualityTags :value="n.quality" :name="n.name" :running="qualityRunning === n.id" @run="qualityTest(n)" />
              <div class="card-speed">
                <span>{{ t("出口测速 · VPS") }}</span
                ><SpeedMetric
                  :result="n.speed"
                  :running="speedRunning === n.id"
                /><button
                  class="text-button"
                  :disabled="busy || !n.enabled"
                  @click="speedTest(n)"
                >
                  <Gauge :size="15" />{{ t("测速") }}</button>
              </div>
              <p v-if="n.probe_error" class="node-warning">
                <CircleHelp :size="14" />{{ n.probe_error }}
              </p>
              <div class="node-card-footer">
                <span>{{
                  n.probed_at
                    ? date(n.probed_at, true) + t(" 已检测")
                    : t("尚无出口检测记录")
                }}</span
                ><button
                  class="icon"
                  :title="t('重新检测 ') + n.name"
                  :disabled="busy"
                  @click="detectNode(n)"
                >
                  <RefreshCw
                    :size="16"
                    :class="{ spin: detecting === n.id }"
                  /></button
                ><button class="text-button" @click="n.managed_by ? go('public') : editNode(n)">
                  {{ n.managed_by ? t('自动节点策略') : n.default_direct ? t('默认配置') : t('配置出口') }}<ArrowUpRight :size="15" />
                </button>
              </div>
            </article>
            <p v-if="!filteredNodes.length" class="empty">{{ t("没有匹配的节点") }}</p>
          </div>
          <div class="table-caption">
            <span>{{ t("共") }}{{ filteredNodes.length }}{{ t("个节点") }}</span
            ><span>{{ t("名称根据实际出口自动更新") }}</span>
          </div>
        </section>
        <section v-else-if="page === 'subscription' || page === 'public-subscription'">
          <div class="page-heading">
            <div>
              <div class="eyebrow">SUBSCRIPTION</div>
              <h1>
                {{ publicSubPage ? t("公共订阅") : t("普通订阅") }}
              </h1>
            </div>
            <select v-if="owner" v-model="subUser" :aria-label="t('订阅成员')">
              <option v-for="u in state.users" :key="u.id" :value="u.id">
                {{ u.username }}
              </option>
            </select>
          </div>
          <div class="pool-intro"><QrCode :size="21"/><div><strong>{{publicSubPage ? t('公共节点专用订阅') : t('普通节点订阅')}}</strong><p>{{publicSubPage ? t('独立订阅凭据，仅含公共节点；拉黑或剔除后自动更新。公共池为空时，Mihomo 配置使用 REJECT，不回退直连。') : simpleMode ? t('获取已授权的节点配置，将订阅导入客户端即可连接。') : t('仅含普通节点，公共节点使用独立公共订阅地址。')}}</p></div><span v-if="!simpleMode" class="spacer"/><button v-if="!simpleMode" @click="go(publicSubPage ? 'subscription' : 'public-subscription')">{{publicSubPage ? t('普通订阅') : t('公共订阅')}}</button></div>
          <p v-if="subError" class="error" role="alert">{{ t(subError) }} <button :disabled="subLoading" @click="loadSub(true)"><RefreshCw :size="14"/>{{t('重试')}}</button></p>
          <div v-if="subLoading && !sub" class="empty" role="status"><LoaderCircle :size="20" class="spin"/> {{t('正在读取订阅节点…')}}</div>
          <template v-if="sub"
            ><div class="sub-summary">
              <div class="user-cell">
                <span class="avatar">{{
                  sub.user.username.slice(0, 1).toUpperCase()
                }}</span>
                <div>
                  <strong>{{ sub.user.username }}</strong
                  ><small>{{
                    sub.user.role === "owner" ? t("管理员") : t("企业成员")
                  }}</small>
                </div>
              </div>
              <span :class="['badge', sub.active ? 'success' : 'danger']">{{
                userStatus(sub.user)
              }}</span
              ><span class="spacer"></span>
              <div>
                <small>{{ t("已用流量") }}</small
                ><strong
                  >{{ bytes(sub.user.upload + sub.user.download)
                  }}<em>
                    / {{ sub.user.quota ? bytes(sub.user.quota) : t("不限") }}</em
                  ></strong
                >
              </div>
              <div>
                <small>{{ t("到期时间") }}</small
                ><strong>{{ date(sub.user.expires) }}</strong>
              </div>
            </div>
            <div class="section-head">
              <h2>{{ t("订阅配置") }}</h2>
              <span class="muted">{{
                (state.runtime?.mode === 'no_logs' || state.runtime?.subscription_access_enabled === false)
                  ? (sub.user.last_sub ? t("最近记录 ") + date(sub.user.last_sub, true) + ' · ' : '') + t("访问时间已停止记录")
                  : sub.user.last_sub
                  ? t("最近访问 ") + date(sub.user.last_sub, true)
                  : t("尚未访问")
              }}</span>
            </div>
            <div class="subscription-layout">
              <div class="subscription-controls">
                <div class="segmented">
                  <button
                    v-for="f in [
                      { id: 'mihomo', name: 'Mihomo' },
                      { id: 'base64', name: 'Base64' },
                      { id: 'raw', name: t('原始链接') },
                    ]"
                    :key="f.id"
                    :class="{ selected: format === f.id }"
                    @click="format = f.id"
                  >
                    {{ f.name }}
                  </button>
                </div>
                <label
                  >{{ t("协议") }}<select v-model="subProtocol">
                    <option value="">{{ t("全部协议") }}</option>
                    <option value="vless">VLESS</option>
                    <option value="hy2">Hysteria2</option>
                  </select></label
                ><label
                  >{{ t("订阅地址") }}<div class="copy-field">
                    <input
                      :value="subURL"
                      readonly
                      :aria-label="t('订阅地址')"
                      @focus="($event.target as HTMLInputElement).select()"
                    /><button
                      class="icon"
                      :title="t('复制订阅地址')"
                      @click="copy(subURL)"
                    >
                      <Copy :size="17" />
                    </button></div
                ></label>
                <div class="button-row">
                  <button
                    class="primary"
                    :disabled="!sub.active || busy"
                    @click="downloadSub"
                  >
                    <Download :size="17" />{{ t("下载订阅") }}</button
                  ><button @click="copy(subURL)">
                    <Copy :size="17" />{{ t("复制地址") }}</button>
                </div>
              </div>
              <div class="qr-area">
                <img
                  v-if="qr"
                  :src="qr"
                  width="220"
                  height="220"
                  :alt="t('当前订阅地址二维码')"
                /><p v-else-if="qrError" class="error" role="alert">{{qrError}}</p><span
                  >{{
                    format === "mihomo"
                      ? "Mihomo"
                      : format === "base64"
                        ? "Base64"
                        : "URI"
                  }}{{ t("订阅") }}</span
                >
              </div>
            </div>
            <NodeSubscriptionCards :key="sub.user.id + ':' + sub.pool + ':' + subProtocol" :nodes="sub.nodes" :active="sub.active" :pool="sub.pool" :loading="subLoading" @refresh="loadSub()"/>
            <div v-if="owner" class="subscription-security">
              <h2>{{ t("凭据管理") }}</h2>
              <button @click="confirmUser(sub.user, publicSubPage ? 'rotate-public-sub' : 'rotate-sub')">
                <RefreshCw :size="16" />{{ t("重置订阅地址") }}</button
              ><button
                class="danger-button"
                @click="confirmUser(sub.user, 'revoke')"
              >
                <KeyRound :size="16" />{{ t("撤销全部旧配置") }}</button>
            </div></template
          >
        </section>
        <section v-else-if="page === 'system'">
          <div class="page-heading">
            <div>
              <div class="eyebrow">SYSTEM</div>
              <h1>{{ t("运维状态") }}</h1>
            </div>
            <button @click="backup"><Download :size="17" />{{ t("下载备份") }}</button>
          </div>
          <div class="metrics system-metrics">
            <div>
              <span>{{ t("控制程序内存堆") }}</span
              ><strong>{{ bytes(state.system.heap) }}</strong
              ><small>Go HeapAlloc</small>
            </div>
            <div>
              <span>{{ t("控制程序运行时间") }}</span
              ><strong>{{ duration(state.system.uptime) }}</strong
              ><small>{{ state.system.version }}</small>
            </div>
            <div>
              <span>{{ t("证书有效期") }}</span
              ><strong
                >{{
                  Math.max(
                    0,
                    Math.ceil(
                      (state.system.cert_expires * 1000 - Date.now()) /
                        86400000,
                    ),
                  )
                }}<em>{{ t("天") }}</em></strong
              ><small>{{ date(state.system.cert_expires) }}</small>
            </div>
          </div>
          <div class="section-head">
            <h2>{{ t("运行服务") }}</h2>
            <span
              :class="[
                'badge',
                state.system.status === 'applied' ? 'success' : 'danger',
              ]"
              >{{
                state.system.status === "applied"
                  ? t("已应用")
                  : state.system.status === "pending"
                    ? t("待应用")
                    : t("异常")
              }}</span
            >
          </div>
          <div class="service-list">
            <div>
              <span class="service-symbol"><Settings2 :size="18" /></span
              ><strong>{{ t("广月企业控制台") }}</strong
              ><span class="muted">{{ t("成员与订阅管理") }}</span
              ><span class="badge success">{{ t("运行中") }}</span>
            </div>
            <div v-for="s in state.system.services" :key="s.name">
              <span class="service-symbol"><Server :size="18" /></span
              ><strong>{{ s.name }}</strong
              ><span class="muted">{{
                s.name === "Xray"
                  ? "VLESS Reality · TCP 443"
                  : s.name === "Hysteria2"
                    ? "HY2 · UDP 443"
                    : "HTTPS / HTTP/2 · SNI"
              }}</span
              ><span :class="['badge', s.active ? 'success' : 'danger']">{{
                s.active ? t("运行中") : t("未运行")
              }}</span>
            </div>
          </div>
          <p v-if="state.system.traffic_error" class="error">
            {{ state.system.traffic_error }}
          </p>
          <div class="section-head">
            <h2>{{ t("最近操作") }}</h2>
            <span class="muted">{{ t("最近 30 条") }}</span>
          </div>
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>{{ t("时间") }}</th>
                  <th>{{ t("操作") }}</th>
                  <th>{{ t("对象") }}</th>
                  <th>{{ t("操作人") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(a, i) in state.audit" :key="i">
                  <td class="muted">{{ date(a.at, true) }}</td>
                  <td>{{ actionName(a.action) }}</td>
                  <td>{{ a.target }}</td>
                  <td>{{ a.actor }}</td>
                </tr>
                <tr v-if="!state.audit.length">
                  <td colspan="4" class="empty">{{ t("暂无记录") }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <footer class="page-footer">
          <span>{{site.panel_name}} · {{site.organization}}</span
          ><span>v{{ state.system.version.split("-")[0] }}</span>
        </footer>
      </div>
    </div>
  </div>
  <div v-if="modal" class="modal-shade" @click.self="!busy && (modal = '')">
    <section
      class="modal"
      role="dialog"
      aria-modal="true"
      :aria-label="
        modal === 'user'
          ? t('成员设置')
          : modal === 'ip'
            ? t('IP 资源设置')
            : modal === 'node'
              ? t('节点设置')
              : modal === 'import'
                ? t('导入订阅或节点')
                : t('修改密码')
      "
    >
      <div class="modal-head">
        <h2>
          {{
            modal === "user"
              ? editingID
                ? t("编辑成员")
                : t("添加成员")
              : modal === "ip"
                ? ipForm.id
                  ? t("编辑 IP 资源")
                  : t("添加 IP 资源")
                : modal === "node"
                  ? nodeForm.id
                    ? t("编辑节点")
                    : t("新建 ") + nodeForm.protocol.toUpperCase() + t(" 节点")
                  : modal === "import"
                    ? t("导入订阅或节点")
                    : t("修改密码")
          }}
        </h2>
        <button class="icon" :title="t('关闭')" :disabled="busy" @click="modal = ''">
          <X :size="20" />
        </button>
      </div>
      <form v-if="modal === 'import'" @submit.prevent="importSubscription">
        <div class="import-intro">
          <div class="import-symbol"><Download :size="24" /></div>
          <div>
            <strong>{{ t("将订阅或单个节点加入出口池") }}</strong>
            <p>{{ t("导入后，可供 VLESS 与 HY2 节点选择使用。") }}</p>
          </div>
        </div>
        <div class="import-tabs">
          <button
            type="button"
            :class="{ active: importMode === 'url' }"
            :disabled="busy || importFileReading"
            @click="importMode = 'url'"
          >{{ t("订阅 / 单节点") }}</button
          ><button
            type="button"
            :class="{ active: importMode === 'content' }"
            :disabled="busy || importFileReading"
            @click="importMode = 'content'"
          >{{ t("粘贴内容") }}</button><button type="button" :class="{ active: importMode === 'file' }" :disabled="busy || importFileReading" @click="importMode = 'file'"><Download :size="14"/>{{t("导入文件")}}</button>
        </div>
        <label v-if="importMode === 'url'"
          >{{ t("订阅或单节点链接") }}<input
            v-model="importForm.url"
            type="password"
            autocomplete="off"
            required
            :placeholder="t('https://… 或 vless://…')"
            :aria-label="t('订阅或单节点链接')"
          /><span class="field-caption"
            >{{ t("自动识别 HTTPS 订阅与 Shadowrocket / 普通节点链接。") }}</span
          ></label
        >
        <label v-else-if="importMode === 'content'"
          >{{ t("节点或订阅内容") }}<textarea
            v-model="importForm.content"
            rows="7"
            required
            maxlength="4194304"
            :placeholder="t('每行一个节点链接或 IP:端口:账号:密码，也支持 Clash / Mihomo 和 Base64')"
            :aria-label="t('订阅内容')"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
          ></textarea>
        </label>
        <div v-else class="private-file-import" :aria-busy="importFileReading">
          <div class="private-file-heading"><Download :size="23"/><div><strong>{{t('导入代理配置文件')}}</strong><p>{{t('支持 Webshare TXT、普通节点列表、Clash / Mihomo 和 Base64。')}}</p></div></div>
          <div v-if="importFile" class="private-file-summary" role="status"><Check :size="17"/><div><strong>{{importFile.name}}</strong><span>{{bytes(importFile.size)}} · {{importFile.lines}} {{t('个非空配置行')}}</span></div><button type="button" class="icon" :disabled="busy || importFileReading" :aria-label="t('移除所选文件')" @click="clearPrivateFile"><X :size="16"/></button></div>
          <label class="import-file-button private-file-picker" :class="{ disabled: busy || importFileReading }"><LoaderCircle v-if="importFileReading" :size="16" class="spin"/><Download v-else :size="16"/>{{importFileReading ? t('正在读取文件…') : importFile ? t('重新选择文件') : t('选择文件')}}<input type="file" :disabled="busy || importFileReading" accept=".txt,.yaml,.yml,.json,.conf,text/plain,application/json" :aria-label="t('选择代理配置文件')" @change="readPrivateFile"/></label>
          <p class="field-caption">{{t('最大 4 MiB；文件内容与账号密码不在预览中显示。')}}</p>
        </div>
        <div v-if="importMode !== 'url'" class="private-plain-protocol">
          <label>{{t('无协议前缀的出口类型')}}<select v-model="importForm.plain_protocol" :disabled="busy || importFileReading"><option value="http">{{t('HTTP（默认）')}}</option><option value="socks5">SOCKS5</option></select></label>
          <div><strong>Webshare · IP:{{t('端口')}}:{{t('账号')}}:{{t('密码')}}</strong><p>{{t('仅用于 IP:端口 或 IP:端口:账号:密码 格式；已有协议的节点链接保持原协议。')}}</p><span>{{t('文件和粘贴内容一次性加入独立出口，不创建订阅来源。')}}</span></div>
        </div>
        <div class="import-formats">
          <span
            class="badge neutral"
            v-for="p in [
              'VLESS',
              'VMess',
              'Trojan',
              'Shadowsocks',
              'Hysteria2',
              'HTTP',
              'SOCKS5',
            ]"
            :key="p"
            >{{ p }}</span
          >
        </div>
        <p class="field-caption">{{ t("HTTPS 订阅保存为来源并每日更新；单节点、粘贴内容和文件归入独立出口。一次最多 256 项，重复配置自动跳过。") }}</p>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div v-if="importIssues.length" class="private-import-issues" role="status"><strong>{{t('错误配置')}} · {{importIssues.length}}</strong><p>{{t('未导入任何出口，请修改标记的行后重新选择文件或粘贴。')}}</p><details><summary>{{t('查看错误行')}}</summary><p v-for="issue in importIssues" :key="issue.index">{{t('行 / 项')}} {{issue.index}} · {{t(issue.reason)}}</p></details></div>
        <div class="modal-footer">
          <button type="button" :disabled="busy" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy || importFileReading || (importMode === 'file' && !importFile)">
            <LoaderCircle v-if="busy" class="spin" :size="16" /><Download
              v-else
              :size="16"
            />{{ busy ? t("正在下载并校验…") : t("导入到 IP 池") }}
          </button>
        </div>
      </form>
      <form v-else-if="modal === 'user'" @submit.prevent="saveUser">
        <label
          >{{ t("账号") }}<input
            v-model="userForm.username"
            required
            pattern="[a-zA-Z0-9][a-zA-Z0-9_.\-]{2,31}"
            maxlength="32"
            autocomplete="off" /></label
        ><label
          >{{ editingID ? t("新密码（留空保持不变）") : t("登录密码")
          }}<input
            v-model="userForm.password"
            type="password"
            autocomplete="new-password"
            :required="!editingID"
            minlength="12"
            maxlength="72"
        /></label>
        <div class="field-row">
          <label
            >{{ t("流量额度 / GB") }}<input
              v-model.number="userForm.quotaGB"
              type="number"
              min="0"
              step="0.01"
              required
            /><small>{{ t("0 为不限") }}</small></label
          ><label
            >{{ t("到期日期") }}<input v-model="userForm.expiresDate" type="date" /><small
              >{{ t("留空为不限") }}</small
            ></label
          >
        </div>
        <div class="check-row">
          <label
            ><input
              v-model="userForm.enabled"
              type="checkbox"
              :disabled="
                state?.users.find((u) => u.id === editingID)?.role === 'owner'
              "
            />{{ t("账号启用") }}</label
          ><label><input v-model="userForm.vless" type="checkbox" />VLESS</label
          ><label><input v-model="userForm.hy2" type="checkbox" />HY2</label>
        </div>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div v-if="editingID" class="advanced-actions">
          <button
            type="button"
            @click="
              confirmUser(
                state!.users.find((u) => u.id === editingID)!,
                'reset-traffic',
              )
            "
          >
            <RefreshCw :size="15" />{{ t("重置流量") }}</button
          ><button
            v-if="
              state?.users.find((u) => u.id === editingID)?.role !== 'owner'
            "
            type="button"
            class="danger-button"
            @click="
              confirmUser(
                state!.users.find((u) => u.id === editingID)!,
                'delete',
              )
            "
          >
            <Trash2 :size="15" />{{ t("删除") }}</button>
        </div>
        <div class="modal-footer">
          <button type="button" :disabled="busy" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy">
            <LoaderCircle v-if="busy" class="spin" :size="16" /><Check
              v-else
              :size="16"
            />{{ t("保存成员") }}</button>
        </div>
      </form>
      <form v-else-if="modal === 'ip'" @submit.prevent="saveIP">
        <label
          >{{ t("备注名称") }}<input
            v-model="ipForm.label"
            maxlength="64"
            :placeholder="t('如：新加坡主出口 / 备用线路')"
        /></label>
        <div class="field-row">
          <label
            >{{ t("出口类型") }}<select
              v-model="ipForm.exit"
              :disabled="ipForm.exit === 'subscription'"
            >
              <option
                v-if="ipForm.exit === 'subscription'"
                value="subscription"
              >{{ t("机场节点 ·") }}{{ ipForm.upstream_type?.toUpperCase() }}
              </option>
              <option value="http">HTTP CONNECT</option>
              <option value="socks5">SOCKS5</option>
            </select></label
          ><label
            >{{ t("资源状态") }}<select
              v-model="ipForm.enabled"
              :disabled="!!ipForm.node_ids?.length"
            >
              <option :value="true">{{ t("启用，可供节点选择") }}</option>
              <option :value="false">{{ t("停用") }}</option>
            </select></label
          >
        </div>
        <template
          v-if="ipForm.exit !== 'direct' && ipForm.exit !== 'subscription'"
          ><div class="field-row host-port">
            <label
              >{{ t("代理服务器") }}<input
                v-model="ipForm.host"
                required
                :placeholder="t('IP 或域名')"
                maxlength="253" /></label
            ><label
              >{{ t("端口") }}<input
                v-model.number="ipForm.port"
                type="number"
                min="1"
                max="65535"
                required
            /></label>
          </div>
          <div class="field-row">
            <label
              >{{ t("认证用户名") }}<input
                v-model="ipForm.username"
                maxlength="128"
                autocomplete="off" /></label
            ><label
              >{{ t("认证密码") }}<input
                v-model="ipForm.password"
                type="password"
                maxlength="256"
                autocomplete="new-password"
                :placeholder="
                  ipForm.has_password ? t('已保存，留空保持不变') : t('无认证可留空')
                "
            /></label></div
        ></template>
        <p v-if="ipForm.exit === 'subscription'" class="field-caption">
          {{ ipForm.host }}:{{ ipForm.port }}{{ t("· 协议参数来自订阅，凭据已加密保存。") }}</p>
        <label
          >{{ t("说明") }}<input
            v-model="ipForm.notes"
            maxlength="240"
            :placeholder="t('供应商、用途或续费备注（可选）')"
        /></label>
        <div v-if="ipForm.node_ids?.length" class="decision-note">
          <CircleHelp :size="16" />
          <div>{{ t("此资源正在被") }}{{ ipForm.node_ids.length }}{{ t("个节点使用。更改出口配置将同步应用到这些节点，并使对应协议的连接重连。") }}<div class="bound-node-list">
              <span
                v-for="n in state?.nodes.filter((n) => n.exit_id === ipForm.id)"
                :key="n.id"
                class="tag"
                :class="n.protocol"
                >{{ n.protocol.toUpperCase() }} · {{ n.name }}</span
              >
            </div>
          </div>
        </div>
        <p v-else class="field-caption">{{ t("保存后自动检测实际公网 IP 和国家。未绑定资源检测失败时仍会入池，并标记为不可用。") }}</p>
        <p v-if="ipForm.exit === 'http'" class="field-caption">{{ t("HTTP CONNECT 出口不支持任意 UDP 流量。") }}</p>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div class="modal-footer">
          <button type="button" :disabled="busy" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy">
            <LoaderCircle v-if="busy" class="spin" :size="16" /><Check
              v-else
              :size="16"
            />{{ ipForm.enabled ? t("检测并保存") : t("保存资源") }}
          </button>
        </div>
      </form>
      <form v-else-if="modal === 'node'" @submit.prevent="saveNode">
        <div class="auto-node-name">
          <span class="field-caption"
            >{{ t("节点名称") }}<span class="badge neutral">{{ t("自动") }}</span></span
          ><strong>{{ displayedNodeName }}</strong>
        </div>
        <div class="check-row">
          <label v-if="!editingDefaultDirect"
            ><input v-model="nodeForm.enabled" type="checkbox" />{{ t("节点启用") }}</label
          ><span v-else class="badge neutral"><ShieldCheck :size="12"/>{{t('默认直连')}}</span><span class="tag" :class="nodeForm.protocol">{{
            nodeForm.protocol.toUpperCase()
          }}</span>
        </div>
        <div v-if="editingDefaultDirect" class="default-direct-notice"><ShieldCheck :size="18"/><div><strong>{{t('保留服务器默认直连入口')}}</strong><p>{{t('此节点固定使用本机出口，保持启用，不能删除或绑定其他出口。')}}</p><button type="button" class="text-button" :disabled="busy" @click="editNode(undefined, nodeForm.protocol)"><Plus :size="14"/>{{t('绑定 IP 池出口请新增节点')}}</button></div></div>
        <div v-if="nodeForm.protocol === 'vless'" class="node-sni-setting">
          <label for="node-reality-sni">{{t('Reality SNI（可选）')}}<input id="node-reality-sni" v-model="nodeForm.reality_sni" type="text" inputmode="url" autocomplete="off" autocapitalize="none" :spellcheck="false" maxlength="63" :disabled="busy" :placeholder="defaultRealitySNI" aria-describedby="node-reality-sni-help"/></label>
          <p id="node-reality-sni-help">{{t('仅填写域名，留空继承默认值')}} <strong>{{defaultRealitySNI}}</strong><br/>{{t('保存时校验公网 TLS 伪装目标；修改后请更新客户端订阅。')}}</p>
        </div>
        <label v-if="!editingDefaultDirect"
          >{{ t("节点出口") }}<select v-model="nodeForm.exit_id" :aria-label="t('节点出口')">
            <option value="">{{ t("本机直连（默认）") }}</option>
            <option
              v-for="p in ipPool"
              :key="p.id"
              :value="p.id"
              :disabled="!p.enabled"
            >
              {{ p.label ? p.label + " · " : "" }}{{ p.name }} ·
              {{ exitName(p.exit) }}{{ !p.enabled ? t("（已停用）") : "" }}
            </option>
          </select></label
        >
        <section v-if="nodeForm.dns" class="node-dns-settings">
          <div class="dns-setting-title"><ShieldCheck :size="17"/><strong>{{t('出口 DNS 防护')}}</strong><span class="badge neutral">IPv4 / IPv6</span></div>
          <label>{{t('DNS 模式')}}<select v-model="nodeForm.dns.mode" :disabled="busy" @change="nodeForm.dns.mode === 'secure' && Object.assign(nodeForm.dns,{doh:nodeForm.dns.doh || 'https://1.1.1.1/dns-query',ipv6:nodeForm.dns.ipv6 || 'block'})"><option value="secure">{{t('经出口加密解析（推荐）')}}</option><option value="system">{{t('常规解析（兼容模式）')}}</option></select></label>
          <template v-if="nodeForm.dns.mode === 'secure'">
            <label>{{t('DNS over HTTPS 地址')}}<input v-model="nodeForm.dns.doh" placeholder="https://1.1.1.1/dns-query" :disabled="busy" autocomplete="off"/></label>
            <div class="dns-presets"><button type="button" :disabled="busy" @click="nodeForm.dns.doh='https://1.1.1.1/dns-query'">Cloudflare</button><button type="button" :disabled="busy" @click="nodeForm.dns.doh='https://8.8.8.8/dns-query'">Google</button></div>
            <label>{{t('IPv6 策略')}}<select v-model="nodeForm.dns.ipv6" :disabled="busy"><option value="block">{{t('阻止 IPv6（推荐）')}}</option><option value="allow">{{t('允许 IPv6（经出口）')}}</option></select></label>
            <p>{{nodeForm.dns.ipv6 === 'block' ? t('阻止 IPv6：不返回 AAAA，拒绝 IPv6 目标') : t('允许 IPv6：A / AAAA 均经所选出口解析')}}</p>
            <p>{{t('域名解析和经过节点的 TCP / UDP 53 查询统一经该出口访问指定 DoH；失败即拒绝，不回退本机 DNS。')}}</p>
            <p>{{t('请输入使用公网 IP 的 HTTPS 地址；需证书有效。IPv6 出口不可用时不会改走 VPS 直连。')}}</p>
          </template>
          <p v-else>{{t('沿用核心与出口的解析方式，不强制 DNS 防护。')}}</p>
          <small>{{t('DNS 检测可能显示递归 DNS 服务商的地址；客户端绕过代理的 DNS 需在客户端开启 TUN / DNS 接管。')}}</small>
        </section>
        <div v-if="selectedIP && !editingDefaultDirect" class="pool-option-preview">
          <div class="user-cell">
            <CountryMark
              :code="selectedIP.country_code"
              :country="selectedIP.country"
            />
            <div>
              <strong>{{ selectedIP.name }}</strong
              ><small>{{ selectedIP.label || t("共享出口资源") }}</small>
            </div>
            <span class="spacer"></span
            ><span :class="['badge', ipBadge(selectedIP)]">{{
              ipStatus(selectedIP)
            }}</span>
          </div>
          <dl class="node-details">
            <div>
              <dt>{{ t("出口方式") }}</dt>
              <dd>{{ exitName(selectedIP.exit) }}</dd>
            </div>
            <div>
              <dt>{{ t("代理地址") }}</dt>
              <dd class="mono">
                {{
                  selectedIP.exit === "direct"
                    ? t("VPS 本机")
                    : selectedIP.host + ":" + selectedIP.port
                }}
              </dd>
            </div>
            <div>
              <dt>{{ t("已绑定节点") }}</dt>
              <dd>{{ selectedIP.node_ids?.length || 0 }}{{ t("个") }}</dd>
            </div>
          </dl>
        </div>
        <div v-else-if="editingDefaultDirect || !nodeForm.exit_id" class="pool-option-preview">
          <div class="user-cell">
            <Globe2 :size="24" />
            <div>
              <strong>{{ t("本机直连") }}</strong
              ><small>{{ t("直接使用此 VPS 的公网 IP，不占用 IP 池资源") }}</small>
            </div>
            <span class="spacer"></span
            ><span class="badge neutral">{{ t("默认出口") }}</span>
          </div>
        </div>
        <div v-if="!editingDefaultDirect" class="pool-select-footer">
          <span>{{ t("需要添加或修改出口") }}</span
          ><button
            type="button"
            class="text-button"
            @click="
              modal = '';
              go('ips');
            "
          >{{ t("前往 IP 管理") }}<ArrowUpRight :size="15" />
          </button>
        </div>
        <p v-if="!editingDefaultDirect || nodeForm.protocol === 'vless'" class="decision-note">
          <CircleHelp :size="16" />{{
            nodeForm.protocol === "hy2"
              ? t("此节点独立选择出口；应用配置会使 HY2 连接短暂重连。")
              : t("应用节点配置会使使用 VLESS 的成员重连。")
          }}{{
            selectedIP?.exit === "http" ? t(" HTTP 出口不支持任意 UDP。") : ""
          }}
        </p>
        <div class="probe-line">
          <button
            type="button"
            :disabled="busy || (!editingDefaultDirect && !!nodeForm.exit_id && !selectedIP?.enabled)"
            @click="probe"
          >
            <Activity :size="16" />{{ t("检测出口") }}</button
          ><span v-if="probeResult" class="probe-success"
            ><Check :size="16" />{{ probeResult }}</span
          >
        </div>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div class="modal-footer">
          <button
            v-if="
              nodeForm.id && !editingDefaultDirect &&
              (state?.nodes.filter((n) => n.protocol === nodeForm.protocol)
                .length || 0) > 1
            "
            type="button"
            class="icon danger-button"
            :title="t('删除节点')"
            @click="deleteNode(nodeForm)"
          >
            <Trash2 :size="17" /></button
          ><span class="spacer"></span
          ><button type="button" :disabled="busy" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy">
            <LoaderCircle v-if="busy" class="spin" :size="16" /><Check
              v-else
              :size="16"
            />{{ t("检测并应用") }}</button>
        </div>
      </form>
      <form v-else @submit.prevent="changePassword">
        <label
          >{{ t("当前密码") }}<input
            v-model="passwordForm.current"
            type="password"
            autocomplete="current-password"
            required /></label
        ><label
          >{{ t("新密码") }}<input
            v-model="passwordForm.password"
            type="password"
            autocomplete="new-password"
            minlength="12"
            maxlength="72"
            required /></label
        ><label
          >{{ t("确认新密码") }}<input
            v-model="passwordForm.confirm"
            type="password"
            autocomplete="new-password"
            minlength="12"
            maxlength="72"
            required
        /></label>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div class="modal-footer">
          <button type="button" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy">
            <KeyRound :size="16" />{{ t("修改密码") }}</button>
        </div>
      </form>
    </section>
  </div>
  <div v-if="confirmation" class="modal-shade confirm-shade">
    <section class="modal confirmation" role="alertdialog" aria-modal="true">
      <h2>{{ confirmation.title }}</h2>
      <p>{{ confirmation.detail }}</p>
      <p v-if="error" class="error">{{ t(error) }}</p>
      <div class="modal-footer">
        <button :disabled="busy" @click="confirmation = null">{{ t("取消") }}</button
        ><button class="primary" :disabled="busy" @click="confirmed">
          <LoaderCircle v-if="busy" class="spin" :size="16" />{{ t("确认") }}</button>
      </div>
    </section>
  </div>
  <div v-if="notice" class="toast" role="status">
    <Check :size="17" />{{ t(notice) }}
  </div>
</template>

<style>
.private-file-import{border:1px dashed var(--border);background:var(--surface);border-radius:10px;padding:19px;margin-bottom:18px}.private-file-heading{display:flex;gap:12px;align-items:flex-start}.private-file-heading>svg{color:var(--accent-text);flex-shrink:0;margin-top:2px}.private-file-heading strong{font-size:13px;font-weight:600}.private-file-heading p{font-size:11px;line-height:1.75;color:var(--muted);margin:6px 0 0}.private-file-summary{display:flex;gap:9px;align-items:center;margin:16px 0 3px;padding:11px 12px;background:var(--bg);border:1px solid var(--border);border-radius:7px}.private-file-summary>svg{color:var(--accent-text);flex-shrink:0}.private-file-summary>div{flex:1;min-width:0}.private-file-summary strong{display:block;font-size:12px;overflow-wrap:anywhere;font-weight:500}.private-file-summary span{display:block;font-size:10px;color:var(--muted);margin-top:5px}.private-file-picker{margin-top:15px!important;border:1px solid var(--border);border-radius:7px;background:var(--bg);color:var(--text)!important;min-height:35px}.private-file-picker.disabled{opacity:.6;cursor:wait}.private-file-import>.field-caption{margin-bottom:0}.private-plain-protocol{display:grid;grid-template-columns:minmax(125px,155px) 1fr;gap:17px;align-items:start;padding:15px;border:1px solid var(--border);border-radius:9px;margin:16px 0}.private-plain-protocol>label{margin:0;font-size:11px}.private-plain-protocol select{font-size:12px;margin-top:8px}.private-plain-protocol>div{padding-top:2px;min-width:0}.private-plain-protocol strong{font-size:11px;font-weight:500;color:var(--text);overflow-wrap:anywhere}.private-plain-protocol p,.private-plain-protocol span{font-size:10px;line-height:1.8;color:var(--muted);margin:6px 0 0}.private-plain-protocol span{display:block;color:var(--accent-text)}.private-import-issues{margin:14px 0;padding:13px 15px;border:1px solid var(--border);border-radius:8px;background:var(--bg);font-size:11px}.private-import-issues strong{color:var(--text)}.private-import-issues p{font-size:11px;line-height:1.7;color:var(--muted);margin:7px 0}.private-import-issues details{max-height:165px;overflow:auto}.private-import-issues summary{cursor:pointer;color:var(--accent-text)}@media(max-width:550px){.private-plain-protocol{grid-template-columns:1fr;gap:12px}.private-file-import{padding:15px}.private-plain-protocol select{width:100%}.private-file-picker{width:100%;box-sizing:border-box}}
.default-direct-badge{display:inline-flex;align-items:center;gap:5px;font-size:10px;margin-top:7px}.default-direct-notice{display:flex;align-items:flex-start;gap:11px;border:1px solid var(--border);border-radius:9px;padding:15px;background:var(--accent-soft);margin:16px 0}.default-direct-notice>svg{color:var(--accent-text);flex-shrink:0}.default-direct-notice>div{min-width:0}.default-direct-notice strong{font-size:12px;font-weight:600}.default-direct-notice p{font-size:11px;line-height:1.7;margin:7px 0;color:var(--muted)}.default-direct-notice button{font-size:11px;padding:4px 0;min-height:28px;white-space:normal;text-align:left}
.node-sni-setting{padding:15px;border:1px solid var(--border);border-radius:9px;background:var(--surface-raised);margin:16px 0}.node-sni-setting label{margin:0!important}.node-sni-setting p{font-size:11px;line-height:1.7;color:var(--muted);margin:9px 0 0;overflow-wrap:anywhere}.node-sni-setting strong{font-weight:500;color:var(--secondary)}.node-sni-label{display:block;font-size:10px;line-height:1.6;color:var(--accent-text);margin-top:7px;max-width:220px;overflow-wrap:anywhere;white-space:normal}
.resource-membership-state{display:flex;flex-direction:column;align-items:center;gap:14px;padding:42px 24px;background:var(--surface);border:1px dashed var(--border);border-radius:10px;color:var(--muted);text-align:center}.resource-membership-state h2{font-size:15px;color:var(--text);margin:0}.resource-membership-state p{font-size:12px;line-height:1.8;margin:0;max-width:570px;overflow-wrap:anywhere}.resource-membership-state button{font-size:11px}.resource-membership-notice{display:flex;align-items:center;justify-content:space-between;gap:12px;border:1px solid var(--border);border-left:3px solid #d8a454;border-radius:7px;background:var(--surface);padding:12px 15px;margin-bottom:18px;font-size:11px;color:var(--muted)}
.resource-page-description{font-size:12px;line-height:1.7;color:var(--muted);margin:9px 0 0}.resource-section-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;margin:0 0 20px}.resource-section-heading h2{font-size:15px;margin:0}.resource-section-heading p{font-size:12px;color:var(--muted);margin:7px 0 0;line-height:1.7}.resource-section-heading>button{flex-shrink:0}.resource-source-filter{display:flex;align-items:center;gap:10px;margin:0 0 16px;padding:12px 15px;border:1px solid var(--border);background:var(--surface);border-radius:8px;font-size:12px}.resource-source-filter>button{margin-left:auto}.resource-origin{color:var(--accent-text)!important;font-size:10px!important;line-height:1.6}.private-pool-page .pool-table td:nth-child(2){min-width:245px;max-width:310px}.private-pool-page .user-stat-grid{margin-bottom:26px}.import-file-button{display:inline-flex!important;align-items:center;justify-content:center;gap:6px;font-size:11px!important;cursor:pointer;margin:0!important;padding:9px 12px;position:relative;color:var(--muted)!important}.import-file-button:focus-within{outline:2px solid var(--accent-text);outline-offset:-2px}.import-file-button input{position:absolute;inset:0;opacity:0;cursor:pointer;width:100%;padding:0!important;min-height:0!important}.import-tabs{flex-wrap:wrap}@media(max-width:650px){.private-pool-page>.page-heading{flex-direction:column;align-items:stretch}.private-pool-page .heading-actions{justify-content:flex-start;flex-wrap:wrap}.resource-section-heading{align-items:flex-start;gap:10px}.resource-section-heading p{font-size:11px}.resource-section-heading>button{font-size:10px}.resource-source-filter{flex-wrap:wrap}.private-pool-page .resource-tabs>button{flex:1}}
</style>
