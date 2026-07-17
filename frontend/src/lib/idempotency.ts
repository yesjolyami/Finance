export type UUIDFactory = () => string;

export function secureUUID(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export class IdempotencyKeyManager {
  private payload: string | null = null;
  private key: string | null = null;

  public constructor(private readonly createUUID: UUIDFactory = secureUUID) {}

  public forPayload(payload: string): string {
    const normalized = payload.trim();
    if (this.payload !== normalized || !this.key) {
      this.payload = normalized;
      this.key = this.createUUID();
    }
    return this.key;
  }

  public succeeded(): void {
    this.payload = null;
    this.key = null;
  }
}
