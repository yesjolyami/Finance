import { describe, expect, it } from "vitest";

import { IdempotencyKeyManager } from "./idempotency";

describe("IdempotencyKeyManager", () => {
  it("keeps a key for retries and rotates it after payload change or success", () => {
    let sequence = 0;
    const manager = new IdempotencyKeyManager(() => `key-${++sequence}`);

    expect(manager.forPayload(" Дом ")).toBe("key-1");
    expect(manager.forPayload("Дом")).toBe("key-1");
    expect(manager.forPayload("Семья")).toBe("key-2");
    manager.succeeded();
    expect(manager.forPayload("Семья")).toBe("key-3");
  });
});
