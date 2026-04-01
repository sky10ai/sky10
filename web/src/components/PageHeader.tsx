import type { ReactNode } from "react";

interface PageHeaderProps {
  actions?: ReactNode;
  description: ReactNode;
  eyebrow?: ReactNode;
  title: ReactNode;
}

export function PageHeader({
  actions,
  description,
  eyebrow,
  title,
}: PageHeaderProps) {
  return (
    <div className="flex flex-col gap-6 md:flex-row md:items-end md:justify-between">
      <div className="space-y-2">
        {eyebrow && (
          <p className="text-[10px] font-bold uppercase tracking-[0.24em] text-outline">
            {eyebrow}
          </p>
        )}
        <div className="space-y-2">
          <h1 className="text-4xl font-bold tracking-tight text-on-surface sm:text-[3.25rem]">
            {title}
          </h1>
          <p className="max-w-2xl text-sm text-secondary sm:text-base">
            {description}
          </p>
        </div>
      </div>
      {actions && (
        <div className="flex flex-wrap items-center gap-3 md:justify-end">
          {actions}
        </div>
      )}
    </div>
  );
}
