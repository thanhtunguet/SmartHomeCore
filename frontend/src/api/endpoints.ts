/**
 * One function per operationId of api/openapi.yaml, named exactly like the operation (the contract
 * test in `endpoints.contract.test.ts` checks method and path of every function against the spec).
 * There are no roles: every authenticated user may call every operation.
 */
import { request } from './client';
import type {
  AgentBinding,
  AgentInspectRequest,
  AgentInspection,
  ActivityCategory,
  AlertChannel,
  AlertChannelCreate,
  AlertChannelUpdate,
  AlertTestResult,
  ActivityItem,
  ApiCommand,
  ApiDdnsConfig,
  ApiDevice,
  ApiSystemMetrics,
  CloudflareLookup,
  CloudflareRecord,
  CloudflareZone,
  CommandStatus,
  ControllerKey,
  DdnsConfigCreate,
  DdnsConfigUpdate,
  DdnsSyncResult,
  DeviceCreate,
  DeviceUpdate,
  Health,
  SystemInfo,
  OpenAPISettings,
  Page,
  PortForward,
  PortForwardCreate,
  PortForwardUpdate,
  ProbeResult,
  ServiceKey,
  ServiceKeyCreate,
  ServiceKeyCreated,
  StatusChange,
  TokenPair,
  User,
} from './schema';

export type { AgentBinding };

const PAGE_LIMIT = 200; // spec maximum
const MAX_PAGES = 50; // safety net against a cursor loop

/**
 * Follow `next_cursor` until exhausted (or `max` items collected). `more` tells that the server
 * still had entries beyond what was returned (stopped at `max` or at the page cap).
 */
async function collectPage<T>(
  path: string,
  query: Record<string, string | number | undefined> = {},
  max = Infinity,
): Promise<{ items: T[]; more: boolean }> {
  const all: T[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < MAX_PAGES; i++) {
    const page = await request<Page<T>>(path, { query: { ...query, limit: PAGE_LIMIT, cursor } });
    all.push(...page.items);
    cursor = page.next_cursor;
    if (!cursor || all.length >= max) break;
  }
  return { items: all.slice(0, max), more: Boolean(cursor) || all.length > max };
}

const collect = async <T>(path: string, query: Record<string, string | number | undefined> = {}, max = Infinity): Promise<T[]> =>
  (await collectPage<T>(path, query, max)).items;

// health (public)
export const getHealthz = () => request<Health>('/healthz', { anonymous: true });

// auth
export const login = (username: string, password: string) =>
  request<TokenPair>('/api/v1/auth/login', { method: 'POST', body: { username, password }, anonymous: true });
export const logout = (refreshToken: string) =>
  request<void>('/api/v1/auth/logout', { method: 'POST', body: { refresh_token: refreshToken } });
export const getCurrentUser = () => request<User>('/api/v1/auth/me');

// service account keys (session token required; a key can never manage keys)
export const listServiceAccountKeys = () => request<{ items: ServiceKey[] }>('/api/v1/service-account-keys').then(r => r.items);
/** The response holds the secret `key` exactly once: show it, never store it. */
export const createServiceAccountKey = (body: ServiceKeyCreate) =>
  request<ServiceKeyCreated>('/api/v1/service-account-keys', { method: 'POST', body });
/** Idempotent: 204 for a key that is already revoked; 404 for an unknown key or another user's. */
export const revokeServiceAccountKey = (id: string) => request<void>(`/api/v1/service-account-keys/${id}`, { method: 'DELETE' });

// alert channels (session token required; a service key gets 403)
export const listAlertChannels = () => request<{ items: AlertChannel[] }>('/api/v1/alerts/channels').then(r => r.items);
export const getAlertChannel = (id: string) => request<AlertChannel>(`/api/v1/alerts/channels/${id}`);
export const createAlertChannel = (body: AlertChannelCreate) =>
  request<AlertChannel>('/api/v1/alerts/channels', { method: 'POST', body });
/** `settings` replaces all settings; `secret` is sent only to change it (the stored one is never returned). */
export const updateAlertChannel = (id: string, body: AlertChannelUpdate) =>
  request<AlertChannel>(`/api/v1/alerts/channels/${id}`, { method: 'PATCH', body });
export const deleteAlertChannel = (id: string) => request<void>(`/api/v1/alerts/channels/${id}`, { method: 'DELETE' });
/** Sends a test message now. A delivery failure is a normal 200 with `result: 'failed'` and the reason. */
export const testAlertChannel = (id: string) => request<AlertTestResult>(`/api/v1/alerts/channels/${id}/test`, { method: 'POST' });

// port forwarding rules (ADR-022; session token required)
export const listPortForwards = () => request<{ items: PortForward[] }>('/api/v1/port-forwards').then(r => r.items);
export const getPortForward = (id: string) => request<PortForward>(`/api/v1/port-forwards/${id}`);
export const createPortForward = (body: PortForwardCreate) =>
  request<PortForward>('/api/v1/port-forwards', { method: 'POST', body });
