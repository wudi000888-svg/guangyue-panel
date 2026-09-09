import { afterEach, expect, it, vi } from 'vitest';
import { latestRequest, serialPoll } from './requests';
afterEach(() => vi.useRealTimers());
it('only the newest filter response may update a view', () => {
  const requests = latestRequest(), old = requests.start(), current = requests.start();
  expect(old.signal.aborted).toBe(true); expect(requests.isCurrent(old)).toBe(false);
  expect(requests.isCurrent(current)).toBe(true);
  requests.cancel(); expect(requests.isCurrent(current)).toBe(false);
});
it('does not overlap slow polls and stops scheduling after unmount', async () => {
  vi.useFakeTimers(); let finish!: () => void;
  const work = vi.fn(() => new Promise<void>(r => { finish = r; }));
  const poll = serialPoll(work, () => 1000);
  poll.start(); poll.start();
  await vi.advanceTimersByTimeAsync(1000); expect(work).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(30000); expect(work).toHaveBeenCalledTimes(1);
  finish(); await Promise.resolve();
  await vi.advanceTimersByTimeAsync(1000); expect(work).toHaveBeenCalledTimes(2);
  poll.stop(); finish(); await Promise.resolve();
  await vi.advanceTimersByTimeAsync(30000); expect(work).toHaveBeenCalledTimes(2);
});
