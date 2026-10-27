import { isMyGo } from "mygo-runtime";
import { fetch as goFetch } from "@mygo-plugins/fetch";
import { apiUrl, DEFAULT_SERVER, normalizeServer } from "./server.ts";

/**
 * Whether the app runs in the desktop shell.
 *
 * `isMyGo()` is false in a browser, which is what the server deployment sees.
 * In the desktop app every request to the review server leaves from Go (the
 * MyGo fetch plugin), so it carries the session cookie of the previous
 * response, is not subject to CORS, and all of its response headers are
 * readable — the four properties the Tauri HTTP plugin used to provide.
 */
export const desktop = isMyGo();
const serverKey = "finesub-review.server";
let server = desktop ? savedServer() : "";

export function savedServer(): string {
  return localStorage.getItem(serverKey) || DEFAULT_SERVER;
}

export function selectServer(value: string): void {
  server = normalizeServer(value);
  if (savedServer() !== server) location.hash = "";
  localStorage.setItem(serverKey, server);
}

export function scopedDraftKey(key: string): string {
  return desktop ? `${server}:${key}` : key;
}

/** Request options shared by the app and the connection probe. */
function desktopOptions(seconds: number, options: RequestInit = {}): RequestInit {
  return {
    ...options,
    // Cookies are those of the Go client's jar; the plugin ignores
    // `credentials`, and the Go side sends the stored session cookie.
    redirect: "manual",
    signal: options.signal || AbortSignal.timeout(seconds * 1000),
  };
}

export async function request(path: string, options: RequestInit = {}): Promise<Response> {
  if (!desktop) return fetch("/api" + path, { ...options, credentials: "same-origin" });
  try {
    return await goFetch(apiUrl(server, path), desktopOptions(120, options));
  } catch {
    throw new Error("无法连接审核服务器，请检查网络、服务器地址和 HTTPS 证书");
  }
}

export async function probeServer(value: string): Promise<void> {
  const response = await goFetch(apiUrl(value, "/bootstrap"), desktopOptions(15));
  if (!response.ok) throw new Error(`连接失败（HTTP ${response.status}）`);
  const body = await response.json();
  if (typeof body.initialized !== "boolean" || typeof body.access_configured !== "boolean")
    throw new Error("该地址不是兼容的 FineSub 审核服务");
}
