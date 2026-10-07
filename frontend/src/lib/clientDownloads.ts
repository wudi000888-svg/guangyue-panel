import clients from '../data/clients.json';

export const clientPlatforms = ['Windows', 'macOS', 'Linux', 'Android', 'iOS / iPadOS'] as const;
export type ClientPlatform = typeof clientPlatforms[number];
export type Client = typeof clients[number];

export function platformDownloads(client: Client, platform = '') {
  // Keep the catalog index before filtering: it is also the server's allowlist key.
  return client.downloads.map((download, index) => ({ ...download, index }))
    .filter(download => !platform || download.platform === platform);
}

export function isGitHubReleaseDownload(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && url.hostname === 'github.com' && !url.port &&
      !url.username && !url.password && !url.search && !url.hash &&
      /^\/[^/]+\/[^/]+\/releases\/download\/[^/]+\/[^/]+$/.test(url.pathname);
  } catch { return false; }
}

export function clientDownloadURL(client: Client, index: number, relay = false): string {
  const download = Number.isInteger(index) ? client.downloads[index] : undefined;
  if (!download) return '';
  return relay && isGitHubReleaseDownload(download.url)
    ? `/api/clients/download/${encodeURIComponent(client.id)}/${index}`
    : download.url;
}

export function detectClientPlatform(userAgent: string, platform = '', touchPoints = 0): ClientPlatform | '' {
  if (/Android/i.test(userAgent)) return 'Android';
  if (/iPhone|iPad|iPod/i.test(userAgent) || (/Mac/i.test(platform) && touchPoints > 1)) return 'iOS / iPadOS';
  if (/Windows/i.test(userAgent)) return 'Windows';
  if (/Macintosh|Mac OS X/i.test(userAgent)) return 'macOS';
  if (/Linux/i.test(userAgent)) return 'Linux';
  return '';
}
