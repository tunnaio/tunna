// Shared access to the spec files. Tests read them by relative path; the
// package does not ship them.

import { readFileSync } from "node:fs";

const specDir = new URL("../../../spec/", import.meta.url);

export function loadSpec<T>(relative: string): T {
  return JSON.parse(readFileSync(new URL(relative, specDir), "utf8")) as T;
}

export function specVersion(): string {
  return readFileSync(new URL("VERSION", specDir), "utf8").trim();
}

/** Bytes for a vector input given as text, hex, or a byte array. */
export function inputBytes(input: { text?: string; hex?: string; bytes?: number[] }): Uint8Array {
  if (input.text !== undefined) return new TextEncoder().encode(input.text);
  if (input.hex !== undefined) return Uint8Array.from(Buffer.from(input.hex, "hex"));
  if (input.bytes !== undefined) return Uint8Array.from(input.bytes);
  throw new Error("vector input has no text, hex or bytes");
}
