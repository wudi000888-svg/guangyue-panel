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
    { path: '/users', component: () => import('./pages/UsersPage.vue'), meta: { owner: true } },
    { path: '/ips/:tab?', component: () => import('./pages/PrivatePoolPage.vue'), meta: { owner: true } },
    { path: '/nodes', component: () => import('./pages/NodesPage.vue'), meta: { owner: true } },
    { path: '/subscription', component: () => import('./pages/SubscriptionPage.vue') },
    { path: '/public/:tab?', component: () => import('./pages/PublicPoolPage.vue'), meta: { owner: true, professional: true } },
    { path: '/public-nodes', component: () => import('./pages/NodesPage.vue'), meta: { owner: true, professional: true } },
    { path: '/public-subscription', component: () => import('./pages/SubscriptionPage.vue'), meta: { professional: true } },
    { path: '/messages', component: () => import('./pages/MessagesPage.vue') },
    { path: '/settings', component: () => import('./pages/SettingsPage.vue'), meta: { owner: true } },
    { path: '/fleet', component: () => import('./pages/BusinessPage.vue'), meta: { owner: true, pro: true } },
    { path: '/fleet/independent', component: () => import('./pages/FleetPage.vue'), meta: { owner: true, pro: true } },
    { path: '/tasks', component: () => import('./pages/TasksPage.vue'), meta: { owner: true } },
    { path: '/system', component: () => import('./pages/SystemPage.vue'), meta: { owner: true } },
    { path: '/sources', redirect: '/ips/sources' },
    { path: '/:pathMatch(.*)*', redirect: '/overview' },
  ],
});

router.beforeEach(to=>{
  const access=useAccessStore(),preferences=usePreferencesStore();
  if(!access.role) return true; // The shell withholds page content until authentication.
  if(!allowedRoute(to.meta,{role:access.role,edition:access.edition,simple:preferences.simpleMode})) return access.role==='owner'?'/overview':'/subscription';
  return true;
});
