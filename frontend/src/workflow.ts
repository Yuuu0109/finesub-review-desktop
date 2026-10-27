/** Share the exact list filters with the review page and its next-item request. */
const filters = ["q", "status", "category", "batch_id", "group_id", "mine", "reviewed", "page"];
export function queueParams(query = ""): URLSearchParams {
  const source = new URLSearchParams(query);
  const result = new URLSearchParams();
  for (const key of filters) {
    const value = source.get(key);
    if (value) result.set(key, value);
  }
  return result;
}

export function nextParams(query: string, revision: string): URLSearchParams {
  const params = queueParams(query || "status=pending");
  params.delete("page");
  params.set("after", revision);
  params.set("limit", "1");
  return params;
}

export function toggleGroup<T extends { id: string; group_id?: string }>(
  items: T[], selected: string[], item: T, checked: boolean,
): string[] {
  const group = items.filter((row) => row.id === item.id || (item.group_id && row.group_id === item.group_id));
  const ids = new Set(selected);
  for (const row of group) checked ? ids.add(row.id) : ids.delete(row.id);
  return [...ids];
}

/** Persistent, user/server-scoped drafts survive restarting a portable update. */
export function readDraft<T>(key: string): T | null {
  try {
    const value = localStorage.getItem(key) ?? sessionStorage.getItem(key);
    if (!value) return null;
    const parsed = JSON.parse(value);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
    return parsed as T;
  } catch { return null; }
}

export function writeDraft(key: string, value: unknown): boolean {
  try {
    localStorage.setItem(key, JSON.stringify(value));
    sessionStorage.removeItem(key);
    return true;
  } catch { return false; }
}

export function removeDraft(key: string): void {
  for (const storage of [localStorage, sessionStorage]) {
    try { storage.removeItem(key); } catch { /* blocked storage must not undo a saved review */ }
  }
}
