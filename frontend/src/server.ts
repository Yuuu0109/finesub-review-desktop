export const DEFAULT_SERVER = "https://49.234.185.63";

/** Desktop servers are origin-only; never accept credentials or URL paths. */
export function normalizeServer(value: string): string {
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    throw new Error("请输入完整服务器地址，例如 https://review.example.com");
  }
  const loopback = ["localhost", "127.0.0.1"].includes(url.hostname);
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback))
    throw new Error("服务器必须使用 HTTPS；本机调试可使用 HTTP 回环地址");
  if (url.username || url.password || url.pathname !== "/" || url.search || url.hash)
    throw new Error("只填写服务器域名和端口，不要附带账号、路径或参数");
  return url.origin;
}

export function apiUrl(server: string, path: string): string {
  if (!path.startsWith("/") || path.startsWith("//") || /[\\#\s]/.test(path))
    throw new Error("无效的 API 路径");
  const origin = normalizeServer(server);
  const url = new URL("/api" + path, origin);
  if (url.origin !== origin || !url.pathname.startsWith("/api/"))
    throw new Error("API 路径不能离开审核服务");
  return url.href;
}

export function downloadFilename(header: string | null, fallback: string): string {
  const encoded = header?.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
  const plain = header?.match(/filename="([^"]+)"/i)?.[1];
  let name = plain || fallback;
  if (encoded) {
    try { name = decodeURIComponent(encoded); } catch { /* use fallback */ }
  }
  return name.replace(/[<>:"/\\|?*\x00-\x1f]/g, "_").slice(0, 180) || "download";
}
