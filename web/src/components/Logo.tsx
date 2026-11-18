// GPUHub logo - rounded square with circuit-style GPU chip glyph.
export default function Logo({ size = 28, withWordmark = true }: { size?: number; withWordmark?: boolean }) {
  const s = size;
  return (
    <span className="inline-flex select-none items-center gap-2" aria-label="GPUHub">
      <svg width={s} height={s} viewBox="0 0 48 48" role="img" aria-hidden="true">
        <rect x="2" y="2" width="44" height="44" rx="11" fill="#0c0f14" stroke="#10b981" strokeWidth="2.2" />
        {/* chip body */}
        <rect x="15" y="15" width="18" height="18" rx="3.5" fill="#10b981" fillOpacity="0.18" stroke="#34d399" strokeWidth="2" />
        {/* chip pins */}
        <g stroke="#34d399" strokeWidth="2" strokeLinecap="round">
          <line x1="21" y1="9" x2="21" y2="14" /><line x1="27" y1="9" x2="27" y2="14" />
          <line x1="21" y1="34" x2="21" y2="39" /><line x1="27" y1="34" x2="27" y2="39" />
          <line x1="9" y1="21" x2="14" y2="21" /><line x1="9" y1="27" x2="14" y2="27" />
          <line x1="34" y1="21" x2="39" y2="21" /><line x1="34" y1="27" x2="39" y2="27" />
        </g>
        {/* core: fan hub */}
        <circle cx="24" cy="24" r="4.6" fill="#10b981" />
        <circle cx="24" cy="24" r="1.8" fill="#0c0f14" />
      </svg>
      {withWordmark && (
        <span className="logo-wordmark text-[17px] font-extrabold tracking-tight text-zinc-50 leading-none">
          GPU<span className="text-emerald-400">Hub</span>
        </span>
      )}
    </span>
  );
}
