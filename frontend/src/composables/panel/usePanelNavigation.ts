import { computed, type Ref } from "vue";
import { Mail, Globe2, KeyRound, LayoutDashboard, Database, QrCode, RadioTower, Server, Settings2, Users, Package, Wallet, Ticket, ShoppingBag, Receipt, Gift, Layers3, Coins } from "lucide-vue-next";
import { t } from "../../i18n";
import { getRemoteSite } from "../../lib/api";
import type { State } from "../../types";
import { titles, pageDescriptions, memberTitles } from "./pageMeta";

/** Sidebar groups, the flat navigation list and the current page's title/description. */
export function usePanelNavigation({ state, page, owner, selectedSite, publicFeaturesEnabled }: {
  state: Ref<State | null>; page: Ref<string>; owner: Ref<boolean>; selectedSite: Ref<string>; publicFeaturesEnabled: Ref<boolean>;
}) {
const pageTitle = computed(() => page.value === 'shop' && owner.value ? t('套餐上架') : t((!owner.value && memberTitles[page.value]) || titles[page.value] || ''));
const pageDescription = computed(() => page.value === 'shop' && owner.value ? t('管理套餐上架与销售规则') : t(pageDescriptions[page.value] || ''));
const allNavGroups = computed(() => [
  {id:'workspace',label:t('工作台'),items:[{id:'overview',label:t('仪表盘'),icon:LayoutDashboard},{id:'clients',label:t('客户端中心'),icon:Package},...(owner.value?[{id:'monitor',label:t('实时监控'),icon:RadioTower}]:[])]},
  {id:'access',label:t('用户与套餐'),items:[...(owner.value?[{id:'users',label:t('用户管理'),icon:Users},{id:'plans',label:t('套餐管理'),icon:Package},{id:'node-groups',label:t('节点组'),icon:Layers3}]:[]),{id:'subscription',label:owner.value?t('订阅管理'):t('我的订阅'),icon:QrCode},{id:'node-square',label:t('节点广场'),icon:Globe2},{id:'messages',label:t('站内信'),icon:Mail}]},
  {id:'resources',label:t('节点与出口'),items:[...(owner.value?[{id:'ips',label:t('本地 IP 池'),icon:Database},{id:'nodes',label:t('本地节点'),icon:RadioTower},{id:'subsite-nodes',label:t('子站节点'),icon:Globe2},{id:'public',label:t('公共 IP 池'),icon:Database},{id:'public-nodes',label:t('公共节点'),icon:Globe2},{id:'public-subscription',label:t('公共订阅'),icon:QrCode}]:[])]},
  {id:'system',label:t('系统与服务'),items:[...(!selectedSite.value?[{id:'wallet',label:owner.value?t('账户余额'):t('我的钱包'),icon:Wallet},{id:'shop',label:owner.value?t('套餐上架'):t('购买套餐'),icon:ShoppingBag},{id:'orders',label:owner.value?t('订单管理'):t('我的订单'),icon:Receipt},{id:'tickets',label:t('工单中心'),icon:Ticket},...(owner.value?[{id:'crypto-payments',label:t('加密货币'),icon:Coins},{id:'redeem-codes',label:t('兑换码管理'),icon:Gift}]:[])]:[]),...(owner.value&&state.value?.system.edition==='pro'&&state.value?.system.role==='controller'&&!getRemoteSite()?[{id:'fleet',label:t('群站管理'),icon:Globe2}]:[]),...(owner.value&&!getRemoteSite()?[{id:'pairing',label:t('配对令牌'),icon:KeyRound}]:[]),...(owner.value?[{id:'tasks',label:t('任务中心'),icon:Server},{id:'system',label:t('运维状态'),icon:Server}]:[]),{id:'settings',label:owner.value?t('系统设置'):t('个人设置'),icon:Settings2}]},
]);
const navGroups = computed(() => {
  const hidePublic = !publicFeaturesEnabled.value;
  if (!hidePublic) return allNavGroups.value;
  const allowed = new Set(owner.value
    ? ["overview", "clients", "monitor", "users", "plans", "crypto-payments", "node-groups", "node-square", "ips", "nodes", "subsite-nodes", "subscription", "settings", "wallet", "shop", "orders", "tickets", "redeem-codes", "messages", "pairing", "fleet", "tasks", "system"]
    : ["clients", "subscription", "node-square", "wallet", "shop", "orders", "tickets", "messages", "settings"]);
  return allNavGroups.value
    .map(group => ({ ...group, items: group.items.filter(item => allowed.has(item.id)) }))
    .filter(group => group.items.length);
});
const nav = computed(()=>navGroups.value.flatMap(group=>group.items));
const currentGroup = computed(()=>navGroups.value.find(g=>g.items.some(item=>item.id===page.value))?.label || '');
return { titles, pageDescriptions, pageTitle, pageDescription, allNavGroups, navGroups, nav, currentGroup };
}
