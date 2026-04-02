import { useDeferredValue, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { EmptyState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { StatusBadge } from "../components/StatusBadge";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { skyfs } from "../lib/rpc";
import { formatBytes, useRPC } from "../lib/useRPC";

function encodePathSegments(path: string) {
  return path
    .split("/")
    .filter(Boolean)
    .map((segment) => encodeURIComponent(segment))
    .join("/");
}

function trimTrailingSlash(value: string) {
  return value.replace(/\/+$/, "");
}

function displayName(key: string, prefix: string) {
  const trimmed = prefix && key.startsWith(prefix) ? key.slice(prefix.length) : key;
  return trimTrailingSlash(trimmed) || "Bucket";
}

function objectIcon(key: string) {
  if (key.endsWith(".enc")) return "lock";
  if (key.endsWith(".json")) return "data_object";
  if (key.endsWith(".pack")) return "archive";
  if (key.endsWith(".txt")) return "description";
  return "draft";
}

export default function Bucket() {
  const navigate = useNavigate();
  const { "*": splat } = useParams();
  const [query, setQuery] = useState("");
  const deferredQuery = useDeferredValue(query);

  const currentPath = splat?.replace(/^\/+|\/+$/g, "") ?? "";
  const prefix = currentPath ? `${currentPath}/` : "";

  const { data, error, loading, refreshing, refetch } = useRPC(
    () => skyfs.s3List({ prefix }),
    [prefix],
    {
      live: STORAGE_EVENT_TYPES,
      refreshIntervalMs: 15_000,
    }
  );

  const breadcrumbParts = currentPath.split("/").filter(Boolean);
  const normalizedQuery = deferredQuery.trim().toLowerCase();

  const filteredDirs = useMemo(() => {
    const dirs = data?.dirs ?? [];
    if (!normalizedQuery) return dirs;
    return dirs.filter((dir) =>
      displayName(dir, prefix).toLowerCase().includes(normalizedQuery)
    );
  }, [data?.dirs, normalizedQuery, prefix]);

  const filteredFiles = useMemo(() => {
    const files = data?.files ?? [];
    if (!normalizedQuery) return files;
    return files.filter((file) => {
      const name = displayName(file.key, prefix).toLowerCase();
      return (
        name.includes(normalizedQuery) ||
        file.key.toLowerCase().includes(normalizedQuery)
      );
    });
  }, [data?.files, normalizedQuery, prefix]);

  const navigateToPrefix = (nextPrefix: string) => {
    const path = trimTrailingSlash(nextPrefix);
    navigate(path ? `/bucket/${encodePathSegments(path)}` : "/bucket");
  };

  return (
    <section className="mx-auto w-full max-w-7xl space-y-6 p-10">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div className="space-y-1">
          <p className="text-[10px] font-bold uppercase tracking-[0.22em] text-outline">
            Raw Storage
          </p>
          <h1 className="text-2xl font-semibold text-on-surface">Bucket</h1>
          <p className="max-w-3xl text-sm text-secondary">
            A quieter view into the underlying S3 bucket, so we can inspect
            prefixes and object keys without turning it into another loud drive
            surface.
          </p>
        </div>
        <div className="flex items-center gap-3">
          {refreshing ? (
            <StatusBadge icon="sync" tone="neutral">
              Refreshing
            </StatusBadge>
          ) : (
            <StatusBadge tone="neutral">Raw View</StatusBadge>
          )}
          <button
            className="flex items-center gap-2 rounded-full border border-outline-variant/20 bg-surface-container-high px-4 py-2 text-sm font-medium text-on-surface transition-colors hover:border-primary/20 hover:bg-surface-container-highest"
            onClick={() => refetch({ background: true })}
            type="button"
          >
            <Icon name="refresh" />
            Refresh
          </button>
        </div>
      </div>

      <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-4 shadow-sm">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex flex-wrap items-center gap-2 text-sm text-secondary">
            <button
              className="font-medium text-primary transition-colors hover:text-primary-container"
              onClick={() => navigateToPrefix("")}
              type="button"
            >
              Bucket
            </button>
            {breadcrumbParts.map((part, index) => {
              const path = breadcrumbParts.slice(0, index + 1).join("/");
              return (
                <button
                  key={path}
                  className="flex items-center gap-2 transition-colors hover:text-primary"
                  onClick={() => navigateToPrefix(path)}
                  type="button"
                >
                  <span className="text-outline">/</span>
                  <span className="font-medium text-on-surface">{part}</span>
                </button>
              );
            })}
          </div>

          <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
            <span className="text-xs font-medium text-secondary">
              {data?.total ?? 0} object{(data?.total ?? 0) === 1 ? "" : "s"}
            </span>
            <label className="relative block">
              <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-outline">
                <Icon className="text-base" name="search" />
              </span>
              <input
                className="w-full rounded-full border border-outline-variant/20 bg-surface-container-low px-4 py-2 pl-10 text-sm text-on-surface outline-none transition-colors focus:border-primary sm:w-72"
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search prefixes or objects"
                value={query}
              />
            </label>
          </div>
        </div>
      </div>

      {error && (
        <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
          {error}
        </div>
      )}

      {loading && (
        <div className="grid gap-6 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
          {[1, 2].map((index) => (
            <div
              key={index}
              className="h-[360px] animate-pulse rounded-2xl bg-surface-container-lowest"
            />
          ))}
        </div>
      )}

      {!loading && filteredDirs.length === 0 && filteredFiles.length === 0 ? (
        <EmptyState
          description={
            normalizedQuery
              ? "Nothing in this bucket view matches the current search."
              : prefix
                ? `No objects were found under ${prefix}.`
                : "No objects are present in the bucket yet."
          }
          icon="deployed_code"
          title={normalizedQuery ? "No matches" : "Bucket is empty"}
        />
      ) : (
        <div className="grid gap-6 lg:grid-cols-[minmax(0,0.85fr)_minmax(0,1.15fr)]">
          <section className="overflow-hidden rounded-2xl border border-outline-variant/10 bg-surface-container-lowest shadow-sm">
            <div className="flex items-center justify-between border-b border-outline-variant/10 px-5 py-4">
              <div>
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Prefixes
                </p>
                <h2 className="mt-1 text-sm font-semibold text-on-surface">
                  Folder-like groups
                </h2>
              </div>
              <span className="text-xs text-secondary">
                {filteredDirs.length}
              </span>
            </div>
            {filteredDirs.length > 0 ? (
              <div className="p-2">
                {filteredDirs.map((dir) => (
                  <button
                    key={dir}
                    className="flex w-full items-center gap-3 rounded-xl px-4 py-3 text-left transition-colors hover:bg-primary/5"
                    onClick={() => navigateToPrefix(dir)}
                    type="button"
                  >
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-surface-container-high text-primary">
                      <Icon className="text-lg" name="folder" />
                    </div>
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium text-on-surface">
                        {displayName(dir, prefix)}
                      </p>
                      <p className="truncate font-mono text-[11px] text-outline">
                        {dir}
                      </p>
                    </div>
                  </button>
                ))}
              </div>
            ) : (
              <div className="px-5 py-8 text-sm text-secondary">
                No nested prefixes at this level.
              </div>
            )}
          </section>

          <section className="overflow-hidden rounded-2xl border border-outline-variant/10 bg-surface-container-lowest shadow-sm">
            <div className="flex items-center justify-between border-b border-outline-variant/10 px-5 py-4">
              <div>
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Objects
                </p>
                <h2 className="mt-1 text-sm font-semibold text-on-surface">
                  Direct files in this prefix
                </h2>
              </div>
              <span className="text-xs text-secondary">
                {filteredFiles.length}
              </span>
            </div>
            {filteredFiles.length > 0 ? (
              <div className="divide-y divide-outline-variant/10">
                {filteredFiles.map((file) => (
                  <div
                    key={file.key}
                    className="grid grid-cols-[minmax(0,1fr)_120px] items-center gap-4 px-5 py-4"
                  >
                    <div className="min-w-0">
                      <div className="flex items-center gap-3">
                        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-surface-container-high text-on-surface-variant">
                          <Icon className="text-lg" name={objectIcon(file.key)} />
                        </div>
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium text-on-surface">
                            {displayName(file.key, prefix)}
                          </p>
                          <p className="truncate font-mono text-[11px] text-outline">
                            {file.key}
                          </p>
                        </div>
                      </div>
                    </div>
                    <div className="text-right">
                      <p className="font-mono text-sm text-on-surface">
                        {formatBytes(file.size)}
                      </p>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="px-5 py-8 text-sm text-secondary">
                No direct objects in this prefix.
              </div>
            )}
          </section>
        </div>
      )}
    </section>
  );
}
