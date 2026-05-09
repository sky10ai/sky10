const GRID_SIZE = 7;

const PALETTES = [
  {
    canvas: "#fff7ed",
    base: "#f97316",
    accent: "#fde047",
    shade: "#7c2d12",
    eye: "#1c1917",
  },
  {
    canvas: "#ecfeff",
    base: "#14b8a6",
    accent: "#a7f3d0",
    shade: "#134e4a",
    eye: "#111827",
  },
  {
    canvas: "#fff1f2",
    base: "#f43f5e",
    accent: "#fbcfe8",
    shade: "#881337",
    eye: "#1f2937",
  },
  {
    canvas: "#f7fee7",
    base: "#84cc16",
    accent: "#ecfccb",
    shade: "#365314",
    eye: "#1a2e05",
  },
  {
    canvas: "#f0f9ff",
    base: "#38bdf8",
    accent: "#e0f2fe",
    shade: "#075985",
    eye: "#0f172a",
  },
  {
    canvas: "#faf5ff",
    base: "#a855f7",
    accent: "#f0abfc",
    shade: "#581c87",
    eye: "#18181b",
  },
  {
    canvas: "#fffbeb",
    base: "#facc15",
    accent: "#fef3c7",
    shade: "#92400e",
    eye: "#1f2937",
  },
  {
    canvas: "#fff7ed",
    base: "#fb7185",
    accent: "#fed7aa",
    shade: "#9f1239",
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

function pixelActive(seed: number, x: number, y: number) {
  const mixed = hashString(`${seed}:${Math.min(x, GRID_SIZE - 1 - x)}:${y}`);
  return mixed % 5 !== 0;
}

function avatarPalette(seed: number) {
  return PALETTES[seed % PALETTES.length] ?? PALETTES[0];
}

function pixelColor(seed: number, x: number, y: number) {
  const palette = avatarPalette(seed);
  const inHead = y >= 1 && y <= 5 && x >= 1 && x <= 5;
  const antenna = y === 0 && x === 3;
  const ears = y === 3 && (x === 0 || x === 6);

  if (!inHead && !antenna && !ears) return "transparent";
  if ((y === 2 && (x === 2 || x === 4)) || (y === 4 && x >= 2 && x <= 4)) {
    return palette.eye;
  }
  if (y === 1 && (x === 1 || x === 5)) return "transparent";
  if (antenna || ears || (x === 3 && y === 1)) return palette.accent;
  if (x === 1 || x === 5 || y === 5) return palette.shade;
  if (!pixelActive(seed, x, y)) return palette.accent;
  return palette.base;
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
  const cells = Array.from({ length: GRID_SIZE * GRID_SIZE }, (_, index) => {
    const x = index % GRID_SIZE;
    const y = Math.floor(index / GRID_SIZE);
    return pixelColor(numericSeed, x, y);
  });

  return (
    <div
      aria-label={`${label} avatar`}
      className={`grid shrink-0 overflow-hidden rounded-lg p-1 shadow-sm ring-1 ring-black/5 ${className}`}
      role="img"
      style={{
        backgroundColor: palette.canvas,
        gridTemplateColumns: `repeat(${GRID_SIZE}, minmax(0, 1fr))`,
      }}
    >
      {cells.map((color, index) => (
        <span
          aria-hidden="true"
          className="aspect-square"
          key={index}
          style={{ backgroundColor: color }}
        />
      ))}
    </div>
  );
}
