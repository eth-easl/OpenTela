'use client';

import { useEffect, useState } from 'react';
import { apiBaseUrl } from '../observatory/config';
import {
  DEFAULT_HOURS,
  fetchNetworkStats,
  formatTokenCount,
  type NetworkTotals,
} from './network-stats';

/**
 * Frontpage strip of live mesh totals. Fetches client-side like the
 * observatory (the endpoint is public and CORS-allowlisted), renders nothing
 * while loading and stays hidden on any failure — the landing page must
 * never depend on the analytics backend being up.
 */
export default function LiveNetworkStats() {
  const [totals, setTotals] = useState<NetworkTotals | null>(null);

  useEffect(() => {
    let alive = true;
    fetchNetworkStats(apiBaseUrl, DEFAULT_HOURS)
      .then((t) => {
        if (alive) setTotals(t);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, []);

  if (!totals || totals.requests === 0) return null;

  const tiles: { value: string; label: string }[] = [
    { value: totals.requests.toLocaleString(), label: 'requests routed' },
    { value: formatTokenCount(totals.input_tokens), label: 'input tokens served' },
    { value: formatTokenCount(totals.cached_input_tokens), label: 'cached input tokens' },
    { value: formatTokenCount(totals.output_tokens), label: 'output tokens generated' },
  ];

  return (
    <section className="mx-auto w-full max-w-6xl px-6 pb-12">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {tiles.map((tile) => (
          <div
            key={tile.label}
            className="rounded-xl border border-fd-border bg-fd-card p-5"
          >
            <p className="font-display text-3xl font-semibold tracking-tight">
              {tile.value}
            </p>
            <p className="mt-1 text-sm text-fd-muted-foreground">{tile.label}</p>
          </div>
        ))}
      </div>
      <p className="mt-4 text-sm text-fd-muted-foreground">
        Live from the gateway across the last {totals.window_days} days —{' '}
        <a href="/observatory" className="text-fd-primary hover:underline">
          mesh observatory →
        </a>
      </p>
    </section>
  );
}
