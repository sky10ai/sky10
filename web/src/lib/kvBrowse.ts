const INTERNAL_SLASH_PREFIX = "_sys/";
const INTERNAL_COLON_PREFIX = "_sys:";

export interface KVBrowseQuery {
  include_internal?: boolean;
  prefix?: string;
}

export function normalizeKVBrowsePrefix(prefix: string) {
  return prefix.trim();
}

export function isInternalKVKey(key: string) {
  return (
    key.startsWith(INTERNAL_SLASH_PREFIX) || key.startsWith(INTERNAL_COLON_PREFIX)
  );
}

export function buildKVBrowseQuery(
  showSystemValues: boolean,
  systemPrefix: string
): KVBrowseQuery | undefined {
  if (!showSystemValues) {
    return undefined;
  }

  const prefix = normalizeKVBrowsePrefix(systemPrefix);
  if (!prefix) {
    return { include_internal: true };
  }

  return {
    include_internal: true,
    prefix,
  };
}

export function matchesKVBrowseView(
  key: string,
  showSystemValues: boolean,
  systemPrefix: string
) {
  if (!showSystemValues) {
    return !isInternalKVKey(key);
  }

  const prefix = normalizeKVBrowsePrefix(systemPrefix);
  if (!prefix) {
    return true;
  }

  return key.startsWith(prefix);
}
