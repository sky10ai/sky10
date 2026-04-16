import type { ReactNode } from "react";
import { Icon } from "./Icon";

type StatusTone = "danger" | "live" | "neutral" | "processing" | "success";

const toneClasses: Record<StatusTone, string> = {
  danger: "bg-error-container/40 text-on-error-container",
  live: "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200",
  neutral: "bg-surface-container-high text-secondary",
  processing: "bg-primary/10 text-primary",
  success: "bg-primary-fixed/40 text-on-primary-fixed-variant dark:bg-primary/20 dark:text-primary-fixed",
};

interface StatusBadgeProps {
  children: ReactNode;
  className?: string;
  icon?: string;
  pulse?: boolean;
  tone?: StatusTone;
}

export function StatusBadge({
  children,
  className = "",
  icon,
  pulse = false,
  tone = "neutral",
}: StatusBadgeProps) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[10px] font-bold uppercase tracking-[0.16em] ${toneClasses[tone]} ${className}`}
    >
      {pulse && <span className="h-2 w-2 rounded-full bg-current animate-pulse" />}
      {icon && <Icon name={icon} className="text-[12px]" />}
      <span>{children}</span>
    </span>
  );
}
