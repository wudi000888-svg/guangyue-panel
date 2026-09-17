import { ApiError, isCancelled, requestGeneration } from './api';
import { t } from '../i18n';
import type { Node, State } from '../types';

export type NodeSaveInput = Pick<Node, 'id' | 'protocol' | 'enabled' | 'exit_id' | 'reality_sni' | 'dns' | 'rate_milli' | 'group_ids' | 'policy_version'> & { save_request_id: string };
type Client = <T>(path: string, method?: string, body?: unknown, options?: RequestInit) => Promise<T>;

export class NodeSaveUncertain extends Error {
  constructor() { super(t('连接中断，尚未确认节点保存结果。请恢复网络后重试，勿重复新建节点。')); }
}

// Never automatically repeat a mutation. Reconcile its receipt using bounded,
// read-only requests after a core restart interrupts the browser's connection.
export async function saveNodeRecovering(client: Client, input: NodeSaveInput, wait = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms)), receipts = true): Promise<Node> {
  const generation = requestGeneration();
  const check = () => { if (generation !== requestGeneration()) throw new DOMException('Request cancelled', 'AbortError'); };
  const timeout = AbortSignal.timeout(60000);
  try {
    // Older independent sites reject unknown JSON fields. Use their original
    // write contract unless the selected site advertises receipt support.
    const {save_request_id: _receipt, ...legacyInput} = input;
    const node = await client<Node>('/nodes', 'POST', receipts ? input : legacyInput, { signal: timeout });
    check();
    return node;
  } catch (error) {
    check();
    if (isCancelled(error) && !timeout.aborted || error instanceof ApiError && error.status < 500) throw error;
    if (!receipts) {
      if (error instanceof ApiError && error.data.error) throw error;
      throw new NodeSaveUncertain();
    }
    for (const delay of [0, 1000, 2000, 4000, 8000]) {
      await wait(delay);
      check();
      const readTimeout = AbortSignal.timeout(5000);
      try {
        const result = await client<State>('/state', 'GET', undefined, { signal: readTimeout });
        check();
        const saved = result.nodes.find(node => input.id
          ? node.id === input.id && node.save_request_id === input.save_request_id
          : node.create_request_id === input.save_request_id);
        if (saved) return saved;
      } catch (readError) {
        check();
        if (isCancelled(readError) && !readTimeout.aborted || readError instanceof ApiError && readError.status < 500) throw readError;
      }
    }
    if (error instanceof ApiError && error.data.error) throw error;
    throw new NodeSaveUncertain();
  }
}

export function upsertSavedNode(nodes: Node[], saved: Node): Node[] {
  return nodes.some(node => node.id === saved.id) ? nodes.map(node => node.id === saved.id ? saved : node) : [...nodes, saved];
}
