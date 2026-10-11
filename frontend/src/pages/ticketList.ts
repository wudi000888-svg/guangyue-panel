export type TicketListFilter = 'all' | 'active' | 'waiting_user' | 'done';

/** Build the server query used by TicketsPage for every list refresh. */
export function ticketListPath(filter: TicketListFilter, search: string, before = ''): string {
  const params = new URLSearchParams();
  if (filter !== 'all') params.set('state', filter);
  const text = search.trim();
  if (text) params.set('search', text);
  if (before) params.set('before', before);
  const query = params.toString();
  return query ? `/tickets?${query}` : '/tickets';
}

export function ticketListHasMore(payload: { has_more?: boolean; items?: unknown[] }): boolean {
  return payload.has_more ?? (payload.items?.length ?? 0) >= 50;
}
