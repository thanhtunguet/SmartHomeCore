export type DeviceType = 'desktop' | 'server' | 'laptop' | 'tv' | 'nas' | 'iot' | 'router';

export type OperatingSystem = 'windows' | 'linux' | 'macos' | 'android' | 'freebsd' | 'embedded';

export type DeviceStatus = 'online' | 'offline' | 'unknown';

/** Derived by the Controller from the device's active command. */
export type PowerTransition = 'none' | 'waking' | 'shutting_down';

export interface DeviceMonitoring {
  enabled: boolean;
  port: number;
  protocolPreset: 'SSH' | 'RDP' | 'HTTP' | 'HTTPS' | 'CUSTOM';
  intervalSeconds: number;
  timeoutMs: number;
  lastLatencyMs?: number;
  lastCheckedAt?: string;
  consecutiveFailures?: number;
}

export interface DeviceWOL {
  /** True when the device has a MAC address (the API has no separate flag). */
  enabled: boolean;
  macAddress: string;
  /** Empty = the Controller derives the subnet's directed broadcast address. */
  broadcastAddress: string;
  port: number; // default 9
}

export interface DeviceShutdown {
  /** True when the device has an agent binding. */
  enabled: boolean;
  /** UUID of the Agent (its `agent_id` file). Commands are Ed25519-signed for this id. */
  agentId: string;
  agentPort: number;
  agentScheme: 'http' | 'https';
  /**
   * Reachability of the Agent. Taken from `agent.status` when the Controller reports it
   * (`agentStatusSource` = 'reported'); otherwise inferred from the device status ('inferred').
   */
  agentStatus: 'ready' | 'unreachable' | 'unknown' | 'not_configured';
  agentStatusSource: 'reported' | 'inferred';
  /** Agent version from the last successful contact (reported by the Controller only). */
  agentVersion?: string;
  /** Last successful contact with the Agent, RFC 3339 (reported by the Controller only). */
  agentLastOkAt?: string;
}

export interface Device {
  id: string;
  name: string;
  description: string;
  /** Stored by the Controller (`device_type`); icon and label only. */
  type: DeviceType;
  /** Stored by the Controller (`os`); informational. */
  os: OperatingSystem;
  ip: string;
  /**
   * Name stored for the device (`hostname`). Deprecated in the API: informational only, never used
   * to connect. Empty when none is stored.
   */
  hostname: string;
  mac: string;
  status: DeviceStatus;
  powerTransition: PowerTransition;
  /** Human readable ("3 min ago", "Never"). */
  lastSeen: string;
  monitoring: DeviceMonitoring;
  wol: DeviceWOL;
  shutdown: DeviceShutdown;
  createdAt: string;
}

export type DDNSState = 'synced' | 'error' | 'updating' | 'never_synced' | 'disabled';

/** One Cloudflare record kept up to date by the Controller (`/api/v1/ddns`). */
export interface DDNSConfig {
  id: string;
  name: string;
  zoneId: string;
  /** Cloudflare record id, chosen from the record lookup when the configuration was created. */
  recordId: string;
  recordName: string;
  recordType: 'A' | 'AAAA';
  proxied: boolean;
  /** 1 = automatic. */
  ttl: number;
  intervalSeconds: number;
  enabled: boolean;
  /** The token itself is never returned by the API. */
  hasApiToken: boolean;
  lastIp?: string;
  /** Public address found by the latest discovery run (absent until the Controller reports it). */
  detectedIp?: string;
  /** Content of the DNS record as last read from Cloudflare. */
  dnsValue?: string;
  lastCheckedAt?: string;
  lastSyncedAt?: string;
  lastError?: string;
  lastErrorAt?: string;
  status: DDNSState;
}

/** `wol` and `shutdown` are the server's `command` category split by action. */
export type ActivityCategory = 'device' | 'wol' | 'shutdown' | 'ddns' | 'auth' | 'user' | 'system';
export type ActivityOutcome = 'success' | 'warning' | 'error' | 'info';

export interface ActivityEvent {
  /** Stable: `act-<id>` (Controller feed), `cmd-<uuid>` (command history only), `local-...` (this tab). */
  id: string;
  /** Where the line comes from; only `local` ones can be cleared by the user. */
  source: 'server' | 'command' | 'local';
  timestamp: string;
  /** Who did it (username, `cli`, `system`); absent when unknown. */
  actor?: string;
  /** Set on wake/shutdown requests; the line then carries the command's outcome. */
  commandId?: string;
  category: ActivityCategory;
  deviceId?: string;
  deviceName?: string;
  title: string;
  description: string;
  outcome: ActivityOutcome;
  details?: {
    endpoint?: string;
    method?: string;
    requestPayload?: Record<string, unknown>;
    responseCode?: number;
    latencyMs?: number;
    rawOutput?: string;
  };
}

/** Subset the Controller reports (`GET /api/v1/system/metrics`); every figure may be missing. */
export interface SystemMetrics {
  collectedAt: string;
  platform: string;
  os: string;
  arch: string;
  hostname?: string;
  uptimeSeconds?: number;
  cpuPercent?: number;
  memoryUsedBytes?: number;
  memoryTotalBytes?: number;
  diskPath?: string;
  diskUsedBytes?: number;
  diskTotalBytes?: number;
  localIps: string[];
  localAddresses: { interfaceName?: string; ipAddress: string }[];
  cpuCores?: number;
  /** 1, 5 and 15 minute load averages. */
  loadAverage?: number[];
  kernel?: string;
  /** Metric names the platform does not expose (shown as an explanation instead of "—"). */
  restricted: string[];
  isAndroidHost: boolean;
  process: {
    pid?: number;
    uptimeSeconds?: number;
    rssBytes?: number;
    goroutines?: number;
    heapAllocBytes?: number;
  };
}

/** Browser-local preferences; Controller-wide OpenAPI settings are managed through the REST API. */
export interface AppSettings {
  monitoring: {
    defaultIntervalSec: number;
    defaultTimeoutMs: number;
  };
}

export type WakeFlowStep = 
  | 'idle' 
  | 'generating' 
  | 'transmitting' 
  | 'probing' 
  | 'online_confirmed' 
  | 'packet_sent_probing_timeout' 
  | 'failed';

export interface WakeFlowState {
  active: boolean;
  deviceId: string | null;
  deviceName: string;
  step: WakeFlowStep;
  message: string;
  progressPercent: number;
  logs: string[];
}

export type ShutdownFlowStep =
  | 'idle'
  | 'confirming'
  | 'connecting'
  | 'authenticating'
  | 'acknowledged'
  | 'waiting_offline'
  | 'offline_verified'
  | 'failed';

export interface ShutdownFlowState {
  active: boolean;
  deviceId: string | null;
  deviceName: string;
  step: ShutdownFlowStep;
  message: string;
  progressPercent: number;
  logs: string[];
}
