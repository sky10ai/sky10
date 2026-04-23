import type { ReactNode } from "react";

interface PageHeaderProps {
  actions?: ReactNode;
  description: ReactNode;
  title: ReactNode;
}

export function PageHeader({ actions, description, title }: PageHeaderProps) {
  return (
    <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between lg:gap-6">
      <div className="min-w-0 flex-1 space-y-2">
        <h1 className="text-4xl font-semibold tracking-tight text-on-surface sm:text-[3.25rem]">
          {title}
        </h1>
        <p className="max-w-3xl text-base text-secondary sm:text-lg">
          {description}
        </p>
      </div>
      {actions && (
        <div className="flex shrink-0 flex-wrap items-center gap-3 lg:justify-end lg:pt-10 xl:pt-12">
          {actions}
        </div>
      )}
    </div>
  );
}
