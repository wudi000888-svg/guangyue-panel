import { expect, it } from 'vitest';
import { ref } from 'vue';
import { useUserDirectory } from './useUserDirectory';
import type { State, User } from '../../types';

const now = Date.now() / 1000;
const user = (id: number, extra: Partial<User>) => ({ id, username: 'u' + id, role: 'member', enabled: true, expires: 0, quota: 0, ...extra }) as User;

it('filters members and reports their status', () => {
  const state = ref({ users: [user(1, {}), user(2, { enabled: false }), user(3, { expires: now - 10 }), user(4, { expires: now + 86400 }), user(5, { mount_access: { site_id: "x" } } as unknown as Partial<User>)] } as unknown as State);
  const directory = useUserDirectory(state);
  expect(directory.users.value.map(u => u.id)).toEqual([1, 2, 3, 4]);
  directory.userFilter.value = 'disabled';
  expect(directory.users.value.map(u => u.id)).toEqual([2]);
  directory.userFilter.value = 'expired';
  expect(directory.users.value.map(u => u.id)).toEqual([3]);
  expect(directory.expiringUsers.value).toBe(1);
  expect(directory.userStatus(state.value!.users[2]!)).toBe('已到期');
  expect(directory.active(state.value!.users[0]!)).toBe(true);
});
