import { afterEach, expect, it, vi } from 'vitest';
import { ApiError, invalidateSession } from './api';
import { NodeSaveUncertain, saveNodeRecovering, type NodeSaveInput } from './nodeSave';

const input: NodeSaveInput = { id: '', protocol: 'vless', enabled: true, save_request_id: 'node-save-fixture-1' };
const saved = { id: 'vless-created', name: 'saved', create_request_id: input.save_request_id, save_request_id: input.save_request_id };
const wait = async () => {};
afterEach(() => { invalidateSession(); vi.restoreAllMocks(); });

it('recovers a committed write after the POST response and first reads are lost', async () => {
  const client = vi.fn().mockRejectedValueOnce(new TypeError('Failed to fetch'))
    .mockRejectedValueOnce(new TypeError('Failed to fetch')).mockResolvedValueOnce({nodes: []}).mockResolvedValueOnce({nodes: [saved]});
  expect(await saveNodeRecovering(client, input, wait)).toEqual(saved);
  expect(client.mock.calls.map(call => call.slice(0, 2))).toEqual([['/nodes', 'POST'], ['/state', 'GET'], ['/state', 'GET'], ['/state', 'GET']]);
});
it('never treats a similar node from another request as this save', async () => {
  const client = vi.fn().mockRejectedValueOnce(new TypeError('Failed to fetch')).mockResolvedValue({nodes: [{...saved, create_request_id: 'other', save_request_id: 'other'}]});
  await expect(saveNodeRecovering(client, input, wait)).rejects.toBeInstanceOf(NodeSaveUncertain);
  expect(client.mock.calls.filter(call => call[1] === 'POST')).toHaveLength(1);
});
it('recovers edits only by their current save receipt', async () => {
  const update = {...input, id: saved.id, save_request_id: 'node-edit-fixture-1'};
  const client = vi.fn().mockRejectedValueOnce(new TypeError('Failed to fetch')).mockResolvedValueOnce({nodes: [saved, {...saved, id: 'other-node', save_request_id: update.save_request_id}]})
    .mockResolvedValueOnce({nodes: [{...saved, save_request_id: update.save_request_id}]});
  expect((await saveNodeRecovering(client, update, wait)).save_request_id).toBe(update.save_request_id);
  expect(client).toHaveBeenCalledTimes(3);
});
it('preserves validation errors without attempting recovery', async () => {
  const client = vi.fn().mockRejectedValue(new ApiError(400, {error: 'invalid node'}));
  await expect(saveNodeRecovering(client, input, wait)).rejects.toMatchObject({status: 400, message: 'invalid node'});
  expect(client).toHaveBeenCalledTimes(1);
});
it('uses the original payload for older independent sites', async () => {
  const client = vi.fn().mockResolvedValue(saved);
  expect(await saveNodeRecovering(client, input, wait, false)).toEqual(saved);
  expect(client.mock.calls[0][2]).not.toHaveProperty('save_request_id');
  client.mockRejectedValueOnce(new TypeError('Failed to fetch'));
  await expect(saveNodeRecovering(client, input, wait, false)).rejects.toBeInstanceOf(NodeSaveUncertain);
  expect(client).toHaveBeenCalledTimes(2);
});
it('does not report success after a known apply failure and rollback', async () => {
  const client = vi.fn().mockRejectedValueOnce(new ApiError(502, {error: 'apply failed'})).mockResolvedValue({nodes: []});
  await expect(saveNodeRecovering(client, input, wait)).rejects.toMatchObject({status: 502, message: 'apply failed'});
});
it('stops recovery when the account or selected site changes during backoff', async () => {
  const client = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
  await expect(saveNodeRecovering(client, input, async () => { invalidateSession(); })).rejects.toMatchObject({name: 'AbortError'});
  expect(client).toHaveBeenCalledTimes(1);
});

it('continues recovery after a bounded read times out', async () => {
  const timeout = vi.spyOn(AbortSignal, 'timeout').mockReturnValueOnce(new AbortController().signal)
    .mockReturnValueOnce(AbortSignal.abort()).mockReturnValueOnce(new AbortController().signal);
  const client = vi.fn().mockRejectedValueOnce(new TypeError('Failed to fetch'))
    .mockRejectedValueOnce(new DOMException('Timed out', 'AbortError')).mockResolvedValueOnce({nodes: [saved]});
  expect(await saveNodeRecovering(client, input, wait)).toEqual(saved);
  expect(timeout.mock.calls.map(call => call[0])).toEqual([60000, 5000, 5000]);
});

it('stops recovery when the view is disposed', async () => {
  const client = vi.fn().mockRejectedValueOnce(new TypeError('Failed to fetch')).mockRejectedValueOnce(new DOMException('Disposed', 'AbortError'));
  await expect(saveNodeRecovering(client, input, wait)).rejects.toMatchObject({name: 'AbortError'});
  expect(client).toHaveBeenCalledTimes(2);
});
