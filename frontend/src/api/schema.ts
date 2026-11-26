/**
 * Wire types of the HomeCore Controller REST API: friendly names for the types generated from
 * `api/openapi.yaml` (see `./generated/schema.d.ts`, `npm run gen:api`).
 *
 * The spec is the source of truth: do not hand-write wire types here. Field names stay exactly as
 * on the wire (snake_case); the UI model lives in `src/types` and is converted in `mappers.ts`.
 *
 * Responses use `Readable<T>` (drops `writeOnly` fields such as `api_token`); request bodies use
 * `Writable<T>` (drops `readOnly` fields such as `AgentBinding.status`, so a client cannot send them).
 */
import type { components, Readable, Writable } from './generated/schema';

type Schemas = components['schemas'];
type Res<K extends keyof Schemas> = Readable<Schemas[K]>;
type Req<K extends keyof Schemas> = Writable<Schemas[K]>;

export type ApiErrorBody = Schemas['Error'];
export type Health = Res<'Health'>;
export type Readiness = Res<'Readiness'>;

export type User = Res<'User'>;
export type LoginRequest = Req<'LoginRequest'>;
export type TokenPair = Res<'TokenPair'>;

/** Service account keys (ADR-018). `ServiceAccountKeyCreated` carries the secret `key`, once. */
export type ServiceKeyScope = Schemas['ServiceAccountKeyScope'];
export type ServiceKey = Res<'ServiceAccountKey'>;
export type ServiceKeyCreated = Res<'ServiceAccountKeyCreated'>;
export type ServiceKeyCreate = Req<'CreateServiceAccountKeyRequest'>;

/** Alert channels (ADR-021). The secret (`secret`, the bot token) is write-only: responses carry `has_secret`. */
export type AlertEvent = Schemas['AlertEvent'];
export type AlertChannelType = Schemas['AlertChannelType'];
export type AlertChannel = Res<'AlertChannel'>;
export type AlertChannelCreate = Req<'AlertChannelCreate'>;
export type AlertChannelUpdate = Req<'AlertChannelUpdate'>;
export type AlertTestResult = Res<'AlertTestResult'>;

/** Port forwarding rules (ADR-022). `status` is runtime state; `enabled` is the stored switch. */
export type PortForwardProtocol = Schemas['PortForwardProtocol'];
export type PortForwardStatus = Schemas['PortForwardStatus'];
export type PortForward = Res<'PortForward'>;
export type PortForwardCreate = Req<'PortForwardCreate'>;
export type PortForwardUpdate = Req<'PortForwardUpdate'>;

export type ApiDeviceStatus = Schemas['DeviceStatus'];
export type PowerTransition = Schemas['PowerTransition'];
export type DeviceType = Schemas['DeviceType'];
export type OperatingSystem = Schemas['OperatingSystem'];

/** Agent binding as returned by the API (with the read-only `status`, `version`, `last_ok_at`). */
export type AgentBinding = Res<'AgentBinding'>;
/** Agent binding as sent in a request body (`agent_id`, `port`, `scheme` only). */
export type AgentBindingInput = Req<'AgentBinding'>;
export type AgentStatus = NonNullable<AgentBinding['status']>;

export type Device = Res<'Device'>;
export type ApiDevice = Device;
export type DeviceCreate = Req<'DeviceCreate'>;
export type DeviceUpdate = Req<'DeviceUpdate'>;

/** A cursor page: every list in the API has this shape (`items`, optional `next_cursor`). */
export interface Page<T> {
  items: T[];
  next_cursor?: string;
}

export type StatusChange = Res<'StatusChange'>;
export type ProbeResult = Res<'ProbeResult'>;

export type CommandType = Schemas['CommandType'];
export type CommandStatus = Schemas['CommandStatus'];
export type Command = Res<'Command'>;
export type ApiCommand = Command;

export type ActivityCategory = Schemas['ActivityCategory'];
export type ActivityItem = Res<'ActivityItem'>;

export type SystemMetrics = Res<'SystemMetrics'>;
export type ApiSystemMetrics = SystemMetrics;
export type SystemInfo = Res<'SystemInfo'>;
export type OpenAPISettings = Res<'OpenAPISettings'>;
export type ControllerKey = Res<'ControllerKey'>;
export type AgentInspectRequest = Req<'AgentInspectRequest'>;
export type AgentInspection = Res<'AgentInspection'>;

export type DnsRecordType = Schemas['DnsRecordType'];
export type DdnsConfig = Res<'DdnsConfig'>;
export type ApiDdnsConfig = DdnsConfig;
export type DdnsConfigCreate = Req<'DdnsConfigCreate'>;
export type DdnsConfigUpdate = Req<'DdnsConfigUpdate'>;
export type DdnsSyncResult = Res<'DdnsSyncResult'>;

/** Exactly one of `api_token` (typed in the form, not saved yet) or `config_id` (stored token). */
export type CloudflareLookup =
  | { api_token: string; config_id?: never }
  | { config_id: string; api_token?: never };
export type CloudflareZone = Res<'CloudflareZone'>;
export type CloudflareRecord = Res<'CloudflareRecord'>;
