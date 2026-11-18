import { PROVIDER_ICONS } from '../lib/providerIcons.generated';

// Embedded brand icons (downloaded 2026-11 from public favicon services / Google s2; kept in-repo - no external calls).
const PNG_ICONS = import.meta.glob('../assets/providers/*.png', {
  eager: true,
  import: 'default',
  query: '?url',
}) as unknown as Record<string, string>;

function pngFor(id: string): string | undefined {
  const hit = Object.entries(PNG_ICONS).find(([p]) => p.endsWith(`/${id}.png`));
  return hit?.[1];
}

// Deterministic fallback palette for providers without a brand mark in simple-icons.
const MONOGRAM_COLORS = [
  '#10b981', '#f59e0b', '#3b82f6', '#8b5cf6', '#ef4444', '#14b8a6',
  '#f97316', '#6366f1', '#22c55e', '#e11d48',
];

function hashCode(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = (Math.imul(31, h) + s.charCodeAt(i)) | 0;
  }
  return Math.abs(h);
}

function monogram(id: string): string {
  const parts = id.replace(/[-_]+/g, ' ').trim().split(' ').filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  const p = parts[0] ?? id;
  return (p.slice(0, 2) || 'GP').toUpperCase();
}

export default function ProviderIcon({ id, size = 18, className = '' }: { id: string; size?: number; className?: string }) {
  const icon = PROVIDER_ICONS[id];
  if (icon) {
    return (
      <span className={`inline-flex shrink-0 items-center justify-center rounded-md border border-zinc-800 bg-zinc-900/70 ${className}`}
        style={{ width: size + 12, height: size + 12 }} title={icon.title}>
        <svg width={size} height={size} viewBox="0 0 24 24" fill={`#${icon.hex}`} role="img" aria-label={icon.title}>
          <path d={icon.path} />
        </svg>
      </span>
    );
  }
  const png = pngFor(id);
  if (png) {
    return (
      <span className={`inline-flex shrink-0 items-center justify-center overflow-hidden rounded-md border border-zinc-800 bg-zinc-900/70 ${className}`}
        style={{ width: size + 12, height: size + 12 }}>
        <img src={png} alt={id} width={size} height={size} style={{ objectFit: 'contain' }} loading="lazy" />
      </span>
    );
  }
  // Brand monogram fallback
  const hue = MONOGRAM_COLORS[hashCode(id) % MONOGRAM_COLORS.length];
  return (
    <span className={`inline-flex shrink-0 items-center justify-center rounded-md border font-bold text-white ${className}`}
      style={{ width: size + 12, height: size + 12, backgroundColor: hue + '26', borderColor: hue + '66', fontSize: Math.round(size * 0.52), color: hue }}
      title={id} aria-label={id}>
      {monogram(id)}
    </span>
  );
}
