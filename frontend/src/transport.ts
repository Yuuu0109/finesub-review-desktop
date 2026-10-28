import { apiPath } from "./server.ts";

/** The web client always talks to the API that served its page. */
export async function request(path: string, options: RequestInit = {}): Promise<Response> {
  return fetch(apiPath(path), { ...options, credentials: "same-origin" });
}

