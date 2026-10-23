import { quotaUsed } from "../lib/quota";
import { download } from "../lib/download";
import { useAccessStore } from "../stores/access";
import { useRouter, useRoute } from "vue-router";
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { bytes, date, duration, exitName } from "../lib/format";
import { useSubscription } from "./useSubscription";
import { useToast } from "./panel/useToast";
import { usePanelNavigation } from "./panel/usePanelNavigation";
import { useSidebarCustomization } from './useSidebarCustomization';
import { useApi, ApiError, downloadBlob, JSON_REQUEST_HEADERS, invalidateSession, onSessionExpired, isCancelled, setRemoteSite, requestGeneration, errorMessage } from "../lib/api";
import { runQueued } from "../lib/tasks";
import { latestRequest, serialPoll } from "../lib/requests";
import { NodeSaveUncertain, saveNodeRecovering, upsertSavedNode } from "../lib/nodeSave";
import { operationID } from "../lib/commerce";
import { clearEmailProof } from '../lib/email';
import { storeToRefs } from "pinia";
import { usePreferencesStore } from "../stores/preferences";
import { t, locale, applyDefaultLocale } from "../i18n";
import { DEFAULT_ADMIN_SIDEBAR, DEFAULT_MEMBER_SIDEBAR } from "../lib/navigation";
import type { SiteSettings } from "../types";
import type { IPQuality } from "../quality";
import type { User, Node, IPResource, State, SpeedResult, Plan, NodeGroup } from "../types";


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
let needsNodeRefresh = false;
let nodeSaveRequestID = "";
const state = ref<State | null>(null),
  ready = ref(false),
  busy = ref(false),
  error = ref(""),
  page = ref("overview"),
  mobileNav = ref(false);
const {notice,toast}=useToast();
const access=useAccessStore();
watch(()=>[state.value?.me.role,state.value?.system.edition] as const,([role,edition])=>{access.role=role||null;access.edition=edition||'lite';},{flush:'sync'});
const site = ref<SiteSettings>({panel_name:'广月面板',organization:'跨境电商工作区',default_locale:'zh-CN',support_email:'',login_notice:'',registration_enabled:false,registration_captcha:false,revision:'',sidebar_admin:[...DEFAULT_ADMIN_SIDEBAR],sidebar_member:[...DEFAULT_MEMBER_SIDEBAR],client_download_relay:false,country_access_enabled:false,country_access_blocked:[],country_access_reason:''});
watch([site, locale], () => { document.title=site.value.panel_name+' · '+t('月庭'); }, {deep:true,immediate:true});
const login = reactive({ username: "", password: "" });
const registration = reactive({ username: "", password: "", confirm_password: "", captcha_token: "", email: "", email_token: "" });
const registrationReset = ref(0);
const userSearch = ref(""),
  userFilter = ref("all"),
  userRole = ref("all"),
  modal = ref(""),
  editingID = ref(0);
const plans=ref<Plan[]>([]),nodeGroups=ref<NodeGroup[]>([]),groupMembers=ref<Record<string,import("../types").GroupMember[]>>({});
async function loadEntitlements(){try{const [p,g]=await Promise.all([api<{plans:Plan[]}>("/plans"),api<{groups:NodeGroup[];members:Record<string,import("../types").GroupMember[]>}>("/node-groups")]);plans.value=p.plans;nodeGroups.value=g.groups;groupMembers.value=g.members;}catch(e){if(!isCancelled(e))error.value=errorMessage(e);}}
const userForm = reactive({
 plan_id:"",
  username: "",
  password: "",
  enabled: true,
  vless: true,
  hy2: true,
  expiresDate: "",
  quotaGB: 0,
});
const nodeForm = reactive<Node>({
 policy_version:1, rate_milli:1000, group_ids:[],
  square_visible: false,
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
async function sourceChanged() { await refresh(true); await loadPrivateSources(); }
// Navigation only refetches /state when the last full refresh is stale; the
// 10 s operations poll keeps runtime counters current in between.
const NAVIGATION_REFRESH_AGE = 15000;
let lastRefreshAt = 0;
watch(page, value => {
  const refreshing = !!state.value && Date.now() - lastRefreshAt >= NAVIGATION_REFRESH_AGE;
  if (refreshing) void refresh(true);
  // refresh() already loads private sources for these tabs; avoid a second request.
  if (value === 'ips' && !(refreshing && !['sources','subsites'].includes(privateTab.value))) loadPrivateSources();
});
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
        !u.mount_access && u.expires > Date.now() / 1000 &&
        u.expires < Date.now() / 1000 + 7 * 86400,
    ).length,
);
const passwordForm = reactive({ current: "", password: "", confirm: "" });
const probeResult = ref("");
const detecting = ref("");
const preferences=usePreferencesStore();
const {theme,sideCollapsed,publicFeaturesEnabled}=storeToRefs(preferences);
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
  /** Ask for the current administrator password and pass it to action(). */
  password?: boolean;
  action: (password: string) => Promise<void>;
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
const { titles, pageDescriptions, pageTitle, pageDescription, allNavGroups, navGroups, nav, currentGroup } = usePanelNavigation({ state, page, owner, selectedSite, publicFeaturesEnabled });
const sidebarCustomization = useSidebarCustomization({
  site, api, canCustomize: () => owner.value && !!state.value,
  available: () => nav.value.map(item => item.id),
  context: () => `${selectedSite.value}:${state.value?.me.id ?? ''}:${owner.value}`,
  onConfirmed: () => stateRequest.cancel(),
});
watch(() => nav.value.map(item => item.id).join(","), () => {
  if (state.value && !nav.value.some(item => item.id === page.value)) go(owner.value ? "overview" : "subscription");
});
const active = (u: User) =>
  u.enabled &&
  (!u.expires || u.expires > Date.now() / 1000) &&
  (!u.quota || quotaUsed(u) < u.quota) && !u.meter?.pending_reset && (!u.meter?.end || u.meter.end > Date.now()/1000);
