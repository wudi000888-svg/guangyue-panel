export type LiveValue = { upload_rate: number | null; download_rate: number | null; vless: number | null; hy2: number | null };
export type LivePoint = { at: number; upload_rate: number | null; download_rate: number | null };
export type LiveSnapshot = {
  site_id: string; sampled_at: number; stale: boolean; traffic_available: boolean[]; connections_available: boolean[];
  total: LiveValue; selected: LiveValue; user_id: number; user_count: number; page: number;
  users: (LiveValue & { id: number; username: string })[]; history: LivePoint[];
};

// Preserve unknown samples and long sampling gaps as breaks in the graph.
export function ratePath(points: LivePoint[], field: 'upload_rate' | 'download_rate', ceiling: number): string {
  const start = points[0]?.at ?? 0, span = Math.max(5000, (points.at(-1)?.at ?? start) - start);
  let pen = false, previous = 0;
  return points.map(p => {
    const value = p[field];
    if (value === null || !Number.isFinite(value) || value < 0) { pen = false; return ''; }
    const x = 60 + (p.at - start) / span * 820, y = 205 - value / Math.max(1, ceiling) * 175;
    const command = pen && p.at - previous <= 15000 ? 'L' : 'M';
    pen = true; previous = p.at;
    return `${command}${x.toFixed(2)},${y.toFixed(2)}`;
  }).join(' ');
}
