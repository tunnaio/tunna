// Builds and spawns cmd/tunna-fixtures for the conformance test (ADR-0010).
// The binary prints its URL as the first stdout line once listening;
// POST /reset restores the fixture state between cases.

import { spawn, spawnSync, type Subprocess } from "bun";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = fileURLToPath(new URL("../../../", import.meta.url));

export class FixtureServer {
  readonly url: string;
  readonly #proc: Subprocess<"ignore", "pipe", "inherit">;

  private constructor(url: string, proc: Subprocess<"ignore", "pipe", "inherit">) {
    this.url = url;
    this.#proc = proc;
  }

  static async start(): Promise<FixtureServer> {
    const dir = mkdtempSync(join(tmpdir(), "tunna-fixtures-"));
    const bin = join(dir, process.platform === "win32" ? "tunna-fixtures.exe" : "tunna-fixtures");
    const build = spawnSync(["go", "build", "-o", bin, "./cmd/tunna-fixtures"], { cwd: repoRoot, stdout: "inherit", stderr: "inherit" });
    if (build.exitCode !== 0) throw new Error(`go build cmd/tunna-fixtures failed with exit code ${build.exitCode}`);

    const proc = spawn([bin, "-fixtures", join(repoRoot, "spec", "conformance", "fixtures.json")], {
      cwd: repoRoot,
      stdin: "ignore",
      stdout: "pipe",
      stderr: "inherit",
    });
    const url = (await firstLine(proc.stdout)).trim();
    if (!url.startsWith("http://")) {
      proc.kill();
      throw new Error(`tunna-fixtures printed ${JSON.stringify(url)}, expected its URL`);
    }
    return new FixtureServer(url, proc);
  }

  async reset(): Promise<void> {
    const res = await fetch(`${this.url}/reset`, { method: "POST" });
    if (res.status !== 204) throw new Error(`POST /reset answered ${res.status}`);
  }

  stop(): void {
    this.#proc.kill();
  }
}

async function firstLine(stream: ReadableStream<Uint8Array>): Promise<string> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let text = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return text;
    text += decoder.decode(value, { stream: true });
    const nl = text.indexOf("\n");
    if (nl >= 0) {
      reader.releaseLock();
      return text.slice(0, nl);
    }
  }
}
