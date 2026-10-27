import { test } from "node:test";
import assert from "node:assert/strict";

// The browser branch of the transport only exists in a page without the MyGo
// runtime, and `desktop` is computed once when the module loads. Node's test
// runner gives every file its own process, so this file simply never defines
// `globalThis.mygo` — no module-cache tricks, and the real module is loaded.
test("browser transport stays same-origin and never touches the Go client", async () => {
  const store = new Map<string, string>();
  const calls: { url: string; options: RequestInit }[] = [];
  Object.assign(globalThis, {
    window: globalThis,
    location: { hash: "", origin: "https://review.example.com" },
    localStorage: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
    },
  });
  // Nothing may reach the Go fetch plugin: there is no runtime to talk to.
  const realFetch = globalThis.fetch;
  globalThis.fetch = (async (input: any, options: RequestInit = {}) => {
    calls.push({ url: String(input), options });
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  }) as typeof fetch;

  try {
    const { request, desktop, savedServer, scopedDraftKey, selectServer } = await import("../src/transport.ts");

    assert.equal(desktop, false, "a page without the MyGo runtime must not take the desktop path");

    const response = await request("/entries?limit=1", { method: "POST", headers: { "X-CSRF-Token": "t" } });
    assert.equal(response.status, 200);

    // A relative path keeps the app same-origin with the server that served it,
    // which is what makes the session cookie and the CSRF token work.
    assert.equal(calls.length, 1);
    assert.equal(calls[0]!.url, "/api/entries?limit=1", "browser requests must stay a relative /api path");
    assert.equal(calls[0]!.options.credentials, "same-origin");
    assert.equal(calls[0]!.options.method, "POST");
    assert.equal((calls[0]!.options.headers as Record<string, string>)["X-CSRF-Token"], "t");

    // No server switching in a browser: drafts are not scoped per server, and
    // selecting one only records the preference for the next desktop launch.
    assert.equal(savedServer(), "https://49.234.185.63");
    assert.equal(scopedDraftKey("draft-key"), "draft-key");
    selectServer("https://another.example.com");
    assert.equal(scopedDraftKey("draft-key"), "draft-key", "browser drafts must not gain a server prefix");
  } finally {
    globalThis.fetch = realFetch;
  }
});
