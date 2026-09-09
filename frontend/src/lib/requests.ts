export function latestRequest() {
  let current: AbortController | undefined;
  return {
    start() { current?.abort(); current = new AbortController(); return current; },
    isCurrent(request: AbortController) { return current === request && !request.signal.aborted; },
    cancel() { current?.abort(); current = undefined; },
  };
}

// Schedule after completion so a slow request cannot accumulate more polls.
export function serialPoll(work: () => Promise<unknown>, delay: () => number) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = true;
  const schedule = () => { if (!stopped) timer = setTimeout(tick, delay()); };
  const tick = async () => {
    try { await work(); } catch { /* The caller owns error presentation. */ } finally { schedule(); }
  };
  return {
    start() { if (!stopped) return; stopped = false; schedule(); },
    stop() { stopped = true; clearTimeout(timer); },
  };
}
