import { describe, expect, test } from "bun:test";
import { Tunna, TunnaError } from "../src/index.ts";
import { createXhrFetch } from "../src/xhr.ts";

// tunna/xhr: a fetch-shaped function for browsers that reports upload bytes
// (ADR-0010, "Upload progress in bytes"). Bun has no XMLHttpRequest, which
// suits the test: the adapter takes its XMLHttpRequest and its fetch as
// dependencies, and the fake below stands in for the browser's.

type Script = (xhr: FakeXHR) => void;

class FakeXHR {
  static instances: FakeXHR[] = [];
  static script: Script = (xhr) => xhr.respond(200, { "Content-Type": "application/json" }, "{}");

  method = "";
  url = "";
  requestHeaders: Record<string, string> = {};
  sent: unknown = undefined;
  aborted = false;
  responseType = "";
  withCredentials = false;
  status = 0;
  statusText = "";
  response: ArrayBuffer | null = null;
  #responseHeaders = "";

  upload: { onprogress: ((e: { loaded: number; total: number; lengthComputable: boolean }) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;

  constructor() {
    FakeXHR.instances.push(this);
  }
  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(name: string, value: string) {
    this.requestHeaders[name.toLowerCase()] = value;
  }
  getAllResponseHeaders() {
    return this.#responseHeaders;
  }
  send(body: unknown) {
    this.sent = body;
    queueMicrotask(() => FakeXHR.script(this));
  }
  abort() {
    this.aborted = true;
    this.onabort?.();
  }

  // --- what a test makes the "browser" do ---
  progress(loaded: number, total: number) {
    this.upload.onprogress?.({ loaded, total, lengthComputable: true });
  }
  respond(status: number, headers: Record<string, string>, body: string) {
    this.status = status;
    this.statusText = status === 200 ? "OK" : "";
    this.#responseHeaders = Object.entries(headers)
      .map(([k, v]) => `${k.toLowerCase()}: ${v}\r\n`)
      .join("");
    this.response = body === "" ? null : (new TextEncoder().encode(body).buffer as ArrayBuffer);
    this.onload?.();
  }
  fail() {
    this.onerror?.();
  }
}

function setup(script?: Script) {
  FakeXHR.instances = [];
  FakeXHR.script = script ?? ((xhr) => xhr.respond(200, { "Content-Type": "application/json" }, "{}"));
  const fetched: { url: string; init: RequestInit | undefined }[] = [];
  const inner = (async (input: string | URL | Request, init?: RequestInit) => {
    fetched.push({ url: String(input), init });
    return new Response(JSON.stringify({ via: "fetch" }), { status: 200, headers: { "Content-Type": "application/json" } });
  }) as typeof globalThis.fetch;
  const xhrFetch = createXhrFetch({ XMLHttpRequest: FakeXHR as unknown as typeof XMLHttpRequest, fetch: inner });
  return { xhrFetch, fetched };
}

describe("xhrFetch", () => {
  test("a request without a progress listener goes to the real fetch, untouched", async () => {
    // Downloads must keep streaming: XHR would buffer a whole object in memory.
    const { xhrFetch, fetched } = setup();
    const init = { method: "GET", headers: { Range: "bytes=0-9" } };
    const res = await xhrFetch("http://store.test/photos/a.jpg", init);
    expect(await res.json()).toEqual({ via: "fetch" });
    expect(fetched).toHaveLength(1);
    expect(fetched[0]!.init).toBe(init);
    expect(FakeXHR.instances).toHaveLength(0);
  });

  test("with a listener it sends through XMLHttpRequest and reports the bytes", async () => {
    const seen: [number, number][] = [];
    const { xhrFetch, fetched } = setup((xhr) => {
      xhr.progress(4, 10);
      xhr.progress(10, 10);
      xhr.respond(201, { "Content-Type": "application/json", ETag: '"abc"', "X-Tunna-Checksum": "crc32c=AAAAAA==" }, '{"ok":true}');
    });
    const body = new Uint8Array([1, 2, 3]);
    const res = await xhrFetch("http://store.test/b/k?x-tunna-sig=s", {
      method: "PUT",
      headers: new Headers({ "Content-Type": "text/plain", "X-Tunna-Checksum": "crc32c=AAAAAA==" }),
      body,
      onUploadProgress: (loaded: number, total: number) => seen.push([loaded, total]),
    } as RequestInit);

    expect(fetched).toHaveLength(0);
    const xhr = FakeXHR.instances[0]!;
    expect(xhr.method).toBe("PUT");
    expect(xhr.url).toBe("http://store.test/b/k?x-tunna-sig=s");
    expect(xhr.requestHeaders).toEqual({ "content-type": "text/plain", "x-tunna-checksum": "crc32c=AAAAAA==" });
    expect(xhr.sent).toBe(body);
    expect(xhr.withCredentials).toBe(false);
    expect(seen).toEqual([[4, 10], [10, 10]]);

    expect(res.status).toBe(201);
    expect(res.ok).toBe(true);
    expect(res.headers.get("ETag")).toBe('"abc"');
    expect(res.headers.get("X-Tunna-Checksum")).toBe("crc32c=AAAAAA==");
    expect(await res.json()).toEqual({ ok: true });
  });

  test("an error status resolves, like fetch: the pipeline reads the body", async () => {
    const { xhrFetch } = setup((xhr) => xhr.respond(422, { "Content-Type": "application/json", "X-Tunna-Error": "checksum_mismatch" }, '{"error":{"code":"checksum_mismatch","message":"no"}}'));
    const res = await xhrFetch("http://store.test/b/k", { method: "PUT", body: "x", onUploadProgress: () => {} } as RequestInit);
    expect(res.ok).toBe(false);
    expect(res.status).toBe(422);
    expect(res.headers.get("X-Tunna-Error")).toBe("checksum_mismatch");
    expect((await res.json()).error.code).toBe("checksum_mismatch");
  });

  test("a 204 has no body", async () => {
    const { xhrFetch } = setup((xhr) => xhr.respond(204, {}, ""));
    const res = await xhrFetch("http://store.test/b/k", { method: "PUT", body: "x", onUploadProgress: () => {} } as RequestInit);
    expect(res.status).toBe(204);
    expect(res.body).toBeNull();
  });

  test("a network failure rejects with a TypeError, like fetch", async () => {
    const { xhrFetch } = setup((xhr) => xhr.fail());
    const err = await xhrFetch("http://store.test/b/k", { method: "PUT", body: "x", onUploadProgress: () => {} } as RequestInit).catch((e) => e);
    expect(err).toBeInstanceOf(TypeError);
  });

  test("aborting the signal aborts the XHR and rejects with the signal's reason", async () => {
    const controller = new AbortController();
    const { xhrFetch } = setup((xhr) => {
      xhr.progress(1, 10);
      controller.abort();
    });
    const err = await xhrFetch("http://store.test/b/k", { method: "PUT", body: "x", signal: controller.signal, onUploadProgress: () => {} } as RequestInit).catch((e) => e);
    expect(FakeXHR.instances[0]!.aborted).toBe(true);
    expect(err).toBe(controller.signal.reason);
    expect(err.name).toBe("AbortError");
  });

  test("a signal that is already aborted never opens a request", async () => {
    const controller = new AbortController();
    controller.abort();
    const { xhrFetch } = setup();
    const err = await xhrFetch("http://store.test/b/k", { method: "PUT", body: "x", signal: controller.signal, onUploadProgress: () => {} } as RequestInit).catch((e) => e);
    expect(err.name).toBe("AbortError");
    expect(FakeXHR.instances).toHaveLength(0);
  });

  test("a stream body is refused: XMLHttpRequest cannot send one", async () => {
    const { xhrFetch } = setup();
    const err = await xhrFetch("http://store.test/b/k", { method: "PUT", body: new ReadableStream(), onUploadProgress: () => {} } as RequestInit).catch((e) => e);
    expect(err).toBeInstanceOf(TypeError);
    expect(FakeXHR.instances).toHaveLength(0);
  });

  test("where there is no XMLHttpRequest it is plain fetch, listener or not", async () => {
    // Node and Bun: importing and using tunna/xhr there must be harmless.
    const fetched: string[] = [];
    const inner = (async (input: string | URL | Request) => {
      fetched.push(String(input));
      return new Response("{}", { status: 200 });
    }) as typeof globalThis.fetch;
    const xhrFetch = createXhrFetch({ XMLHttpRequest: undefined, fetch: inner });
    const res = await xhrFetch("http://store.test/b/k", { method: "PUT", body: "x", onUploadProgress: () => {} } as RequestInit);
    expect(res.status).toBe(200);
    expect(fetched).toEqual(["http://store.test/b/k"]);
  });

  test("the default export is usable without arguments and touches no global at import", async () => {
    const mod = await import("../src/xhr.ts");
    expect(typeof mod.xhrFetch).toBe("function");
    expect(typeof mod.createXhrFetch).toBe("function");
  });
});

describe("xhrFetch as the client's transport", () => {
  const key = { id: "tk_test", secret: "test-secret-not-real" };

  test("objects.put reports bytes, and a server error is still a TunnaError", async () => {
    const wire = { bucket: "b", key: "k", size: 3, content_type: "text/plain", checksum: "crc32c=AAAAAA==", created_at: 1 };
    let answer: Script = (xhr) => {
      xhr.progress(1, 3);
      xhr.progress(3, 3);
      xhr.respond(201, { "Content-Type": "application/json" }, JSON.stringify(wire));
    };
    const { xhrFetch } = setup((xhr) => answer(xhr));
    const tunna = new Tunna({ url: "http://store.test", key, fetch: xhrFetch });

    const progress: [number, number][] = [];
    const obj = await tunna.objects.put("b", "k", "abc", { onProgress: (sent, total) => progress.push([sent, total]) });
    expect(obj.size).toBe(3);
    expect(progress).toEqual([[1, 3], [3, 3]]);
    // Signed as usual: the adapter is only a transport.
    expect(FakeXHR.instances[0]!.requestHeaders["authorization"]).toStartWith("TUNNA1-HMAC-SHA256 ");

    answer = (xhr) => xhr.respond(403, { "Content-Type": "application/json", "X-Tunna-Error": "forbidden" }, '{"error":{"code":"forbidden","message":"no"}}');
    const err = await tunna.objects.put("b", "k", "abc", { onProgress: () => {} }).catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(err.code).toBe("forbidden");
  });
});
