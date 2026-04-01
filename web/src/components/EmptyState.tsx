import type { ReactNode } from "react";
import { Icon } from "./Icon";

interface EmptyStateProps {
  action?: ReactNode;
  description: ReactNode;
  icon: string;
  title: ReactNode;
}

export function EmptyState({
  action,
  description,
  icon,
  title,
}: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-outline-variant/20 bg-surface-container-low/30 px-6 py-20 text-center">
      <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-surface-container-high text-outline">
        <Icon name={icon} className="text-4xl" />
      </div>
      <h3 className="text-lg font-semibold text-on-surface">{title}</h3>
      <p className="mt-2 max-w-md text-sm leading-relaxed text-secondary">
        {description}
      </p>
      {action && <div className="mt-6">{action}</div>}
    </div>
  );
}
