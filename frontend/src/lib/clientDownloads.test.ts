import { describe, expect, it } from 'vitest';
import clients from '../data/clients.json';
import { clientDownloadURL, detectClientPlatform, isGitHubReleaseDownload, platformDownloads } from './clientDownloads';

describe('client download routing', () => {
  it('preserves the server catalog index when filtering another operating system', () => {
    const client = clients.find(item => item.id === 'flclash')!;
    const linux = platformDownloads(client, 'Linux');
    expect(linux.map(item => item.index)).toEqual([7, 8]);
    expect(clientDownloadURL(client, linux[1]!.index, true)).toBe('/api/clients/download/flclash/8');
    expect(clientDownloadURL(client, linux[1]!.index)).toBe(client.downloads[8]!.url);
  });
  it('keeps app stores and F-Droid direct even when the relay is enabled', () => {
    for (const id of ['shadowrocket', 'cfa']) {
      const client = clients.find(item => item.id === id)!;
      expect(clientDownloadURL(client, 0, true)).toBe(client.downloads[0]!.url);
    }
    expect(clientDownloadURL(clients[0]!, -1, true)).toBe('');
    expect(clientDownloadURL(clients[0]!, 0.5, true)).toBe('');
    expect(clientDownloadURL(clients[0]!, 500, true)).toBe('');
  });
  it('routes only HTTPS GitHub release files through the site', () => {
    expect(isGitHubReleaseDownload('https://github.com/owner/repo/releases/download/v1/app.exe')).toBe(true);
    for (const url of ['http://github.com/o/r/releases/download/v1/app.exe', 'https://github.com.evil.test/o/r/releases/download/v1/app.exe', 'https://github.com/o/r/releases', 'https://user@github.com/o/r/releases/download/v1/app.exe']) {
      expect(isGitHubReleaseDownload(url)).toBe(false);
    }
  });
});

describe('device suggestions', () => {
  it('recognizes Android and iPad desktop mode without guessing a CPU architecture', () => {
    expect(detectClientPlatform('Mozilla/5.0 (Linux; Android 14)', 'Linux arm')).toBe('Android');
    expect(detectClientPlatform('Mozilla/5.0 (Macintosh; Intel Mac OS X)', 'MacIntel', 5)).toBe('iOS / iPadOS');
    expect(detectClientPlatform('Mozilla/5.0 (Macintosh; Intel Mac OS X)', 'MacIntel', 0)).toBe('macOS');
    expect(detectClientPlatform('unknown')).toBe('');
  });
});
