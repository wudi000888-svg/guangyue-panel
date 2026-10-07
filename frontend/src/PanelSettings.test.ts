import {describe, expect, it} from 'vitest';
import {mergePanelSettings} from './PanelSettings.vue';
import type {SiteSettings} from './types';

const settings = (): SiteSettings => ({
 panel_name:'My site', organization:'Team', default_locale:'zh-CN', support_email:'', login_notice:'',
 registration_enabled:false, registration_captcha:false, revision:'initial',
 sidebar_admin:['overview','settings'], sidebar_member:['subscription','settings'],
 client_download_relay:false, country_access_enabled:false, country_access_blocked:[], country_access_reason:'',
});

describe('explicit settings save alongside automatic navigation changes',()=>{
 it('preserves local non-navigation drafts while carrying forward both latest sidebars and revision',()=>{
  const baseline=settings(),draft={...baseline,panel_name:'Unsaved name',country_access_blocked:['US']};
  const current={...baseline,revision:'sidebar-save',sidebar_admin:['plans','settings'],sidebar_member:['orders','settings']};
  const payload=mergePanelSettings(baseline,draft,current);
  expect(payload).toMatchObject({panel_name:'Unsaved name',country_access_blocked:['US'],revision:'sidebar-save',sidebar_admin:['plans','settings'],sidebar_member:['orders','settings']});
  expect(baseline.panel_name).toBe('My site');
  expect(draft.sidebar_admin).toEqual(['overview','settings']);
  payload.sidebar_admin.push('nodes');
  expect(current.sidebar_admin).toEqual(['plans','settings']);
 });

 it.each([
  {panel_name:'Other administrator'},
  {registration_enabled:true},
  {client_download_relay:true},
  {country_access_blocked:['CN']},
 ])('refuses concurrent non-navigation edits instead of hiding a settings conflict: %j',changes=>{
  const baseline=settings();
  expect(()=>mergePanelSettings(baseline,{...baseline,organization:'Local draft'},{...baseline,...changes,revision:'new-revision'})).toThrow('设置已被其他管理员修改，请重新加载');
 });

 it('permits a revision-only change and protects newly introduced non-navigation fields',()=>{
  const baseline=settings();
  expect(mergePanelSettings(baseline,baseline,{...baseline,revision:'same-values'}).revision).toBe('same-values');
  expect(()=>mergePanelSettings(baseline,baseline,{...baseline,new_access_rule:true} as SiteSettings)).toThrow('设置已被其他管理员修改，请重新加载');
 });
});
