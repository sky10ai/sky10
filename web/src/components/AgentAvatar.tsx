const PALETTES = [
  {
    canvas: "#fff7ed",
    base: "#f97316",
    accent: "#fde047",
    shade: "#7c2d12",
    panel: "#ffedd5",
    eye: "#1c1917",
  },
  {
    canvas: "#ecfeff",
    base: "#14b8a6",
    accent: "#a7f3d0",
    shade: "#134e4a",
    panel: "#ccfbf1",
    eye: "#111827",
  },
  {
    canvas: "#fff1f2",
    base: "#f43f5e",
    accent: "#fbcfe8",
    shade: "#881337",
    panel: "#ffe4e6",
    eye: "#1f2937",
  },
  {
    canvas: "#f7fee7",
    base: "#84cc16",
    accent: "#ecfccb",
    shade: "#365314",
    panel: "#d9f99d",
    eye: "#1a2e05",
  },
  {
    canvas: "#f0f9ff",
    base: "#38bdf8",
    accent: "#e0f2fe",
    shade: "#075985",
    panel: "#bae6fd",
    eye: "#0f172a",
  },
  {
    canvas: "#faf5ff",
    base: "#a855f7",
    accent: "#f0abfc",
    shade: "#581c87",
    panel: "#e9d5ff",
    eye: "#18181b",
  },
  {
    canvas: "#fffbeb",
    base: "#facc15",
    accent: "#fef3c7",
    shade: "#92400e",
    panel: "#fde68a",
    eye: "#1f2937",
  },
  {
    canvas: "#fff7ed",
    base: "#fb7185",
    accent: "#fed7aa",
    shade: "#9f1239",
    panel: "#ffe4e6",
    eye: "#111827",
  },
] as const;

function hashString(value: string) {
  let hash = 2166136261;
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

function avatarPalette(seed: number) {
  return PALETTES[seed % PALETTES.length] ?? PALETTES[0];
}

function Mouth({ mood, color }: { mood: number; color: string }) {
  if (mood === 0) {
    return (
      <>
        <rect fill={color} height="3" width="4" x="25" y="36" />
        <rect fill={color} height="3" width="6" x="29" y="39" />
        <rect fill={color} height="3" width="4" x="35" y="36" />
      </>
    );
  }
  if (mood === 1) {
    return <rect fill={color} height="3" width="14" x="25" y="38" />;
  }
  return (
    <>
      <rect fill={color} height="7" width="8" x="28" y="36" />
      <rect fill="#ffffff" height="2" opacity="0.8" width="4" x="30" y="37" />
    </>
  );
}

export function AgentAvatar({
  className = "",
  label,
  seed,
}: {
  className?: string;
  label: string;
  seed: string;
}) {
  const numericSeed = hashString(seed || label || "agent");
  const palette = avatarPalette(numericSeed);
  const mood = numericSeed % 3;
  const antennaStyle = (numericSeed >> 3) % 3;
  const hasBadge = ((numericSeed >> 7) & 1) === 1;

  return (
    <div
      aria-label={`${label} avatar`}
      className={`shrink-0 overflow-hidden rounded-lg shadow-sm ring-1 ring-black/5 ${className}`}
      role="img"
      style={{ backgroundColor: palette.canvas }}
    >
      <svg
        aria-hidden="true"
        className="h-full w-full"
        shapeRendering="crispEdges"
        viewBox="0 0 64 64"
      >
        <rect fill={palette.canvas} height="64" width="64" />
        <rect fill={palette.shade} height="36" opacity="0.16" width="36" x="16" y="18" />

        <rect fill={palette.shade} height="8" width="2" x="31" y="5" />
        {antennaStyle === 0 && <rect fill={palette.accent} height="6" width="10" x="27" y="1" />}
        {antennaStyle === 1 && <rect fill={palette.accent} height="8" width="8" x="28" y="0" />}
        {antennaStyle === 2 && (
          <>
            <rect fill={palette.accent} height="4" width="14" x="25" y="2" />
            <rect fill={palette.accent} height="4" width="4" x="30" y="0" />
          </>
        )}

        <rect fill={palette.shade} height="14" width="8" x="7" y="27" />
        <rect fill={palette.shade} height="14" width="8" x="49" y="27" />
        <rect fill={palette.accent} height="6" width="4" x="9" y="31" />
        <rect fill={palette.accent} height="6" width="4" x="51" y="31" />

        <rect fill={palette.shade} height="8" width="12" x="26" y="48" />
        <rect fill={palette.shade} height="7" width="32" x="16" y="55" />
        <rect fill={palette.base} height="4" width="24" x="20" y="55" />

        <rect fill={palette.shade} height="40" width="38" x="13" y="13" />
        <rect fill={palette.base} height="36" width="34" x="15" y="11" />
        <rect fill={palette.accent} height="4" width="20" x="22" y="15" />
        <rect fill={palette.panel} height="24" width="28" x="18" y="22" />

        <rect fill={palette.eye} height="8" width="7" x="23" y="28" />
        <rect fill={palette.eye} height="8" width="7" x="34" y="28" />
        <rect fill="#ffffff" height="2" opacity="0.9" width="2" x="25" y="30" />
        <rect fill="#ffffff" height="2" opacity="0.9" width="2" x="36" y="30" />

        <Mouth color={palette.eye} mood={mood} />

        <rect fill={palette.accent} height="3" width="3" x="20" y="39" />
        <rect fill={palette.accent} height="3" width="3" x="41" y="39" />
        {hasBadge && <rect fill={palette.accent} height="5" width="5" x="41" y="16" />}
        <rect fill={palette.shade} height="3" opacity="0.22" width="34" x="15" y="47" />
      </svg>
    </div>
  );
}
