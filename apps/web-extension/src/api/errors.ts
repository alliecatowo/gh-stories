import type { WireError } from "../messaging/types.js";

/** Every failure path — network, timeout, abort, HTTP, malformed body —
 * normalizes to this so callers never branch on exception shape. */
export class ApiClientError extends Error {
  readonly code: string;
  readonly field?: string;
  readonly retryAfterSeconds?: number;

  constructor(error: WireError) {
    super(error.message);
    this.name = "ApiClientError";
    this.code = error.code;
    this.field = error.field;
    this.retryAfterSeconds = error.retryAfterSeconds;
  }

  toWireError(): WireError {
    return { code: this.code, message: this.message, field: this.field, retryAfterSeconds: this.retryAfterSeconds };
  }

  static network(message = "Could not reach the Stories service."): ApiClientError {
    return new ApiClientError({ code: "network_error", message });
  }
  static timeout(): ApiClientError {
    return new ApiClientError({ code: "timeout", message: "The request timed out." });
  }
  static unauthenticated(): ApiClientError {
    return new ApiClientError({ code: "unauthenticated", message: "You're signed out." });
  }
  static malformed(): ApiClientError {
    return new ApiClientError({ code: "malformed_response", message: "The service returned an unexpected response." });
  }
}
