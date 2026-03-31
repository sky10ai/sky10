/** Material Symbols Outlined icon wrapper. */
export function Icon({
  name,
  className = "",
  filled = false,
  size,
}: {
  name: string;
  className?: string;
  filled?: boolean;
  size?: string;
}) {
  const style = filled
    ? { fontVariationSettings: "'FILL' 1, 'wght' 400, 'GRAD' 0, 'opsz' 24" }
    : undefined;

  return (
    <span
      className={`material-symbols-outlined ${size ?? ""} ${className}`}
      style={style}
    >
      {name}
    </span>
  );
}
