import type { ReactNode } from "react";
import { Link } from "react-router";
import type { PinnablePageID } from "../lib/pinnablePages";
import { Icon } from "./Icon";
import { PageDescription, PageHeader, PageTitle } from "./PageHeader";
import { PinPageButton } from "./PinPageButton";

const widthClasses = {
  default: "max-w-6xl",
  narrow: "max-w-5xl",
  wide: "max-w-7xl",
} as const;

type SettingsPageWidth = keyof typeof widthClasses;

interface SettingsPageProps {
  actions?: ReactNode;
  backHref?: string;
  backLabel?: string;
  children?: ReactNode;
  description: ReactNode;
  pinnablePageID?: PinnablePageID;
  title: ReactNode;
  width?: SettingsPageWidth;
}

export function SettingsPage({
  actions,
  backHref,
  backLabel = "Settings",
  children,
  description,
  pinnablePageID,
  title,
  width = "default",
}: SettingsPageProps) {
  const pinAction = pinnablePageID ? (
    <PinPageButton pageID={pinnablePageID} />
  ) : null;
  const headerActions =
    actions || backHref || pinAction ? (
      <>
        {actions}
        {pinAction}
        {backHref && <SettingsGhostLink label={backLabel} to={backHref} />}
      </>
    ) : undefined;

  return (
    <section
      className={`mx-auto flex w-full flex-1 flex-col gap-8 px-6 pb-12 pt-6 sm:px-8 sm:pt-7 lg:px-10 ${widthClasses[width]}`}
    >
      <PageHeader actions={headerActions}>
        <PageTitle>{title}</PageTitle>
        <PageDescription>{description}</PageDescription>
      </PageHeader>
      {children}
    </section>
  );
}

interface SettingsGhostLinkProps {
  label?: string;
  to: string;
}

export function SettingsGhostLink({
  label = "Settings",
  to,
}: SettingsGhostLinkProps) {
  return (
    <Link
      className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 bg-surface-container-lowest px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:border-primary/20 hover:bg-surface-container-low hover:text-on-surface"
      to={to}
    >
      <Icon className="text-base" name="arrow_back" />
      {label}
    </Link>
  );
}
