import { afterEach, describe, expect, it, vi } from 'vitest';
import { effectScope, ref, type EffectScope } from 'vue';
import { ApiError, type api } from '../lib/api';
import { useSidebarCustomization } from './useSidebarCustomization';
import type { SiteSettings } from '../types';

const scopes: EffectScope[] = [];
afterEach(() => scopes.splice(0).forEach(scope => scope.stop()));
function setup(custom?: (path: string, method: string, body: any, normal: () => unknown) => unknown) {
  const site = ref<SiteSettings>({panel_name:'Site',organization:'Team',default_locale:'zh-CN',support_email:'',login_notice:'',registration_enabled:false,registration_captcha:false,revision:'s0',sidebar_admin:['overview','settings'],sidebar_member:['subscription','settings'],client_download_relay:false,country_access_enabled:false,country_access_blocked:[],country_access_reason:''});
  const remote = ref('master'), owner = ref(true), server: SiteSettings = JSON.parse(JSON.stringify(site.value));
  let version = 0;
  const response = (role: 'admin'|'member') => ({role,items:[...server[role==='admin'?'sidebar_admin':'sidebar_member']],revision:role+server[role==='admin'?'sidebar_admin':'sidebar_member'].join(','),site_revision:server.revision});
  const request = vi.fn(async (path: string, method='GET', body?: any) => {
    const role = (method==='GET'?path.split('role=')[1]:body.role) as 'admin'|'member';
    const normal = () => {
      if (method==='PUT') {
        if (body.revision!==response(role).revision) throw new ApiError(409,{error:'Conflict'});
        server[role==='admin'?'sidebar_admin':'sidebar_member']=[...body.items];
        server.revision='s'+(++version);
      }
      return response(role);
    };
    return custom ? await custom(path,method,body,normal) : normal();
  });
  const scope=effectScope();scopes.push(scope);
  const controller=scope.run(()=>useSidebarCustomization({site,api:request as typeof api,canCustomize:()=>owner.value,available:()=>['overview','clients','users','settings'],context:()=>remote.value}))!;
  const settle=()=>controller.withSettingsLock(async()=>{});
  return {site,server,request,controller,settle,remote,owner,response};
}

describe('sidebar automatic persistence',()=>{
  it('renders rapid moves immediately, serializes writes and survives refresh without losing a later move',async()=>{
    const s=setup();
    s.controller.setPinned('clients',true);
    s.controller.setPinned('users',true);
    s.controller.setPinned('clients',false);
    expect(s.controller.selected('admin')).toEqual(['overview','users','settings']);
    expect(s.controller.saving.value).toBe(true);
    s.site.value={...s.site.value,sidebar_admin:['overview','settings']};
    expect(s.controller.selected('admin')).toContain('users');
    await s.settle();
    expect(s.server.sidebar_admin).toEqual(['overview','users','settings']);
    expect(s.controller.selected('admin')).toEqual(s.server.sidebar_admin);
    expect(s.controller.saving.value).toBe(false);
    expect(s.controller.saved.value).toBe(true);
    expect(s.server.panel_name).toBe('Site');
  });
  it('rebases only the moved item after another administrator changes the same sidebar',async()=>{
    let conflict=true;
    const s=setup((_path,method,_body,normal)=>{
      if(method==='PUT'&&conflict){conflict=false;s.server.sidebar_admin=['overview','users','settings'];throw new ApiError(409,{error:'Conflict'});}
      return normal();
    });
    s.controller.setPinned('clients',true);
    await s.settle();
    expect(s.server.sidebar_admin).toEqual(['overview','users','clients','settings']);
    expect(s.controller.error.value).toBe('');
  });
  it('reads back a committed write after its response is lost instead of undoing it',async()=>{
    const s=setup((_path,method,_body,normal)=>{const value=normal();if(method==='PUT')throw new Error('Disconnected');return value;});
    s.controller.setPinned('clients',true);
    await s.settle();
    expect(s.controller.selected('admin')).toContain('clients');
    expect(s.controller.saved.value).toBe(true);
    expect(s.controller.error.value).toBe('');
  });
  it('rolls back a rejected move, keeps later moves and allows retry without submitting other settings',async()=>{
    let reject=true;
    const s=setup((_path,method,body,normal)=>{if(method==='PUT'&&body.items.includes('clients')&&reject)throw new ApiError(503,{error:'Unavailable'});return normal();});
    s.controller.setPinned('clients',true);
    s.controller.setPinned('users',true);
    await s.settle();
    expect(s.controller.selected('admin')).toEqual(['overview','users','settings']);
    expect(s.controller.error.value).toContain('Unavailable');
    expect(s.controller.saved.value).toBe(false);
    reject=false;s.controller.retry();await s.settle();
    expect(s.server.sidebar_admin).toEqual(['overview','users','clients','settings']);
    expect(s.request.mock.calls.every(call=>call[0].startsWith('/settings/navigation'))).toBe(true);
  });
  it('ignores an old site response and clears transient drag state on site switch',async()=>{
    let release!:()=>void;
    const s=setup((_path,_method,_body,normal)=>new Promise(resolve=>{release=()=>resolve(normal());}));
    s.controller.setPinned('clients',true);
    await Promise.resolve();
    s.remote.value='child';s.site.value={...s.site.value,panel_name:'Child',sidebar_admin:['users','settings']};
    release();await s.settle();
    expect(s.controller.selected('admin')).toEqual(['users','settings']);
    expect(s.request.mock.calls).toHaveLength(1);
    expect(s.controller.saving.value).toBe(false);
  });
  it('discards a failed move that the user subsequently reversed, so retry cannot resurrect it',async()=>{
    const s=setup((_path,method,body,normal)=>{if(method==='PUT'&&body.items.includes('clients'))throw new ApiError(503,{error:'Unavailable'});return normal();});
    s.controller.setPinned('clients',true);
    s.controller.setPinned('clients',false);
    await s.settle();
    expect(s.controller.selected('admin')).not.toContain('clients');
    expect(s.controller.error.value).toBe('');
    const calls=s.request.mock.calls.length;
    s.controller.retry();await s.settle();
    expect(s.request.mock.calls).toHaveLength(calls);
    expect(s.server.sidebar_admin).not.toContain('clients');
  });
  it('shares its queue with a full settings save and preserves the latest optimistic moves',async()=>{
    const s=setup();
    s.controller.setPinned('clients',true);
    const save=s.controller.withSettingsLock(async()=>{
      expect(s.server.sidebar_admin).toContain('clients');
      s.server.panel_name='New name';s.server.revision='explicit';s.controller.acceptSettings({...s.server});
    });
    s.controller.setPinned('users',true);
    await save;await s.settle();
    expect(s.site.value.panel_name).toBe('New name');
    expect(s.server.sidebar_admin).toEqual(['overview','clients','users','settings']);
  });
  it('keeps settings fixed and rejects member or foreign drag writes',async()=>{
    const s=setup();
    s.controller.setPinned('settings',false);
    s.controller.drop({preventDefault:vi.fn(),stopPropagation:vi.fn()} as unknown as DragEvent,'sidebar');
    s.owner.value=false;s.controller.setPinned('clients',true);s.controller.setSelected('member',['orders']);
    await s.settle();
    expect(s.request).not.toHaveBeenCalled();
    expect(s.controller.selected('admin')).toContain('settings');
  });
});
