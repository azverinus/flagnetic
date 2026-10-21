import type { Problem, Violation } from "@flagnetic/api";

/**
 * A refusal of the management API, or a request that never reached it
 * (status 0, code `unreachable`). The console branches on `code` and
 * translates it; `detail` is the server's English text, shown only for a
 * code the console does not know.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly detail: string;
  readonly requestId: string;
  /** The problem document as the server sent it, extension members included. */
  readonly problem: Partial<Problem> & Record<string, unknown>;

  constructor(status: number, problem: Partial<Problem> & Record<string, unknown>) {
    const code = typeof problem.code === "string" ? problem.code : statusCode(status);
    const detail = typeof problem.detail === "string" ? problem.detail : "";
    super(detail || code);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.detail = detail;
    this.requestId = typeof problem.request_id === "string" ? problem.request_id : "";
    this.problem = problem;
  }

  /** Seconds the server asked the caller to wait, when it named them. */
  get retryAfter(): number | undefined {
    const value = this.problem.retry_after;
    return typeof value === "number" && value >= 0 ? value : undefined;
  }

  /** The field violations of an invalid request. */
  get violations(): Violation[] {
    return Array.isArray(this.problem.errors) ? this.problem.errors : [];
  }
}

/** The code of a refusal whose body was not a problem document. */
function statusCode(status: number): string {
  if (status === 0) return "unreachable";
  if (status === 401) return "unauthenticated";
  if (status === 403) return "forbidden";
  if (status === 404) return "not_found";
  if (status >= 500) return "internal";
  return `http_${status}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Reads the refusal in a response without consuming the caller's body. */
export async function errorOf(response: Response): Promise<ApiError> {
  let body: unknown;
  try {
    body = await response.clone().json();
  } catch {
    body = undefined;
  }
  return new ApiError(response.status, isRecord(body) ? body : {});
}

/** What a generated operation resolves to. */
type Result<T> = {
  data?: T;
  error?: unknown;
  response?: Response;
};

/**
 * Turns a generated operation's result into its data, throwing the
 * server's refusal as an ApiError. Queries render it inline with retry;
 * mutations toast it.
 */
export async function unwrap<T>(result: Promise<Result<T>>): Promise<T> {
  const { data, error, response } = await result;
  if (response && !response.ok) {
    throw new ApiError(response.status, isRecord(error) ? error : {});
  }
  if (response === undefined) {
    // The request never reached the server.
    throw new ApiError(0, { detail: error instanceof Error ? error.message : "" });
  }
  return data as T;
}
