import { download } from "../lib/download";
import { useAccessStore } from "../stores/access";
import { useRouter, useRoute } from "vue-router";
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { Mail, Globe2, LayoutDashboard, Database, QrCode, RadioTower, Server, Settings2, Users } from "lucide-vue-next";
import { bytes, date, duration, exitName } from "../lib/format";
import { useSubscription } from "./useSubscription";
import { useApi, ApiError, downloadBlob, invalidateSession, onSessionExpired, isCancelled, setRemoteSite, getRemoteSite, requestGeneration } from "../lib/api";
import { runQueued } from "../lib/tasks";
import { latestRequest, serialPoll } from "../lib/requests";
import { storeToRefs } from "pinia";
import { usePreferencesStore } from "../stores/preferences";
import { t, locale, applyDefaultLocale } from "../i18n";
import { type SiteSettings } from "../PanelSettings.vue";
import type { IPQuality } from "../quality";
import type { User, Node, IPResource, State, SpeedResult } from "../types";
import "../view-mode.css";

export function usePanel() {
const router=useRouter(),route=useRoute();
const api = useApi();
const selectedSite=ref(""),selectedSiteName=ref("");
async function switchSite(id:string,name="") {
 clearSession();ready.value=false;busy.value=false;setRemoteSite(id);selectedSite.value=id;selectedSiteName.value=name;
 await refresh(true);
 if(!state.value&&id) {setRemoteSite("");selectedSite.value="";selectedSiteName.value="";await refresh(true);error.value=t("站点连接失败，已返回本站");}
 go("overview");
}
const stateRequest = latestRequest();
const state = ref<State | null>(null),
  ready = ref(false),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  page = ref("overview"),
  mobileNav = ref(false);
const access=useAccessStore();
watch(state,v=>{access.role=v?.me.role||null;access.edition=v?.system.edition||'lite';},{flush:'sync'});
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
 const generation=requestGeneration();
  await task(async () => {
    qualityRunning.value = n.id;
    try { const result: IPQuality = await runQueued<IPQuality>('quality',(pool ? 'ips/' : 'nodes/') + n.id); toast(result.error || t('质量检测完成，标签已更新')); }
    finally { if(generation===requestGeneration()){qualityRunning.value = ''; await refresh();} }
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
 const generation=requestGeneration();
  await task(async () => {
    speedRunning.value = n.id;
    try {
      const result: SpeedResult = await runQueued<SpeedResult>("speed",(pool ? "ips/" : "nodes/") + n.id);
      toast(
        result.error || t("出口测速完成 · ") + result.mbps.toFixed(1) + " Mbps",
      );
    } finally {
      if(generation===requestGeneration()){speedRunning.value = "";
      await refresh();}
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
  void router.replace('/public' + (value === 'resources' ? '' : '/' + value));
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
  void router.replace('/ips/' + value);
}
function viewSourceResources(ids: string[], name: string) {
  selectPrivateTab('subscriptions');
  sourceFilter.value = { ids, name };
  nextTick(() => window.scrollTo({ top: 0, behavior: 'instant' }));
}
async function sourceChanged() { await refresh(true); await loadPrivateSources(); }
watch(page, value => { if (state.value) void refresh(true); if (value === 'ips') loadPrivateSources(); });
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
const probeResult = ref("");
const detecting = ref("");
const preferences=usePreferencesStore();
const {theme,sideCollapsed,viewMode,simpleMode}=storeToRefs(preferences);
const {toggleTheme}=preferences;
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
  tasks: "任务中心",
  fleet: "群站管理",
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
  tasks: "统一查看任务排队、执行结果与失败原因",
  fleet: "集中接入和管理不同 VPS 上的独立站点",
  system: "查看服务状态、证书与最近操作记录",
  messages: "集中接收维护通知与账户消息",
  settings: "统一管理企业品牌、访问偏好与成员支持",
  sources: "管理上游订阅链接与每日错峰更新",
};
const allNavGroups = computed(() => [
  {id:'workspace',label:t('工作台'),items:[{id:'overview',label:t('仪表盘'),icon:LayoutDashboard},...(owner.value?[{id:'users',label:t('用户管理'),icon:Users}]:[]),{id:'messages',label:t('站内信'),icon:Mail}]},
  {id:'business',label:t('业务资源'),items:[...(owner.value?[{id:'ips',label:t('私有 IP 池'),icon:Database},{id:'nodes',label:t('普通节点'),icon:RadioTower}]:[]),{id:'subscription',label:t('普通订阅'),icon:QrCode}]},
  {id:'public',label:t('公共代理'),items:[...(owner.value?[{id:'public',label:t('公共 IP 池'),icon:Database},{id:'public-nodes',label:t('公共节点'),icon:Globe2}]:[]),{id:'public-subscription',label:t('公共订阅'),icon:QrCode}]},
  ...(owner.value?[{id:'admin',label:t('系统管理'),items:[...(state.value?.system.edition==='pro'&&!getRemoteSite()?[{id:'fleet',label:t('群站管理'),icon:Globe2}]:[]),{id:'tasks',label:t('任务中心'),icon:Server},{id:'system',label:t('运维状态'),icon:Server},{id:'settings',label:t('系统设置'),icon:Settings2}]}]:[]),
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
const {sub,subUser,format,subProtocol,qr,qrError,subLoading,subError,subURL,clearSubscription,loadSub,showSub,downloadSub} = useSubscription({state,page,owner,publicSubPage,active,task,go});
const usage = (u: User) =>
  u.quota ? Math.min(100, ((u.upload + u.download) / u.quota) * 100) : 0;
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
 busy.value=false;speedRunning.value="";qualityRunning.value="";
 setRemoteSite("");selectedSite.value="";selectedSiteName.value="";
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
 const generation=requestGeneration();
  busy.value = true;
  error.value = "";
  try {
    await fn();
  } catch (e) {
    if (generation===requestGeneration() && !isCancelled(e)) error.value = (e as Error).message;
  } finally {
    if(generation===requestGeneration()) busy.value = false;
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
  id=id.replace(/^\/+/, "");
 const target = id === "sources" ? "ips/sources" : id;
  const [section, nested] = target.split("/");
  id = section;
  if (!nav.value.some(item=>item.id===id)) id = owner.value ? "overview" : "subscription";
  if (id === "ips" && ["subscriptions", "manual", "sources"].includes(nested)) selectPrivateTab(nested);
  if (id === "public") publicTab.value = ["resources", "sources", "policy"].includes(nested) ? nested : "resources";
  page.value = id;
  void router.replace("/"+id+(id === "ips" ? "/"+privateTab.value : id === "public" && publicTab.value !== "resources" ? "/"+publicTab.value : ""));
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
async function copy(s: string) {
  try {
    await navigator.clipboard.writeText(s);
    toast(t("已复制"));
  } catch {
    error.value = t("剪贴板不可用，请选择内容复制");
  }
}
async function backup() {
 if(selectedSite.value){error.value=t("请在目标站点直接下载备份");return;}
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
 if(selectedSite.value){error.value=t("请返回本站修改登录密码");return;}
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
let lastFullRefresh=0;
const poll = serialPoll(async () => {
 if(!state.value||busy.value||document.hidden)return;
 if(Date.now()-lastFullRefresh>60000){await refresh(true);lastFullRefresh=Date.now();return;}
 const value=await api<{system:Partial<State['system']>;me:User;site:SiteSettings;runtime:State['runtime'];unread_messages:number}>('/operations');
 if(!state.value)return;
 state.value={...state.value,me:value.me,site:value.site,runtime:value.runtime,unread_messages:value.unread_messages,system:{...state.value.system,...value.system}};
},()=>10000);
let mounted = true;
watch(() => route.fullPath, path => { if (state.value) go(path); });
onMounted(async () => {
  await router.isReady();
 const initial = route.fullPath;
  try { site.value=await api<SiteSettings>('/site'); applyDefaultLocale(site.value.default_locale); } catch {}
  if (!mounted) return;
  await refresh(true);
  if (state.value) go(initial || (owner.value ? "overview" : "subscription"));
  if (!mounted) return;
  
  poll.start();
});
onUnmounted(() => { mounted = false; poll.stop(); stateRequest.cancel(); removeSessionListener(); clearTimeout(toastTimer);  clearSubscription(); });
return { selectedSite,selectedSiteName,switchSite, api, stateRequest, state, ready, busy, error, notice, page, mobileNav, site, login, userSearch, userFilter, userRole, modal, editingID, userForm, nodeForm, ipForm, speedRunning, qualityRunning, qualityTest, importMode, importForm, importFile, importFileReading, importIssues, importFileContent, importFileSequence, clearPrivateFile, readPrivateFile, importReport, openImport, importSubscription, speedTest, ipSearch, ipType, ipState, ipPool, publicResources, privateTab, privateSources, privateSourcesReady, privateSourcesLoading, privateSourcesError, privateSourcesRequest, receivePrivateSources, loadPrivateSources, publicTab, selectPublicTab, subscriptionResourceIDs, isSubscribedResource, subscriptionIPs, manualIPs, privateTabs, sourceLabel, selectPrivateTab, viewSourceResources, sourceChanged, selectedIP, poolStats, ipStatus, ipBadge, sourceFilter, filteredIPs, nodePoolLabel, expiringUsers, passwordForm, sub, subUser, format, subProtocol, qr, qrError, subLoading, subError, probeResult, detecting, theme, sideCollapsed, viewMode, simpleMode, nodeSearch, nodeProtocol, nodeStatus, publicNodePage, publicSubPage, filteredNodes, toggleTheme, originalExit, exitFingerprint, displayedNodeName, confirmation, owner, defaultRealitySNI, editingDefaultDirect, pendingHY, titles, pageDescriptions, allNavGroups, navGroups, nav, currentGroup, active, userStatus, users, subURL, usage, bytes, date, duration, exitName, refresh, clearSession, removeSessionListener, task, toastTimer, toast, signIn, signOut, go, editUser, saveUser, toggleUser, confirmUser, confirmed, editNode, probe, detectNode, editIP, saveIP, detectIP, deleteIP, selectedIPIDs, selectedNodeIDs, selectableNodes, selectAll, batchAction, saveNode, deleteNode, clearSubscription, loadSub, showSub, copy, downloadSub, download, backup, changePassword, actionName, poll, mounted };
}
