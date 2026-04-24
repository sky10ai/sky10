import type { ReactNode } from "react";

interface PageHeaderProps {
  actions?: ReactNode;
  children?: ReactNode;
  description?: ReactNode;
  title?: ReactNode;
}

export function PageHeader({
  actions,
  children,
  description,
  title,
}: PageHeaderProps) {
  return (
    <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between lg:gap-6">
      <div className="min-w-0 flex-1 space-y-3">
        {children ?? (
          <>
            <PageTitle>{title}</PageTitle>
            <PageDescription>{description}</PageDescription>
          </>
        )}
      </div>
      {actions && (
        <div className="flex shrink-0 flex-wrap items-center gap-3 lg:justify-end lg:pt-10 xl:pt-12">
          {actions}
        </div>
      )}
    </div>
  );
}

interface PageTextProps {
  children: ReactNode;
}

export function PageTitle({ children }: PageTextProps) {
  return (
    <h1 className="text-4xl font-semibold tracking-tight text-on-surface sm:text-[3.25rem]">
      {children}
    </h1>
  );
}

export function PageDescription({ children }: PageTextProps) {
  return (
    <p className="max-w-3xl pl-2 text-base text-secondary sm:text-lg">
      {children}
    </p>
  );
}
