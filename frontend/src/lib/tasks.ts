import { api, requestGeneration } from './api';

export type BackgroundTask<T = unknown> = {
  id: string; kind: string; target: string; state: string; created: number;
  started: number; finished: number; next_at: number; attempts: number; error?: string; result?: T;
};

export async function runQueued<T>(kind: string, target: string): Promise<T> {
  const generation = requestGeneration();
  const check = () => { if (generation !== requestGeneration()) throw new DOMException('Session or site changed', 'AbortError'); };
  const submitted = await api<BackgroundTask<T>>('/tasks', 'POST', { kind, target });
  for (;;) {
    check();
    const task = await api<BackgroundTask<T>>('/tasks/' + submitted.id);
    check();
    if (task.state === 'succeeded') return task.result as T;
    if (task.state === 'failed' || task.state === 'cancelled') throw new Error(task.error || task.state);
    await new Promise(resolve => setTimeout(resolve, 1200));
  }
}