const userStatus = (u: User) =>
 u.archived ? t("已归档") :
 u.meter?.pending_reset || (u.meter?.end && u.meter.end<=Date.now()/1000) ? t("周期切换中") :
  !u.enabled
    ? t("已停用")
    : u.expires && u.expires <= Date.now() / 1000
      ? t("已到期")
      : u.quota && quotaUsed(u) >= u.quota
        ? t("额度用尽")
        : t("正常");
const users = computed(() =>
  (state.value?.users || []).filter(
    (u) =>
      !u.mount_access && u.username.toLowerCase().includes(userSearch.value.toLowerCase()) &&
      (userRole.value === "all" || u.role === userRole.value) &&
      (userFilter.value === "all" ||
        (userFilter.value === "active"
          ? active(u)
          : userFilter.value === "disabled"
            ? !u.enabled
            : userFilter.value === "expired"
              ? !!u.expires && u.expires <= Date.now() / 1000
              : !!u.quota && quotaUsed(u) >= u.quota)),
  ),
);
const {sub,subUser,format,subProtocol,subSource,qr,qrError,subLoading,subError,subURL,clearSubscription,loadSub,showSub,downloadSub} = useSubscription({state,page,owner,publicSubPage,active,task,go});
const usage = (u: User) =>
  u.quota ? Math.min(100, (quotaUsed(u) / u.quota) * 100) : 0;
