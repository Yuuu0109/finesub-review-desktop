/** Return a same-origin API path without allowing traversal out of /api/. */
export function apiPath(path: string): string {
  if (!path.startsWith("/") || path.startsWith("//") || /[\\#\s]/.test(path))
    throw new Error("无效的 API 路径");
  const url = new URL("/api" + path, "https://review.invalid");
  if (!url.pathname.startsWith("/api/"))
    throw new Error("API 路径不能离开审核服务");
  return url.pathname + url.search;
}

