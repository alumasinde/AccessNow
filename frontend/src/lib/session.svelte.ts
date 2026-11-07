import { api, errorMessage, isUnauthorized } from '$lib/api/client';
import type { SessionPayload } from '$lib/api/types';

class SessionStore {
  current = $state<SessionPayload | null>(null);
  loading = $state(true);
  lastError = $state<string | null>(null);

  get user() {
    return this.current?.user ?? null;
  }

  get tenant() {
    return this.current?.tenant ?? null;
  }

  get site() {
    return this.current?.site ?? null;
  }

  get signedIn() {
    return this.current !== null;
  }

  get isSuperAdmin() {
    return this.current?.capabilities.super_admin ?? false;
  }

  get isReadOnly() {
    return this.current?.capabilities.read_only ?? false;
  }

  can(permission: string): boolean {
    if (!this.current) return false;
    if (this.current.capabilities.super_admin) return true;
    return this.current.permissions.includes(permission) || this.current.permissions.includes('*');
  }

  canAny(permissions: string[]): boolean {
    return permissions.some((permission) => this.can(permission));
  }

  moduleOn(key: string): boolean {
    return this.current?.modules[key] === true;
  }

  async refresh(): Promise<boolean> {
    const { data, error, response } = await api.GET('/api/v1/auth/me');
    if (response.ok && data) {
      this.current = data;
      this.lastError = null;
      this.loading = false;
      return true;
    }
    if (isUnauthorized(response)) {
      this.current = null;
      this.lastError = null;
      this.loading = false;
      return false;
    }
    this.current = null;
    this.lastError = errorMessage(error, response);
    this.loading = false;
    return false;
  }

  async login(tenant: string, identifier: string, password: string): Promise<string | null> {
    this.lastError = null;
    const { data, error, response } = await api.POST('/api/v1/auth/login', {
      body: { tenant, identifier, password }
    });
    if (!response.ok || !data) {
      return errorMessage(error, response);
    }
    const ok = await this.refresh();
    if (!ok) return 'Signed in, but the workspace could not be loaded.';
    return null;
  }

  async logout(): Promise<void> {
    await api.POST('/api/v1/auth/logout');
    this.current = null;
    this.lastError = null;
  }
}

export const session = new SessionStore();
