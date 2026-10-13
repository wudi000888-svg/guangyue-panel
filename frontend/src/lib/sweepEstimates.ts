import { treasuryPreviewMatches, type SweepInput, type SweepPreview } from './cryptoTreasury';
export const WALLET_GAS_POLL_MS = 5000;
export interface SweepEstimateContext { walletID: string; input: SweepInput }
export interface SweepEstimateState { preview?: SweepPreview; loading: boolean; error: string }
export type SweepEstimateAPI = (path: string, method?: string, body?: unknown, options?: RequestInit) => Promise<SweepPreview>;
export function sweepEstimateCurrent(value: SweepPreview | undefined, walletID: string, input: SweepInput, now: number): value is SweepPreview {
 return !!value && !!value.quote && Number.isFinite(value.checked_at) && value.checked_at <= now + 5 && Number.isFinite(value.expires_at) && value.expires_at > now && treasuryPreviewMatches(value, walletID, input);
}
/** Estimates only. There is no nonce allocation, task creation, transfer or funding endpoint here. */
export function createSweepEstimator(api: SweepEstimateAPI, publish: (state: SweepEstimateState) => void, clock = () => Date.now() / 1000) {
 let context: SweepEstimateContext | undefined, key = '', generation = 0, invalidated = true, controller: AbortController | undefined, flight: Promise<void> | undefined;
 let state: SweepEstimateState = { loading: false, error: '' };
 const update = (changes: Partial<SweepEstimateState>) => { state = { ...state, ...changes }; publish(state); };
 function cancel() { generation++; invalidated = true; controller?.abort(); controller = undefined; flight = undefined; update({ loading: false }); }
 function setContext(next: SweepEstimateContext | undefined) {
  const nextKey = next ? JSON.stringify([next.walletID, next.input]) : '';
  if (nextKey === key) return;
  cancel(); key = nextKey; context = next ? JSON.parse(JSON.stringify(next)) as SweepEstimateContext : undefined;
  state = { loading: false, error: '' }; publish(state);
 }
 function read(): Promise<void> {
  if (!context) return Promise.resolve();
  if (flight) return flight;
  const captured = context, current = ++generation, request = new AbortController(); controller = request; update({ loading: true });
  const run = (async () => {
   try {
    const value = await api('/wallets/' + encodeURIComponent(captured.walletID) + '/sweeps/preview', 'POST', captured.input, { signal: request.signal });
    if (current !== generation || request.signal.aborted) return;
    if (!sweepEstimateCurrent(value, captured.walletID, captured.input, clock())) throw new Error('提取预估与当前选择不一致或已过期，请重新预估');
    invalidated = false; update({ preview: value, error: '' });
   } catch (reason) { if (current === generation && !request.signal.aborted) update({ error: reason instanceof Error ? reason.message : String(reason) }); }
   finally { if (current === generation) { controller = undefined; flight = undefined; update({ loading: false }); } }
  })();
  flight = run; return run;
 }
 return { setContext, read, cancel, getState: () => state, freeze: (): SweepPreview | undefined => context && !invalidated && !state.loading && !state.error && sweepEstimateCurrent(state.preview, context.walletID, context.input, clock()) ? JSON.parse(JSON.stringify(state.preview)) as SweepPreview : undefined };
}
