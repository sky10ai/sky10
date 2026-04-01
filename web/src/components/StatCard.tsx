import type { ReactNode } from "react";

interface StatCardProps {
  detail?: ReactNode;
  label: ReactNode;
  value: ReactNode;
}

export function StatCard({ detail, label, value }: StatCardProps) {
  return (
    <div className="rounded-xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
      <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
        {label}
      </p>
      <p className="mt-2 text-xl font-semibold text-on-surface">{value}</p>
      {detail && <p className="mt-2 text-xs text-secondary">{detail}</p>}
    </div>
  );
}
