import { expect, it } from 'vitest';
import { computed, ref } from 'vue';
import { usePanelNavigation } from './usePanelNavigation';
import type { State } from '../../types';

const setup = (role: 'owner' | 'member', publicFeatures = true) => {
  const state = ref({ me: { role }, system: { edition: 'lite', role: '' } } as unknown as State);
  const page = ref('subscription');
  return { page, nav: usePanelNavigation({ state, page, owner: computed(() => role === 'owner'), selectedSite: ref(''), publicFeaturesEnabled: ref(publicFeatures) }) };
};

it('shows members only their own pages with member titles', () => {
  const { nav } = setup('member');
  const ids = nav.nav.value.map(item => item.id);
  expect(ids).toContain('subscription');
  expect(ids).not.toContain('users');
  expect(nav.pageTitle.value).toBe('我的订阅');
});

it('hides public-pool pages when public features are off', () => {
  const { nav, page } = setup('owner', false);
  expect(nav.nav.value.map(item => item.id)).not.toContain('public');
  page.value = 'shop';
  expect(nav.pageTitle.value).toBe('套餐上架');
  expect(nav.currentGroup.value).toBe('系统与服务');
});
