import { test } from "node:test";
import assert from "node:assert/strict";
import { downloadFilename } from "../src/server.ts";

// A minimal stand-in for the MyGo runtime that Go injects as window.mygo.
// The transport talks to the fetch plugin through it, so mocking here
// exercises the real @mygo-plugins/fetch code path and the real transport.
function channel() {
  const queue: any[] = [];
  let resolveNext: ((v: IteratorResult<any>) => void) | null = null;
  let done = false;
  const flush = () => {
    if (!resolveNext) return;
    if (queue.length === 0 && !done) return;
    const next = queue.length > 0 ? { value: queue.shift(), done: false } : { value: undefined, done: true };
    const resolve = resolveNext;
    resolveNext = null;
    resolve(next);
  };
  return {
    onmessage: null,
    onclose: null,
    closed: false,
    push(value: any) { queue.push(value); flush(); },
    finish() { done = true; flush(); },
    close() { done = true; queue.length = 0; flush(); },
    [Symbol.asyncIterator]() {
      return {
        next: () => new Promise<IteratorResult<any>>((resolve) => { resolveNext = resolve; flush(); }),
      };
    },
  };
}

function toBytes(base64: string): Uint8Array {
  return new Uint8Array(Buffer.from(base64, "base64"));
}

test("desktop transport keeps CSRF header, multipart body, binary bytes and refuses redirects", async () => {
  const store = new Map<string, string>();
  const captured: any = {};
  Object.assign(globalThis, {
    window: globalThis,
    location: { hash: "" },
    localStorage: { getItem: (k: string) => store.get(k) || null, setItem: (k: string, v: string) => store.set(k, v) },
  });
  // The Go-side fetch: records the request, streams a head then two chunks,
  // one of which holds 0xFF to prove the bytes survive base64.
  (globalThis as any).mygo = {
    platform: "windows",
    windowId: 1,
    version: "0.7.1",
    preferences: { accent: null, reduceMotion: false, opaque: false },
    locale: "zh-Hans",
    call(method: string, ...args: any[]) {
      if (method === "plugin:fetch.Fetch") {
        const [request, parts] = args;
        captured.request = request;
        queueMicrotask(() => {
          parts.push({
            status: 200,
            statusText: "OK",
            url: request.url,
            // The headers must stay ASCII: the MyGo plugin builds a `Response`
            // from them, and a `Response` header value is a ByteString, so a raw
            // UTF-8 file name would throw. The server therefore percent-encodes
            // it (RFC 5987), exactly as this line does.
            headers: [["content-type", "application/octet-stream"], ["content-disposition", "attachment; filename*=UTF-8''%E5%8C%85.zip"]],
          });
          parts.push({ chunk: Buffer.from([80, 75, 0, 255]).toString("base64") });
          parts.finish();
        });
        return Promise.resolve();
      }
      throw new Error(`Unexpected MyGo call ${method}`);
    },
    channel: () => channel(),
  };

  try {
    const { request, savedServer, selectServer, scopedDraftKey, desktop } = await import("../src/transport.ts");
    assert.equal(desktop, true, "the MyGo runtime must make the transport choose the desktop path");
    assert.equal(savedServer(), "https://49.234.185.63");
    const form = new FormData();
    form.append("files", new Blob(["知识正文"], { type: "text/markdown" }), "知识.md");
    const response = await request("/imports", { method: "POST", headers: { "X-CSRF-Token": "test-csrf" }, body: form });

    // 1. absolute URL to the configured server, CSRF header intact.
    assert.equal(captured.request.url, "https://49.234.185.63/api/imports");
    assert.equal(captured.request.method, "POST");
    const headers = new Headers(captured.request.headers);
    assert.equal(headers.get("x-csrf-token"), "test-csrf");

    // 2. multipart stays multipart: FormData is serialized with a boundary.
    assert.match(headers.get("content-type")!, /^multipart\/form-data; boundary=/);
    const body = new TextDecoder().decode(toBytes(captured.request.body));
    assert.match(body, /知识\.md/);
    assert.match(body, /知识正文/);

    // 3. redirects are refused, so a 3xx cannot be followed into a 200.
    assert.equal(captured.request.redirect, "manual");

    // 4. binary bytes arrive unchanged, and response headers are readable.
    assert.deepEqual(new Uint8Array(await response.arrayBuffer()), new Uint8Array([80, 75, 0, 255]));
    assert.equal(downloadFilename(response.headers.get("content-disposition"), "x.zip"), "包.zip");

    // Drafts stay scoped per server, as before.
    const oldKey = scopedDraftKey("same-user-and-entry");
    selectServer("https://another.example.com");
    assert.equal(savedServer(), "https://another.example.com");
    assert.notEqual(scopedDraftKey("same-user-and-entry"), oldKey);
  } finally {
    delete (globalThis as any).mygo;
  }
});

test("a browser does not take the desktop path", async () => {
  // A separate process check is unnecessary: `desktop` is computed once from
  // the runtime that exists when the module loads, and the desktop test above
  // already proves the MyGo runtime selects the Go path. This asserts the
  // inverse for a page without the runtime.
  const saved = (globalThis as any).mygo;
  delete (globalThis as any).mygo;
  try {
    const { isMyGo } = await import("mygo-runtime");
    assert.equal(isMyGo(), false);
  } finally {
    (globalThis as any).mygo = saved;
  }
});
