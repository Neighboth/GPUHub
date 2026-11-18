/** Format usd_micros (integer, 1e-6 USD) as a dollar string, e.g. $0.0123 */
export function usd(micros?: number | null): string {
  const m = typeof micros === 'number' && Number.isFinite(micros) ? micros : 0;
  return `$${(m / 1e6).toFixed(4)}`;
}

/** Compact usd for axis ticks, e.g. $1.25 */
export function usdShort(micros?: number | null): string {
  const m = typeof micros === 'number' && Number.isFinite(micros) ? micros : 0;
  return `$${(m / 1e6).toFixed(2)}`;
}

export function fmtDateTime(s?: string | number | null): string {
  if (s === undefined || s === null || s === '') return '—';
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return String(s);
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function fmtDate(s?: string | number | null): string {
  if (s === undefined || s === null || s === '') return '—';
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return String(s);
  return d.toLocaleDateString(undefined, { month: 'short', day: '2-digit' });
}

export function shortId(id?: string | null, len = 8): string {
  if (!id) return '—';
  return id.length > len ? id.slice(0, len) : id;
}

export function isFuture(s?: string | null): boolean {
  if (!s) return false;
  const d = new Date(s);
  return !Number.isNaN(d.getTime()) && d.getTime() > Date.now();
}

export function daysFromNowFree(s?: string | null): string {
  return fmtDateTime(s);
}
