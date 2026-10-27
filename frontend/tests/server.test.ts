import { test } from "node:test";
import assert from "node:assert/strict";
import { normalizeServer, apiUrl, downloadFilename } from "../src/server.ts";

test("server origins: HTTPS and loopback-only HTTP", () => {
  assert.equal(normalizeServer(" https://review.example.com/ "), "https://review.example.com");
  assert.equal(normalizeServer("http://127.0.0.1:8765"), "http://127.0.0.1:8765");
  assert.equal(normalizeServer("http://localhost:8765"), "http://localhost:8765");
  for (const value of ["http://example.com", "file:///tmp", "javascript:alert(1)", "https://u:p@example.com", "https://example.com/api", "https://example.com?token=secret", "https://example.com/#x", "example.com"]) {
    assert.throws(() => normalizeServer(value), value);
  }
});

test("API paths cannot escape the configured server API", () => {
  assert.equal(apiUrl("https://example.com", "/entries?page=2"), "https://example.com/api/entries?page=2");
  for (const path of ["//evil.test", "/../admin", "/%2e%2e/admin", "/\\evil.test", "https://evil.test", "/entries#x"]) {
    assert.throws(() => apiUrl("https://example.com", path), path);
  }
});

test("download names support Chinese headers and strip unsafe path characters", () => {
  assert.equal(downloadFilename("attachment; filename*=UTF-8''%E5%AE%A1%E6%A0%B8.zip", "x.zip"), "审核.zip");
  assert.equal(downloadFilename('attachment; filename="../../test.zip"', "x.zip"), ".._.._test.zip");
  assert.equal(downloadFilename("attachment; filename*=UTF-8''%ZZ", "x.zip"), "x.zip");
});
