/**
 * Validates the presigned upload target the service hands back before the
 * extension PUTs user media to it. A compromised or misconfigured service
 * must not be able to aim the browser at an arbitrary URL (an intranet host,
 * a plain-http endpoint, a credentialed URL) or smuggle extra request headers.
 */
import { ApiClientError } from "./errors.js";

/** Headers an S3-compatible presigned PUT legitimately signs. `host` and
 * `content-length` are set by the browser itself and dropped from the request. */
const ALLOWED_HEADERS = new Set([
  "content-type",
  "content-length",
  "content-md5",
  "content-disposition",
  "content-encoding",
  "cache-control",
  "expires",
  "host",
]);
const BROWSER_MANAGED = new Set(["host", "content-length"]);

function isIpLiteral(hostname: string): boolean {
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(hostname) || hostname.includes(":") || hostname.startsWith("[");
}

function reject(): never {
  throw new ApiClientError({ code: "invalid_upload_target", message: "The upload address was not valid." });
}

export interface PresignedTarget {
  url: string;
  headers: Record<string, string>;
}

export function validatePresignedUpload(
  rawUrl: unknown,
  rawHeaders: Record<string, string> | undefined,
  serviceOrigin: string,
): PresignedTarget {
  if (typeof rawUrl !== "string") return reject();
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return reject();
  }
  if (url.username || url.password || url.hash) return reject();

  const sameOriginAsService = url.origin === serviceOrigin;
  // A loopback service (local development, e2e) legitimately fronts a local
  // object store on another port; the same dev allowance as the service URL.
  let devService = false;
  try {
    const service = new URL(serviceOrigin);
    devService = service.hostname === "localhost" || service.hostname === "127.0.0.1";
  } catch {
    /* an unparsable service origin gets no relaxation */
  }
  if (devService) {
    if (url.protocol !== "https:" && url.protocol !== "http:") return reject();
  } else if (!sameOriginAsService) {
    // Third-party storage must be https on a real hostname: no http, no IP
    // literals, no localhost/internal names.
    if (url.protocol !== "https:") return reject();
    const host = url.hostname.toLowerCase();
    if (isIpLiteral(host) || host === "localhost" || host.endsWith(".localhost") || host.endsWith(".local") || host.endsWith(".internal") || !host.includes(".")) {
      return reject();
    }
  } else if (url.protocol !== "https:" && url.protocol !== "http:") {
    return reject();
  }

  const headers: Record<string, string> = {};
  for (const [name, value] of Object.entries(rawHeaders ?? {})) {
    const lower = name.toLowerCase();
    if (typeof value !== "string" || /[\r\n]/.test(value)) return reject();
    if (!ALLOWED_HEADERS.has(lower) && !lower.startsWith("x-amz-")) return reject();
    if (BROWSER_MANAGED.has(lower)) continue;
    headers[name] = value;
  }
  return { url: url.toString(), headers };
}