async function refresh(silent = false) {
  const request = stateRequest.start();
  try {
    const result = await api<State>("/state", "GET", undefined, { signal: request.signal });
    if (!stateRequest.isCurrent(request)) return false;
    state.value = result;
    lastRefreshAt = Date.now();
    needsNodeRefresh = false;
    site.value = result.site;
    applyDefaultLocale(site.value.default_locale);
    if (page.value === "ips" && !["sources","subsites"].includes(privateTab.value)) await loadPrivateSources();
    if (stateRequest.isCurrent(request) && !subUser.value && state.value) subUser.value = state.value.me.id;
    return true;
  } catch (e) {
    if (!isCancelled(e) && !silent && state.value) error.value = errorMessage(e);
    return false;
  } finally {
    if (stateRequest.isCurrent(request)) ready.value = true;
  }
}
function clearSession() {
 needsNodeRefresh=false;nodeSaveRequestID="";lastRefreshAt=0;
 busy.value=false;speedRunning.value="";qualityRunning.value="";
 setRemoteSite("");selectedSite.value="";selectedSiteName.value="";
  stateRequest.cancel();
  state.value = null;
  plans.value=[];nodeGroups.value=[];groupMembers.value={};
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
    if (generation===requestGeneration() && !isCancelled(e)) error.value = errorMessage(e);
  } finally {
    if(generation===requestGeneration()) busy.value = false;
  }
}
async function signIn() {
  await task(async () => {
    await api("/login", "POST", login);
    login.password = "";
    clearEmailProof();
    await refresh();
    subUser.value = state.value?.me.id || 0;
    go(owner.value ? "overview" : "subscription");
    toast(t("已登录"));
  });
}
async function registerAccount() {
  await task(async () => {
    try { await api('/register', 'POST', registration); } catch(e) { registration.captcha_token='';registrationReset.value++;throw e; }
    Object.assign(login, {username: registration.username, password: ''});
    Object.assign(registration, {username:'', password:'', confirm_password:'', captcha_token:'', email:'', email_token:''});
    clearEmailProof();
    registrationReset.value++;
    await refresh();
    subUser.value = state.value?.me.id || 0;
    go('subscription');
    toast(t('注册成功，已自动登录'));
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
 if (id.replace(/^\/+/, "").startsWith("email/")) return;
 const query=id.includes("?")?id.slice(id.indexOf("?")):"";id=id.split("?")[0];
  id=id.replace(/^\/+/, "");
 const target = id === "sources" ? "ips/sources" : id;
  const [section, nested] = target.split("/");
  id = section;
  if (!nav.value.some(item=>item.id===id)) id = owner.value ? "overview" : "subscription";
  if (id === "ips" && ["subscriptions", "manual", "sources", "subsites", "subsite-egress"].includes(nested)) selectPrivateTab(nested);
  if (id === "public") publicTab.value = ["resources", "sources", "policy"].includes(nested) ? nested : "resources";
  page.value = id;
  void router.replace("/"+id+(id === "ips" ? "/"+privateTab.value : id === "public" && publicTab.value !== "resources" ? "/"+publicTab.value : id === "fleet" && nested === "independent" ? "/independent" : "")+(id===section?query:""));
  nextTick(() => window.scrollTo({ top: 0, behavior: "instant" }));
  mobileNav.value = false;
  error.value = "";
}
function editUser(u?: User) {
 void loadEntitlements();
  editingID.value = u?.id || 0;
  Object.assign(userForm, {
 plan_id:u?.entitlement?.plan_id||plans.value.find(p=>p.kind==='demo'&&!p.archived)?.id||"",
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
        plan_id:editingID.value?"":userForm.plan_id,
 username: userForm.username,
        password: userForm.password,
        enabled: userForm.enabled,
        vless: userForm.vless,
        hy2: userForm.hy2,
        quota: Math.round(userForm.quotaGB * 1024 ** 3),
        expires: editingID.value && state.value?.users.find(u=>u.id===editingID.value)?.entitlement ? state.value.users.find(u=>u.id===editingID.value)!.expires : userForm.expiresDate
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
        t("旧订阅和节点凭据将失效。HY2 会撤销连接，VLESS 会通过核心重启关闭现有连接。"),
      "rotate-all-sub": t("重置后所有旧订阅地址失效，已有节点凭据仍然有效。"),
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
          "rotate-all-sub": t("重置全部订阅地址"),
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
async function confirmed(password = "") {
  await task(async () => {
    await confirmation.value?.action(password);
    confirmation.value = null;
  });
}
function editNode(n?: Node, protocol = "vless") {
 void loadEntitlements();
  if (n?.managed_by) { go('public'); return; }
  nodeSaveRequestID = operationID();
  Object.assign(
    nodeForm,
    n
      ? { ...n, dns: n.dns ? {...n.dns} : {mode:"system"}, password: "", default_direct: !!n.default_direct, reality_sni: n.protocol === "vless" ? n.reality_sni || "" : "", ...(n.default_direct ? { enabled: true, exit: "direct", exit_id: "" } : {}) }
      : {
          policy_version:1,rate_milli:1000,group_ids:[],
          square_visible: false,
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
  let saved: IPResource | undefined;
  await task(async () => {
    const creating = !ipForm.id;
    const savingSite = selectedSite.value;
    const p: IPResource = await api("/ips", "POST", ipForm);
    saved = p;
    if (state.value && selectedSite.value === savingSite) {
      state.value.ip_pool = [...(state.value.ip_pool || []).filter(ip => ip.id !== p.id), p];
    }
    modal.value = "";
    if (creating) go("ips/manual");
    await refresh();
    toast(
      p.enabled && p.probe_error
        ? t("IP 已保存，检测结果：") + p.probe_error
        : t("IP 资源已保存"),
    );
  });
  return saved;
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
    const saved = await saveNodeRecovering(api, {
      save_request_id: nodeForm.id ? operationID() : nodeSaveRequestID || (nodeSaveRequestID = operationID()),
 policy_version:1, rate_milli:nodeForm.rate_milli??1000,group_ids:nodeForm.group_ids??[],
      id: nodeForm.id,
      protocol: nodeForm.protocol,
      enabled: editingDefaultDirect.value ? true : nodeForm.enabled,
      exit_id: editingDefaultDirect.value ? "" : nodeForm.exit_id,
      ...(nodeForm.protocol === "vless" ? { reality_sni: realitySNI } : {}),
      dns: nodeForm.dns ? {...nodeForm.dns} : undefined,
      square_visible: !!nodeForm.square_visible,
    }, undefined, state.value?.system.node_save_receipts === true).catch(error => {
      if (error instanceof NodeSaveUncertain) needsNodeRefresh = true;
      throw error;
    });
    // The write already succeeded. Keep its authoritative result even when
    // applying the core briefly disconnects the following list request.
    stateRequest.cancel();
    if (state.value) state.value = {...state.value, nodes: upsertSavedNode(state.value.nodes, saved)};
    error.value = "";
    modal.value = "";
    needsNodeRefresh = true;
    toast(sniChanged ? t("节点已应用，请更新客户端订阅以使用新的 SNI。") : t("节点已应用，出口名称已更新"));
    await refresh(true);
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
    detail: t("备份含账号、节点凭据与恢复密钥，请仅保存在可信设备。") + " " + t("下载前请验证管理员当前密码。"),
    password: true,
    action: async (password: string) => {
      download(
        await downloadBlob("/api/backup", { method: "POST", headers: JSON_REQUEST_HEADERS, body: JSON.stringify({ password }) }),
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
const poll = serialPoll(async () => {
 if(!state.value||busy.value||document.hidden)return;
 if(needsNodeRefresh||Date.now()-lastRefreshAt>60000){await refresh(true);return;}
 const value=await api<{system:Partial<State['system']>;me:User;site:SiteSettings;runtime:State['runtime'];unread_messages:number}>('/operations');
 const current=state.value;
 if(!current)return;
 // Patch only the polled fields so computeds over users/nodes/ip_pool stay cached.
 current.me=value.me;current.site=value.site;current.runtime=value.runtime;current.unread_messages=value.unread_messages;
 Object.assign(current.system,value.system);
},()=>10000);
let mounted = true;
watch(() => route.fullPath, async (path, previous) => {
 if (route.meta.publicEmail) return;
 if (previous.startsWith('/email/')) { await refresh(true); if (state.value) go(path); }
 else if (state.value) go(path);
});
onMounted(async () => {
  await router.isReady();
 const initial = route.fullPath;
  try { site.value=await api<SiteSettings>('/site'); applyDefaultLocale(site.value.default_locale); } catch {}
  if (!mounted) return;
  if (route.meta.publicEmail) { ready.value = true; poll.start(); return; }
  await refresh(true);
  if (state.value) go(initial || (owner.value ? "overview" : "subscription"));
  if (!mounted) return;
  
  poll.start();
});
onUnmounted(() => { mounted = false; poll.stop(); stateRequest.cancel(); removeSessionListener(); clearSubscription(); });
return { sidebarCustomization, canMountSubsites,plans,nodeGroups,groupMembers,loadEntitlements,quotaUsed,selectedSite,selectedSiteName,switchSite, api, stateRequest, state, ready, busy, error, notice, page, mobileNav, site, login, registration, registrationReset, userSearch, userFilter, userRole, modal, editingID, userForm, nodeForm, ipForm, speedRunning, qualityRunning, qualityTest, importMode, importForm, importFile, importFileReading, importIssues, clearPrivateFile, readPrivateFile, importReport, openImport, importSubscription, speedTest, ipSearch, ipType, ipState, ipPool, publicResources, privateTab, privateSources, privateSourcesReady, privateSourcesLoading, privateSourcesError, receivePrivateSources, loadPrivateSources, publicTab, selectPublicTab, subscriptionResourceIDs, isSubscribedResource, subscriptionIPs, manualIPs, privateTabs, sourceLabel, selectPrivateTab, viewSourceResources, sourceChanged, selectedIP, poolStats, ipStatus, ipBadge, sourceFilter, filteredIPs, nodePoolLabel, expiringUsers, passwordForm, sub, subUser, format, subProtocol, subSource, qr, qrError, subLoading, subError, probeResult, detecting, theme, sideCollapsed, publicFeaturesEnabled, nodeSearch, nodeProtocol, nodeStatus, filteredNodes, publicNodePage, publicSubPage, toggleTheme, originalExit, exitFingerprint, displayedNodeName, confirmation, owner, defaultRealitySNI, editingDefaultDirect, pendingHY, titles, pageTitle, pageDescription, pageDescriptions, allNavGroups, navGroups, nav, currentGroup, active, userStatus, users, subURL, usage, bytes, date, duration, exitName, refresh, clearSession, task, toast, signIn, signOut, registerAccount, go, editUser, saveUser, toggleUser, confirmUser, confirmed, editNode, probe, detectNode, editIP, saveIP, detectIP, deleteIP, selectedIPIDs, selectedNodeIDs, selectableNodes, selectAll, batchAction, saveNode, deleteNode, clearSubscription, loadSub, showSub, copy, downloadSub, download, backup, changePassword, actionName, poll };
}
