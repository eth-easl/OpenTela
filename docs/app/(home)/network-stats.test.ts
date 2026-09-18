import { afterEach, describe, expect, it, vi } from 'vitest';
import { fetchNetworkStats, formatTokenCount } from './network-stats';

afterEach(() => vi.restoreAllMocks());

describe('formatTokenCount', () => {
  it('presents magnitudes the way people say them', () => {
    expect(formatTokenCount(0)).toBe('0');
    expect(formatTokenCount(563)).toBe('563');
    expect(formatTokenCount(9500)).toBe('9.5 thousand');
    expect(formatTokenCount(374056)).toBe('374 thousand');
    expect(formatTokenCount(1000000)).toBe('1 million');
    expect(formatTokenCount(48394719)).toBe('48.4 million');
    expect(formatTokenCount(1500000000)).toBe('1.5 billion');
  });
});

describe('fetchNetworkStats', () => {
  // Shaped like the live /v1/leaderboard response's usage section.
  const BODY = {
    generated_at: '2026-09-18T12:00:00Z',
    window_hours: 720,
    window_days: 30,
    entries: [],
    usage: [
      {
        day: '2026-09-18',
        model: 'zai-org/GLM-5.3-Flash',
        requests: 563,
        input_tokens: 48000000,
        cached_input_tokens: 0,
        output_tokens: 370000,
      },
      {
        day: '2026-09-17',
        model: 'zai-org/GLM-5.3-Flash',
        requests: 12,
        input_tokens: 394719,
        cached_input_tokens: 40,
        output_tokens: 4056,
      },
    ],
  };

  it('sums the daily usage rows into one set of totals', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify(BODY), { status: 200 })),
    );
    const totals = await fetchNetworkStats('https://api.example.com', 720);
    expect(totals.requests).toBe(575);
    expect(totals.input_tokens).toBe(48394719);
    expect(totals.cached_input_tokens).toBe(40);
    expect(totals.output_tokens).toBe(374056);
    expect(totals.window_days).toBe(30);
  });

  it('derives the window from hours when the API omits window_days', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ ...BODY, window_days: undefined }), {
          status: 200,
        }),
      ),
    );
    expect((await fetchNetworkStats('https://api.example.com', 720)).window_days).toBe(30);
    expect((await fetchNetworkStats('https://api.example.com', 48)).window_days).toBe(2);
  });

  it('throws so the frontpage can stay quiet when the API is down', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('nope', { status: 503 })),
    );
    await expect(fetchNetworkStats('https://api.example.com')).rejects.toThrow('503');
  });

  it('throws when the deployment has no usage section at all', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ entries: [] }), { status: 200 })),
    );
    await expect(fetchNetworkStats('https://api.example.com')).rejects.toThrow('usage');
  });
});
