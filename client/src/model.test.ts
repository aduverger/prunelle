import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadPreferences, defaults } from './model';
import { serverURL } from './api/client';

describe('local preferences and server targets', () => {
  afterEach(() => vi.unstubAllGlobals());
  it('restores only valid presentation settings and tolerates unavailable storage', () => {
    vi.stubGlobal('localStorage', {
      getItem: () =>
        JSON.stringify({ ...defaults, perspective: 'Design', views: ['TEXT', 'DIAGRAM'] }),
    });
    expect(loadPreferences().perspective).toBe('Design');
    vi.stubGlobal('localStorage', {
      getItem: () =>
        '{"perspective":"toString","views":["TEXT","DIAGRAM"],"split":true,"server":"http://127.0.0.1:7390"}',
    });
    expect(loadPreferences()).toEqual(defaults);
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('Storage denied');
      },
    });
    expect(loadPreferences()).toEqual(defaults);
  });
  it('accepts Tailscale HTTPS or loopback, never credentials or an arbitrary remote target', () => {
    expect(serverURL('https://dev.tail123.ts.net/')).toBe('https://dev.tail123.ts.net');
    expect(serverURL('http://127.0.0.1:7390')).toBe('http://127.0.0.1:7390');
    for (const target of [
      'http://dev.tail123.ts.net',
      'https://example.org',
      'https://dev.ts.net.evil.org',
      'http://user@localhost:7390',
      'http://localhost:7390/v1',
      'http://localhost:7390?x=1',
    ]) {
      expect(() => serverURL(target)).toThrow();
    }
  });
});
