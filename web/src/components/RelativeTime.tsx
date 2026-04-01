import { useEffect, useState } from "react";
import { timeAgo } from "../lib/useRPC";

export function RelativeTime({
  className = "",
  value,
}: {
  className?: string;
  value: string;
}) {
  if (!value) {
    return <span className={className}>-</span>;
  }

  const [, setTick] = useState(0);

  useEffect(() => {
    const interval = window.setInterval(() => {
      setTick((current) => current + 1);
    }, 30_000);

    return () => window.clearInterval(interval);
  }, []);

  const timestamp = new Date(value);
  const title = Number.isNaN(timestamp.getTime())
    ? value
    : timestamp.toLocaleString();

  return (
    <time className={className} dateTime={value} title={title}>
      {timeAgo(value)}
    </time>
  );
}
