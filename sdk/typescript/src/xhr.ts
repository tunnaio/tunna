// tunna/xhr: upload progress in bytes for browsers (ADR-0010). fetch has no
// upload progress; XMLHttpRequest does. A request the pipeline marks with
// onUploadProgress goes out through XHR, every other one through fetch.

import type { Fetch } from "./types.ts";

/** What the adapter uses: the globals by default, fakes in tests. */
export interface XhrDeps {
  XMLHttpRequest?: typeof XMLHttpRequest | undefined;
  fetch?: Fetch;
}

const NULL_BODY_STATUSES = new Set([101, 103, 204, 205, 304]);

function parseXhrHeaders(raw: string): Headers {
  const headers = new Headers();
  for (const line of raw.split("\r\n")) {
    const i = line.indexOf(":");
    if (i === -1) continue;
    headers.append(line.slice(0, i).trim(), line.slice(i + 1).trim());
  }
  return headers;
}

/** Builds a fetch-shaped transport: a request with onUploadProgress goes through XMLHttpRequest and reports bytes; any other, or where there is no XMLHttpRequest, is handed to fetch untouched, so downloads still stream. */
export function createXhrFetch(deps?: XhrDeps): Fetch {
  return async function (input, init) {
    const XHR = deps?.XMLHttpRequest ?? globalThis.XMLHttpRequest;
    const inner = deps?.fetch ?? globalThis.fetch;
    const listener = init?.onUploadProgress;
    if (!listener || !XHR) return inner(input, init);
    if (init.body instanceof ReadableStream) {
      throw new TypeError("XHR does not support ReadableStream");
    }

    const signal = init.signal;
    signal?.throwIfAborted();

    const req = input instanceof Request ? input : undefined;
    const url = req?.url ?? String(input);
    const method = init.method ?? req?.method ?? "GET";
    const headers = new Headers(init.headers ?? req?.headers);
    // Stream case is handled above, everything else fetch accepts, XHR accepts too
    const body =
      init.body !== undefined
        ? (init.body as XMLHttpRequestBodyInit | null)
        : req?.body
          ? await req.arrayBuffer()
          : null;

    return new Promise<Response>((resolve, reject) => {
      const xhr = new XHR();
      const onAbort = () => xhr.abort();
      const cleanup = () => signal?.removeEventListener("abort", onAbort);

      xhr.upload.onprogress = ({ loaded, total }) => listener(loaded, total);
      xhr.onload = () => {
        cleanup();
        const body = NULL_BODY_STATUSES.has(xhr.status)
          ? null
          : (xhr.response as ArrayBuffer);
        resolve(
          new Response(body, {
            headers: parseXhrHeaders(xhr.getAllResponseHeaders()),
            status: xhr.status,
            statusText: xhr.statusText,
          }),
        );
      };
      xhr.onerror = () => {
        cleanup();
        reject(new TypeError("network request failed"));
      };
      xhr.onabort = () => {
        cleanup();
        reject(signal?.reason ?? new DOMException("Aborted", "AbortError"));
      };
      signal?.addEventListener("abort", onAbort, { once: true });

      xhr.open(method, url);
      xhr.responseType = "arraybuffer";
      xhr.withCredentials =
        (init.credentials ?? req?.credentials) === "include";
      headers.forEach((value, name) => xhr.setRequestHeader(name, value));
      xhr.send(body);
    });
  };
}

/** The adapter on the globals: new Tunna({ url, fetch: xhrFetch }). Looks XMLHttpRequest up per call, so importing this in Node is harmless. */
export const xhrFetch = createXhrFetch();
