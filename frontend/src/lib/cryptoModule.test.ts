import { describe, expect, it } from 'vitest';
import { cryptoWorkspaceReadiness, cryptoWorkspaceSections } from './cryptoModule';

describe('standalone crypto payment workspace', () => {
  it('keeps wallet, payment setup, and treasury as separate operator sections', () => {
    expect(cryptoWorkspaceSections.map(section => section.id)).toEqual(['treasury', 'wallets', 'payments']);
    expect(new Set(cryptoWorkspaceSections.map(section => section.label)).size).toBe(3);
  });

  it('reports setup readiness without treating a disabled module as healthy', () => {
    expect(cryptoWorkspaceReadiness({ enabled: false, readiness: { ready: true, message: 'ok' } })).toEqual({
      tone: 'muted',
      label: '未开放加密货币付款',
      detail: '先完成支付模块与收款预检。',
    });
    expect(cryptoWorkspaceReadiness({ enabled: true, readiness: { ready: true, message: 'ok' } })).toEqual({
      tone: 'ready',
      label: '收款已就绪',
      detail: '已通过最近一次收款预检。',
    });
    expect(cryptoWorkspaceReadiness({ enabled: true, readiness: { ready: false, message: 'RPC 不可用' } })).toEqual({
      tone: 'warning',
      label: '需要检查',
      detail: 'RPC 不可用',
    });
  });
});
