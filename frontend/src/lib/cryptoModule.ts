export type CryptoWorkspaceSectionID = 'payments' | 'wallets' | 'treasury';

export interface CryptoWorkspaceSection {
  id: CryptoWorkspaceSectionID;
  label: string;
  description: string;
}

export const cryptoWorkspaceSections: CryptoWorkspaceSection[] = [
  { id: 'payments', label: '收款配置', description: '选择钱包、网络、资产与报价策略' },
  { id: 'wallets', label: 'HD 钱包', description: '管理热钱包、公钥、地址和备份' },
  { id: 'treasury', label: '钱包资产', description: '查看全部资产、Gas 和归集任务' },
];

export interface CryptoWorkspaceSettings {
  enabled: boolean;
  readiness?: { ready: boolean; message: string };
}

export function cryptoWorkspaceReadiness(settings: CryptoWorkspaceSettings) {
  if (!settings.enabled) return { tone: 'muted' as const, label: '未开放加密货币付款', detail: '先完成支付模块与收款预检。' };
  if (settings.readiness?.ready) return { tone: 'ready' as const, label: '收款已就绪', detail: '已通过最近一次收款预检。' };
  return { tone: 'warning' as const, label: '需要检查', detail: settings.readiness?.message || '请完成收款预检。' };
}
