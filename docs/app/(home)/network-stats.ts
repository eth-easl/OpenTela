/**
 * Live network totals for the frontpage: what the public mesh has actually
 * served, from the same permissionless `GET /v1/leaderboard` endpoint the
 * observatory reads. The daily `usage` section carries per-day token totals;
 * we sum it into one set of headline numbers and format them for humans —
 * "48.4 million", not 48394719.
 */

export interface UsageRow {
  day: string;
  model: string;
  requests: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
}

export interface NetworkTotals {
  /** Responses whose usage billing would settle (the usage default). */
  requests: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
  /** Window the totals cover, in whole days (derived from the hours asked). */
  window_days: number;
}

export const DEFAULT_HOURS = 720; // 30 days — the raw-sample retention

/** Fetch the leaderboard and sum its daily usage rows into headline totals. */
export async function fetchNetworkStats(
  baseUrl: string,
  hours: number = DEFAULT_HOURS,
): Promise<NetworkTotals> {
  const res = await fetch(`${baseUrl}/v1/leaderboard?hours=${hours}`);
  if (!res.ok) throw new Error(`leaderboard unavailable: ${res.status}`);
  const body = (await res.json()) as {
    window_days?: number;
    usage?: Partial<UsageRow>[] | null;
  };
  if (!body.usage) throw new Error('usage section unavailable');
  const totals: NetworkTotals = {
    requests: 0,
    input_tokens: 0,
    cached_input_tokens: 0,
    output_tokens: 0,
    window_days: body.window_days ?? Math.ceil(hours / 24),
  };
  for (const row of body.usage) {
    totals.requests += row.requests ?? 0;
    totals.input_tokens += row.input_tokens ?? 0;
    totals.cached_input_tokens += row.cached_input_tokens ?? 0;
    totals.output_tokens += row.output_tokens ?? 0;
  }
  return totals;
}

/** Round a magnitude for display: one decimal below 100 of the unit, then
 * whole numbers ("48.4 million" but "374 thousand"). */
function compact(value: number, unit: string): string {
  const rounded = value >= 100 ? value.toFixed(0) : value.toFixed(1);
  const trimmed = rounded.endsWith('.0') ? rounded.slice(0, -2) : rounded;
  return `${trimmed} ${unit}`;
}

/**
 * Human token counts: "48.4 million input tokens", never a raw 48394719.
 * Below a thousand the exact number is already readable, so it stays.
 */
export function formatTokenCount(n: number): string {
  if (n >= 1e9) return compact(n / 1e9, 'billion');
  if (n >= 1e6) return compact(n / 1e6, 'million');
  if (n >= 1e3) return compact(n / 1e3, 'thousand');
  return String(Math.round(n));
}
