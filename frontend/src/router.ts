import { useAccessStore } from './stores/access';
import { usePreferencesStore } from './stores/preferences';
import { allowedRoute } from './lib/access';
import { createRouter, createWebHashHistory } from 'vue-router';

// Preserve bookmarks from releases that used #ips/sources rather than #/ips/sources.
if (location.hash && !location.hash.startsWith('#/')) {
  history.replaceState(history.state, '', '#/' + location.hash.slice(1));
}
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/overview' },
    { path: '/overview', component: () => import('./pages/OverviewPage.vue') },
    { path: '/clients', component: () => import('./pages/ClientsPage.vue') },
    { path: '/monitor', component: () => import('./pages/MonitorPage.vue'), meta: { owner: true } },
    { path: '/plans', component: () => import('./pages/PlansPage.vue'), meta: { owner: true } },
    { path: '/node-groups', component: () => import('./pages/NodeGroupsPage.vue'), meta: { owner: true } },
    { path: '/users', component: () => import('./pages/UsersPage.vue'), meta: { owner: true } },
    { path: '/ips/:tab?', component: () => import('./pages/PrivatePoolPage.vue'), meta: { owner: true } },
    { path: '/nodes', component: () => import('./pages/NodesPage.vue'), meta: { owner: true } },
    { path: '/subsite-nodes', component: () => import('./pages/SubsiteNodesPage.vue'), meta: { owner: true, pro: true } },
    { path: '/subscription', component: () => import('./pages/SubscriptionPage.vue') },
    { path: '/public/:tab?', component: () => import('./pages/PublicPoolPage.vue'), meta: { owner: true, professional: true, publicFeatures: true } },
    { path: '/public-nodes', component: () => import('./pages/NodesPage.vue'), meta: { owner: true, professional: true, publicFeatures: true } },
    { path: '/public-subscription', component: () => import('./pages/SubscriptionPage.vue'), meta: { owner: true, professional: true, publicFeatures: true } },
    { path: '/wallet', component: () => import('./pages/WalletPage.vue') },
    { path: '/shop', component: () => import('./pages/ShopPage.vue') },
    { path: '/orders', component: () => import('./pages/OrdersPage.vue') },
    { path: '/tickets', component: () => import('./pages/TicketsPage.vue') },
    { path: '/redeem-codes', component: () => import('./pages/RedeemCodesPage.vue'), meta: { owner: true } },
    { path: '/messages', component: () => import('./pages/MessagesPage.vue') },
    { path: '/settings', component: () => import('./pages/SettingsPage.vue'), meta: { owner: true } },
    { path: '/fleet', component: () => import('./pages/BusinessPage.vue'), meta: { owner: true, pro: true } },
    { path: '/fleet/independent', redirect: '/fleet', meta: { owner: true, pro: true } },
    { path: '/pairing', component: () => import('./pages/FleetPage.vue'), meta: { owner: true } },
    { path: '/tasks', component: () => import('./pages/TasksPage.vue'), meta: { owner: true } },
    { path: '/system', component: () => import('./pages/SystemPage.vue'), meta: { owner: true } },
    { path: '/sources', redirect: '/ips/sources' },
    { path: '/:pathMatch(.*)*', redirect: '/overview' },
  ],
});

router.beforeEach(to=>{
  const access=useAccessStore(),preferences=usePreferencesStore();
  if(!access.role) return true; // The shell withholds page content until authentication.
  if(!allowedRoute(to.meta,{role:access.role,edition:access.edition,simple:preferences.simpleMode,publicFeatures:preferences.publicFeaturesEnabled})) return access.role==='owner'?'/overview':'/subscription';
  return true;
});
