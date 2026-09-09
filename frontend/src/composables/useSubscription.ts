import { computed, onScopeDispose, ref, watch, type Ref, type ComputedRef } from "vue";
import QRCode from "qrcode";
import { useApi, downloadBlob, getRemoteSite } from "../lib/api";
import { download } from "../lib/download";
import { t } from "../i18n";
import type { State, Sub, User } from "../types";
interface Context { state:Ref<State|null>;page:Ref<string>;owner:ComputedRef<boolean>;publicSubPage:ComputedRef<boolean>;active:(u:User)=>boolean;task:(fn:()=>Promise<void>)=>Promise<void>;go:(id:string)=>void }
export function useSubscription({state,page,owner,publicSubPage,active,task,go}:Context) {
const api=useApi();
const sub = ref<Sub | null>(null),
  subUser = ref(0),
  format = ref("mihomo"),
  subProtocol = ref(""),
  qr = ref(""),
  qrError = ref(""),
  subLoading = ref(false),
  subError = ref("");
const subURL = computed(() =>
  sub.value
    ? sub.value.url +
      "?format=" +
      format.value +
      (subProtocol.value ? "&protocol=" + subProtocol.value : "")
    : "",
);
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
async function downloadSub() {
  if (getRemoteSite()) { window.open(subURL.value, '_blank', 'noopener,noreferrer'); return; }
  await task(async () => {
    download(
      await downloadBlob(subURL.value),
      (publicSubPage.value ? "guangyue-public." : "guangyue.") + (format.value === "mihomo" ? "yaml" : "txt"),
    );
  });
}
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

onScopeDispose(()=>{clearSubscription();subscriptionQRSequence++;});
return {sub,subUser,format,subProtocol,qr,qrError,subLoading,subError,subURL,clearSubscription,loadSub,showSub,downloadSub};
}
