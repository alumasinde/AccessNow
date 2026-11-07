import createClient from 'openapi-fetch';
import type { paths } from './schema';

export const api = createClient<paths>({
  baseUrl: '',
  credentials: 'include'
});

type ErrorPayload = {
  detail?: string;
  title?: string;
  fields?: { field?: string; message?: string }[];
};

export function errorMessage(error: unknown, response?: Response): string {
  if (error && typeof error === 'object') {
    const body = error as ErrorPayload;
    if (body.fields?.length) {
      return body.fields.map((field) => field.message ?? field.field).filter(Boolean).join(' · ');
    }
    if (body.detail) return body.detail;
    if (body.title && body.title !== 'Error') return body.title;
  }
  if (response?.status === 401) return 'Your session has ended. Sign in again to continue.';
  if (response?.status === 403) return 'You do not have access to that action.';
  if (response?.status === 404) return 'That record is no longer available.';
  if (response?.status === 409) return 'The record changed while you were working. Refresh and try again.';
  if (response?.status === 429) return 'Too many attempts. Wait a moment and try again.';
  if (response && response.status >= 500) return 'The server could not complete that request.';
  return 'Something went wrong. Please try again.';
}

export function isUnauthorized(response?: Response): boolean {
  return response?.status === 401;
}
