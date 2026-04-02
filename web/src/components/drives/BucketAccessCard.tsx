import { Link } from "react-router";
import { STORAGE_EVENT_TYPES } from "../../lib/events";
import { skyfs } from "../../lib/rpc";
import { useRPC } from "../../lib/useRPC";
import { Icon } from "../Icon";
import { StatusBadge } from "../StatusBadge";

function displayPrefix(prefix: string) {
  return prefix.replace(/\/+$/, "").split("/").filter(Boolean).pop() ?? "Bucket";
}

export function BucketAccessCard() {
  const { data, error, loading, refreshing } = useRPC(
    () => skyfs.s3List({ prefix: "" }),
    [],
    {
      live: STORAGE_EVENT_TYPES,
      refreshIntervalMs: 15_000,
    }
  );

  const dirs = data?.dirs ?? [];
  const files = data?.files ?? [];
  const previewPrefixes = dirs.slice(0, 5);

  return (
    <section className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest/90 p-5 shadow-sm">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-2">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-surface-container-high text-on-surface-variant">
              <Icon className="text-lg" name="deployed_code" />
            </div>
            <div>
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Raw Storage
              </p>
              <h2 className="text-sm font-semibold text-on-surface">
                Bucket Inspector
              </h2>
            </div>
          </div>
          <p className="max-w-2xl text-sm text-secondary">
            Need to inspect the underlying S3 layout? Browse raw prefixes and
            object keys from here without promoting it to a full sidebar
            destination.
          </p>
        </div>

        <div className="flex items-center gap-3">
          <StatusBadge icon="deployed_code" tone="neutral">
            {refreshing ? "Refreshing" : "Subtle raw view"}
          </StatusBadge>
          <Link
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 bg-surface-container-high px-4 py-2 text-sm font-medium text-on-surface transition-colors hover:border-primary/20 hover:bg-surface-container-highest"
            to="/bucket"
          >
            <Icon name="arrow_forward" />
            Open Bucket
          </Link>
        </div>
      </div>

      <div className="mt-5 grid gap-4 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
        <div className="space-y-3">
          <div className="flex flex-wrap gap-2">
            {loading &&
              [1, 2, 3, 4].map((index) => (
                <div
                  className="h-8 w-24 animate-pulse rounded-full bg-surface-container-high"
                  key={index}
                />
              ))}

            {!loading &&
              previewPrefixes.map((prefix) => (
                <span
                  className="rounded-full bg-surface-container-high px-3 py-1.5 text-xs font-medium text-on-surface-variant"
                  key={prefix}
                >
                  {displayPrefix(prefix)}
                </span>
              ))}

            {!loading && previewPrefixes.length === 0 && !error && (
              <span className="rounded-full bg-surface-container-high px-3 py-1.5 text-xs font-medium text-on-surface-variant">
                Bucket is empty
              </span>
            )}
          </div>

          {error ? (
            <p className="text-sm text-error">{error}</p>
          ) : (
            <p className="text-xs text-secondary">
              {data?.total ?? 0} object{(data?.total ?? 0) === 1 ? "" : "s"} in
              the bucket, {dirs.length} top-level prefix
              {dirs.length === 1 ? "" : "es"}, {files.length} direct root file
              {files.length === 1 ? "" : "s"}.
            </p>
          )}
        </div>

        {!loading && dirs.length > previewPrefixes.length && !error && (
          <p className="text-xs text-outline">
            +{dirs.length - previewPrefixes.length} more prefixes
          </p>
        )}
      </div>
    </section>
  );
}
