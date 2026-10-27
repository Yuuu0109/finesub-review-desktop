import { test } from "node:test";
import assert from "node:assert/strict";
import { nextParams, queueParams, toggleGroup, readDraft, writeDraft, removeDraft } from "../src/workflow.ts";

test("review navigation preserves scope and uses a revision cursor, never the old page offset", () => {
  const query = "q=美香&category=streamer&status=pending&batch_id=batch&mine=true&reviewed=no&page=7&limit=100&after=old";
  const params = nextParams(query, "current");
  assert.equal(params.get("q"), "美香");
  assert.equal(params.get("mine"), "true");
  assert.equal(params.get("reviewed"), "no");
  assert.equal(params.get("batch_id"), "batch");
  assert.equal(params.has("page"), false);
  assert.equal(params.get("limit"), "1");
  assert.equal(params.get("after"), "current");
  assert.equal(nextParams("", "id").get("status"), "pending");
  assert.equal(queueParams("group_id=g&revision_id=other").toString(), "group_id=g");
});

test("related items select and deselect together, independent items stay untouched", () => {
  const a = { id: "a", group_id: "linked" }, b = { id: "b", group_id: "linked" }, c = { id: "c" };
  const items = [a, b, c];
  assert.deepEqual(toggleGroup(items, ["c"], a, true), ["c", "a", "b"]);
  assert.deepEqual(toggleGroup(items, ["a", "b", "c"], b, false), ["c"]);
  assert.deepEqual(toggleGroup(items, ["a", "b"], c, false), ["a", "b"]);
});

test("drafts survive a cleared session and malformed drafts do not break the page", () => {
  const local = new Map<string, string>(), session = new Map<string, string>();
  const storage = (m: Map<string, string>) => ({ getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => m.set(k, v), removeItem: (k: string) => m.delete(k) });
  Object.assign(globalThis, { localStorage: storage(local), sessionStorage: storage(session) });
  session.set("draft", JSON.stringify({ body: "历史草稿" }));
  assert.deepEqual(readDraft("draft"), { body: "历史草稿" });
  assert.equal(writeDraft("draft", { body: "新版草稿" }), true);
  session.clear();
  assert.deepEqual(readDraft("draft"), { body: "新版草稿" });
  local.set("broken", "{bad-json");
  assert.equal(readDraft("broken"), null);
  local.set("broken", "[]");
  assert.equal(readDraft("broken"), null);
  removeDraft("draft");
  assert.equal(readDraft("draft"), null);
  Object.assign(globalThis, { localStorage: { setItem: () => { throw new Error("quota"); } } });
  assert.equal(writeDraft("full", { content: "保留在编辑器" }), false);
});
