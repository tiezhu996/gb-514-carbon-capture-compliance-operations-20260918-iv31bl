
import type { ApiEnvelope, ReviewCheck, UserSession } from '../types/domain';

const TOKEN_KEY = 'domain-control-session';

// ApiError preserves the backend envelope code (e.g. "review_gate") and any
// structured data so the review closed loop can show gate reasons on the page
// instead of a generic HTTP message.
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly reviewCheck?: ReviewCheck;

  constructor(status: number, code: string, message: string, reviewCheck?: ReviewCheck) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.reviewCheck = reviewCheck;
  }
}

export function getToken(): string {
  try { return JSON.parse(localStorage.getItem(TOKEN_KEY) || '{}').token || ''; } catch { return ''; }
}
export function getStoredSession(): UserSession | null {
  try {
    const value = JSON.parse(localStorage.getItem(TOKEN_KEY) || 'null') as UserSession | null;
    return value?.token && value?.role ? value : null;
  } catch {
    return null;
  }
}
export function saveSession(session: unknown): void { localStorage.setItem(TOKEN_KEY, JSON.stringify(session)); }
export function clearSession(): void { localStorage.removeItem(TOKEN_KEY); }

export async function request<T>(path: string, init: RequestInit = {}): Promise<ApiEnvelope<T>> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.body) headers.set('Content-Type', 'application/json');
  const token = getToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(`/api${path}`, { ...init, headers });
  if (response.status === 204) return { data: undefined as T };
  const payload = await response.json().catch(() => ({ error: 'invalid_response', message: '服务返回了无法解析的响应' }));
  if (!response.ok) {
    const reviewCheck = payload?.data?.reviewCheck as ReviewCheck | undefined;
    throw new ApiError(response.status, payload.error || `HTTP ${response.status}`, payload.message || payload.error || `HTTP ${response.status}`, reviewCheck);
  }
  return payload as ApiEnvelope<T>;
}
