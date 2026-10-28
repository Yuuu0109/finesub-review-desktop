import { test } from "node:test";
import assert from "node:assert/strict";
import { request } from "../src/transport.ts";

test("browser requests retain sessions, CSRF and multipart data", async () => {
  const calls: { url: string; options: RequestInit }[] = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = (async (input: any, options: RequestInit = {}) => {
    calls.push({ url: String(input), options });
    return new Response(new Uint8Array([80, 75, 0, 255]), {
      status: 200,
      headers: { "Content-Disposition": "attachment; filename*=UTF-8''%E5%8C%85.zip" },
    });
  }) as typeof fetch;
  try {
    const body = new FormData();
    body.append("files", new Blob(["知识正文"]), "知识.md");
    const response = await request("/imports", {
      method: "POST", headers: { "X-CSRF-Token": "session-csrf" }, body,
    });
    assert.equal(calls[0]!.url, "/api/imports");
    assert.equal(calls[0]!.options.credentials, "same-origin");
    assert.equal(calls[0]!.options.method, "POST");
    assert.equal(new Headers(calls[0]!.options.headers).get("x-csrf-token"), "session-csrf");
    assert.equal(calls[0]!.options.body, body);
    assert.equal(new Headers(calls[0]!.options.headers).has("content-type"), false, "the browser must provide the multipart boundary");
    assert.deepEqual(new Uint8Array(await response.arrayBuffer()), new Uint8Array([80, 75, 0, 255]));
    assert.match(response.headers.get("Content-Disposition")!, /filename\*/);
  } finally { globalThis.fetch = realFetch; }
});

test("browser requests keep cancellation and reject non-API paths before sending", async () => {
  const realFetch = globalThis.fetch;
  const controller = new AbortController();
  let sent = 0;
  globalThis.fetch = (async (_input: any, options: RequestInit = {}) => {
    sent++;
    assert.equal(options.signal, controller.signal);
    return new Response("{}");
  }) as typeof fetch;
  try {
    await request("/entries", { signal: controller.signal });
    for (const path of ["/../admin", "https://another.example/api/users"]) {
      await assert.rejects(request(path));
    }
    assert.equal(sent, 1);
  } finally { globalThis.fetch = realFetch; }
});