/** `enabled` is the on/off switch; any other change restarts a running rule. */
export const updatePortForward = (id: string, body: PortForwardUpdate) =>
  request<PortForward>(`/api/v1/port-forwards/${id}`, { method: 'PATCH', body });
export const deletePortForward = (id: string) => request<void>(`/api/v1/port-forwards/${id}`, { method: 'DELETE' });

// devices
export const listDevices = () => collect<ApiDevice>('/api/v1/devices');
export const createDevice = (body: DeviceCreate) => request<ApiDevice>('/api/v1/devices', { method: 'POST', body });
export const updateDevice = (id: string, body: DeviceUpdate) =>
  request<ApiDevice>(`/api/v1/devices/${id}`, { method: 'PATCH', body });
export const deleteDevice = (id: string) => request<void>(`/api/v1/devices/${id}`, { method: 'DELETE' });
/** Idempotent: 204 whether or not a binding existed. */
export const removeDeviceAgent = (id: string) => request<void>(`/api/v1/devices/${id}/agent`, { method: 'DELETE' });
export const wakeDevice = (id: string) => request<ApiCommand>(`/api/v1/devices/${id}/wake`, { method: 'POST' });
export const shutdownDevice = (id: string) => request<ApiCommand>(`/api/v1/devices/${id}/shutdown`, { method: 'POST' });
export const probeDevice = (id: string) => request<ProbeResult>(`/api/v1/devices/${id}/probe`, { method: 'POST' });
export const getDeviceHistory = (id: string, max = 50) => collect<StatusChange>(`/api/v1/devices/${id}/history`, {}, max);

// commands
export const listCommands = (opts: { deviceId?: string; status?: CommandStatus; max?: number } = {}) =>
  collect<ApiCommand>('/api/v1/commands', { device_id: opts.deviceId, status: opts.status }, opts.max ?? 100);
/**
 * Activity feed (audit log), newest first. `more` is true when older entries exist beyond `max`
 * (the UI then knows its window is truncated). Filters are the spec's `category` and `device_id`.
 */
export const listActivity = (opts: { category?: ActivityCategory; deviceId?: string; max?: number } = {}) =>
  collectPage<ActivityItem>('/api/v1/activity', { category: opts.category, device_id: opts.deviceId }, opts.max ?? 200);
export const getCommand = (id: string) => request<ApiCommand>(`/api/v1/commands/${id}`);

// system
export const getSystemMetrics = () => request<ApiSystemMetrics>('/api/v1/system/metrics');
export const getSystemInfo = () => request<SystemInfo>('/api/v1/system/info');
export const getOpenAPISettings = () => request<OpenAPISettings>('/api/v1/system/settings/openapi');
export const updateOpenAPISettings = (enabled: boolean) =>
  request<OpenAPISettings>('/api/v1/system/settings/openapi', { method: 'PUT', body: { enabled } });
export const getControllerKey = () => request<ControllerKey>('/api/v1/system/controller-key');

// agents
/** Ask the agent at an IP what it is (its id, version, trusted key ids); the Controller makes the call. */
export const inspectAgent = (body: AgentInspectRequest) => request<AgentInspection>('/api/v1/agents/inspect', { method: 'POST', body });

// ddns
export const listDdnsConfigs = () => request<{ items: ApiDdnsConfig[] }>('/api/v1/ddns').then(r => r.items);
export const createDdnsConfig = (body: DdnsConfigCreate) => request<ApiDdnsConfig>('/api/v1/ddns', { method: 'POST', body });
export const updateDdnsConfig = (id: string, body: DdnsConfigUpdate) =>
  request<ApiDdnsConfig>(`/api/v1/ddns/${id}`, { method: 'PATCH', body });
export const deleteDdnsConfig = (id: string) => request<void>(`/api/v1/ddns/${id}`, { method: 'DELETE' });
export const syncDdnsConfig = (id: string) => request<DdnsSyncResult>(`/api/v1/ddns/${id}/sync`, { method: 'POST' });
/**
 * Cloudflare lookups for the DDNS form's dropdowns. POST only so the token travels in the body;
 * they change nothing. The Controller answers 501 until the Cloudflare provider exists (Phase 4)
 * and 502 (`cloudflare_error`, message = Cloudflare's reason) when Cloudflare refuses the token.
 */
export const listCloudflareZones = (lookup: CloudflareLookup) =>
  request<{ items: CloudflareZone[] }>('/api/v1/ddns/cloudflare/zones', { method: 'POST', body: lookup }).then(r => r.items);
export const listCloudflareRecords = (zoneId: string, lookup: CloudflareLookup) =>
  request<{ items: CloudflareRecord[] }>(`/api/v1/ddns/cloudflare/zones/${encodeURIComponent(zoneId)}/records`, {
    method: 'POST',
    body: lookup,
  }).then(r => r.items);
