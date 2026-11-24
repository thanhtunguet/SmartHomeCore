import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, ReactNode } from 'react';
import {
  Device,
  DDNSConfig,
  ActivityEvent,
  SystemMetrics,
  AppSettings,
  WakeFlowState,
  ShutdownFlowState
} from '../types';
import * as api from '../api/endpoints';
import { ApiError } from '../api/client';
import type {
  ActivityItem,
  ApiCommand,
  ApiDdnsConfig,
  ApiDevice,
  ApiSystemMetrics,
  ControllerKey,
  Health
} from '../api/schema';
import { mergeActivity } from '../api/activity';
import { buildDdnsCreate, buildDdnsUpdate, commandToActivity, type DdnsDraft, needsAgentRemoval, toDDNSConfig, toDevice, toDeviceCreate, toDeviceUpdate, toSystemMetrics } from '../api/mappers';
import { migrateLegacyDeviceMeta } from '../api/deviceMeta';
import { clockTime } from '../api/format';
import { isTerminal, pollCommand as followCommand } from '../api/commandPoll';
import { usePolling } from '../hooks/usePolling';
import { usePowerCooldown } from '../hooks/usePowerCooldown';
import { useAuth } from './AuthContext';

type Tab = 'dashboard' | 'devices' | 'ddns' | 'portforward' | 'activity' | 'system' | 'settings';

export interface Toast {
  id: number;
  kind: 'error' | 'success' | 'info';
  message: string;
}

/** Fields of a DDNS form (see `DdnsDraft`); `api_token` is only sent when the user typed one. */
export type DDNSDraft = DdnsDraft;

interface AppContextType {
  // Session
  username: string;
  logout: () => Promise<void>;
  serverHealth: Health | null;
  /** Worst current API problem (unreachable, 501, ...) for the status banner. */
  connectionIssue: ApiError | null;
  devicesLoaded: boolean;
  toasts: Toast[];
  dismissToast: (id: number) => void;

  // Navigation
  activeTab: Tab;
  setActiveTab: (tab: Tab) => void;
  ambientMode: boolean;
  setAmbientMode: (val: boolean) => void;
  theme: 'dark' | 'light';
  toggleTheme: () => void;
  sidebarCollapsed: boolean;
  setSidebarCollapsed: (val: boolean | ((prev: boolean) => boolean)) => void;

  // Devices
  devices: Device[];
  selectedDevice: Device | null;
  setSelectedDevice: (device: Device | null) => void;
  deviceModalOpen: boolean;
  setDeviceModalOpen: (val: boolean) => void;
  editingDevice: Device | null;
  setEditingDevice: (device: Device | null) => void;
  /** Resolves to an error message, or null when saved (the modal is then closed). */
  saveDevice: (deviceData: Partial<Device>) => Promise<string | null>;
  deleteDevice: (id: string) => Promise<void>;
  probeDevice: (id: string) => Promise<boolean>;
  refreshDevices: () => Promise<void>;

  /**
   * True for one second after a wake/shutdown request for this device (the API allows one per
   * device per second): use it to disable that device's wake/shutdown buttons.
   */
  isPowerLocked: (deviceId: string) => boolean;

  // Asynchronous flows (driven by the Controller's command state machine)
  wakeFlow: WakeFlowState;
  startWakeFlow: (device: Device) => void;
  cancelWakeFlow: () => void;

  shutdownFlow: ShutdownFlowState;
  requestShutdownConfirmation: (device: Device) => void;
  confirmAndExecuteShutdown: () => void;
  cancelShutdownFlow: () => void;

  // DDNS
  ddnsConfigs: DDNSConfig[];
  /** The record shown on the dashboard / DDNS screen (selected, else the first). */
  ddnsConfig: DDNSConfig | null;
  setActiveDdnsId: (id: string | null) => void;
  triggerDDNSSync: (id?: string) => Promise<void>;
  saveDdns: (id: string | null, draft: DDNSDraft) => Promise<string | null>;
  deleteDdns: (id: string) => Promise<string | null>;

  // Activity: the Controller's activity feed + command outcomes + browser-local events
  activities: ActivityEvent[];
  /** The Controller's feed was cut at its page cap: older entries exist but are not loaded. */
  activityTruncated: boolean;
  /** Adds a browser-local event (things the Controller cannot know, e.g. a probe). */
  addActivity: (event: Omit<ActivityEvent, 'id' | 'timestamp' | 'source'>) => void;
  /** Clears only the browser-local events; the Controller's log cannot be cleared. */
  clearActivities: () => void;

  // System
  systemMetrics: SystemMetrics | null;
  refreshSystemMetrics: () => Promise<void>;
  controllerKey: ControllerKey | null;
  controllerKeyError: string | null;
  loadControllerKey: () => Promise<void>;

  // Browser-local preferences
  settings: AppSettings;
  updateSettings: (newSettings: Partial<AppSettings>) => void;

  // Quick stats
  onlineCount: number;
  totalDevices: number;
}

const AppContext = createContext<AppContextType | undefined>(undefined);

const STORAGE_KEYS = {
  SETTINGS: 'homecore_prefs_v1',
  THEME: 'homecore_theme_v1'
};

const DEFAULT_SETTINGS: AppSettings = {
  monitoring: { defaultIntervalSec: 30, defaultTimeoutMs: 2000 }
};

export function errMsg(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.notImplemented) return 'Not implemented by the Controller yet (501).';
    if (e.forbidden) return `The Controller refused this request (403): ${e.message}`;
    return e.message;
  }
  return e instanceof Error ? e.message : String(e);
}

const follow = (first: ApiCommand, isCurrent: () => boolean, onUpdate: (c: ApiCommand) => void) =>
  followCommand(first, isCurrent, onUpdate, { fetchCommand: api.getCommand });

const tooManyRequests = (deviceName: string) => `Too many requests for ${deviceName}, wait a second`;

const idleWake: WakeFlowState = { active: false, deviceId: null, deviceName: '', step: 'idle', message: '', progressPercent: 0, logs: [] };
const idleShutdown: ShutdownFlowState = { active: false, deviceId: null, deviceName: '', step: 'idle', message: '', progressPercent: 0, logs: [] };

export const AppProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const auth = useAuth();
  const powerCooldown = usePowerCooldown();

  // Navigation
  const [activeTab, setActiveTab] = useState<Tab>('dashboard');
  const [ambientMode, setAmbientMode] = useState<boolean>(false);
  // Below lg the sidebar overlaps the content, so start collapsed to the icon rail.
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(
    () => typeof window !== 'undefined' && !window.matchMedia('(min-width: 1024px)').matches,
  );

  // Theme
  const [theme, setTheme] = useState<'dark' | 'light'>(() => {
    try {
      const saved = localStorage.getItem(STORAGE_KEYS.THEME);
      if (saved === 'light' || saved === 'dark') return saved;
    } catch { /* storage unavailable */ }
    return 'dark'; // Default to dark for infrastructure / OLED screens
  });

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark');
    try { localStorage.setItem(STORAGE_KEYS.THEME, theme); } catch { /* ignore */ }
  }, [theme]);

  const toggleTheme = () => setTheme(prev => (prev === 'dark' ? 'light' : 'dark'));

  // ---- Toasts ----------------------------------------------------------------------------
  const [toasts, setToasts] = useState<Toast[]>([]);
  const toastId = useRef(0);
  const dismissToast = useCallback((id: number) => setToasts(t => t.filter(x => x.id !== id)), []);
  const pushToast = useCallback((kind: Toast['kind'], message: string) => {
    const id = ++toastId.current;
    setToasts(t => [...t.slice(-3), { id, kind, message }]);
    setTimeout(() => dismissToast(id), 6000);
  }, [dismissToast]);

  // ---- Server state ----------------------------------------------------------------------
  const [apiDevices, setApiDevices] = useState<ApiDevice[]>([]);
  const [devicesLoaded, setDevicesLoaded] = useState(false);
  const [latencies, setLatencies] = useState<Record<string, number>>({});
  const [commands, setCommands] = useState<ApiCommand[]>([]);
  const [apiActivity, setApiActivity] = useState<ActivityItem[]>([]);
  const [activityTruncated, setActivityTruncated] = useState(false);
  const [localEvents, setLocalEvents] = useState<ActivityEvent[]>([]);
  const [apiMetrics, setApiMetrics] = useState<ApiSystemMetrics | null>(null);
  const [serverHealth, setServerHealth] = useState<Health | null>(null);
  const [apiDdns, setApiDdns] = useState<ApiDdnsConfig[]>([]);
  const [ddnsBusy, setDdnsBusy] = useState<ReadonlySet<string>>(new Set());
  const [activeDdnsId, setActiveDdnsId] = useState<string | null>(null);
  const [controllerKey, setControllerKey] = useState<ControllerKey | null>(null);
  const [controllerKeyError, setControllerKeyError] = useState<string | null>(null);
  const [issues, setIssues] = useState<Record<string, ApiError | null>>({});

  const setIssue = useCallback((key: string, e: unknown) => {
    setIssues(prev => {
      const next = e instanceof ApiError ? e : e ? new ApiError(0, 'unknown', errMsg(e)) : null;
      return prev[key]?.message === next?.message && prev[key]?.status === next?.status ? prev : { ...prev, [key]: next };
    });
  }, []);

  const [settings, setSettings] = useState<AppSettings>(() => {
    try {
      const saved = localStorage.getItem(STORAGE_KEYS.SETTINGS);
      if (saved) return { ...DEFAULT_SETTINGS, ...JSON.parse(saved) };
    } catch { /* corrupted or unavailable */ }
    return DEFAULT_SETTINGS;
  });
  useEffect(() => {
    try { localStorage.setItem(STORAGE_KEYS.SETTINGS, JSON.stringify(settings)); } catch { /* ignore */ }
  }, [settings]);

  // Modal and selection state
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [deviceModalOpen, setDeviceModalOpen] = useState<boolean>(false);
  const [editingDevice, setEditingDevice] = useState<Device | null>(null);

  const devices = useMemo(
    () => apiDevices.map(d => toDevice(d, latencies[d.id])),
    [apiDevices, latencies]
  );
  const selectedDevice = useMemo(() => devices.find(d => d.id === selectedId) ?? null, [devices, selectedId]);
  const setSelectedDevice = useCallback((d: Device | null) => setSelectedId(d?.id ?? null), []);

  // ---- Polling (no push channel by design) -----------------------------------------------
  const refreshDevices = useCallback(async () => {
    try {
      setApiDevices(await api.listDevices());
      setDevicesLoaded(true);
      setIssue('devices', null);
    } catch (e) {
      setDevicesLoaded(true);
      setIssue('devices', e);
      throw e;
    }
  }, [setIssue]);

  // One-time carry-over of browser-local device type/OS to the Controller (GAP-API-01).
  const metaMigrated = useRef(false);
  useEffect(() => {
    if (!devicesLoaded || metaMigrated.current) return;
    metaMigrated.current = true;
    void migrateLegacyDeviceMeta(apiDevices, (id, meta) => api.updateDevice(id, meta)).then(moved => {
      if (moved > 0) void refreshDevices().catch(() => undefined);
    });
  }, [devicesLoaded, apiDevices, refreshDevices]);

  const refreshCommands = useCallback(async () => {
    try {
      setCommands(await api.listCommands({ max: 100 }));
      setIssue('commands', null);
    } catch (e) {
      setIssue('commands', e);
      throw e;
    }
  }, [setIssue]);

  const refreshActivity = useCallback(async () => {
    try {
      const { items, more } = await api.listActivity();
      setApiActivity(items);
      setActivityTruncated(more);
      setIssue('activity', null);
    } catch (e) {
      setIssue('activity', e); // keep the last feed
      throw e;
    }
  }, [setIssue]);

  /** Pull the feed (and commands) right after one of the user's own mutating actions. */
  const refreshAfterAction = useCallback(() => {
    void refreshActivity().catch(() => undefined);
    void refreshCommands().catch(() => undefined);
  }, [refreshActivity, refreshCommands]);

  const refreshSystemMetrics = useCallback(async () => {
    try {
      setApiMetrics(await api.getSystemMetrics());
      setIssue('metrics', null);
    } catch (e) {
      setIssue('metrics', e);
      throw e;
    }
  }, [setIssue]);

  const refreshDdns = useCallback(async () => {
    try {
      setApiDdns(await api.listDdnsConfigs());
      setIssue('ddns', null);
    } catch (e) {
      setIssue('ddns', e);
      throw e;
    }
  }, [setIssue]);

  const refreshHealth = useCallback(async () => {
    setServerHealth(await api.getHealthz());
  }, []);

  usePolling(refreshDevices, 10_000);
  usePolling(refreshCommands, 15_000);
  usePolling(refreshActivity, 15_000);
  usePolling(refreshSystemMetrics, 10_000);
  usePolling(refreshDdns, 30_000);
  usePolling(refreshHealth, 60_000);

  const loadControllerKey = useCallback(async () => {
    try {
      setControllerKey(await api.getControllerKey());
      setControllerKeyError(null);
    } catch (e) {
      setControllerKeyError(errMsg(e));
    }
  }, []);

  // ---- Activity --------------------------------------------------------------------------
  const addActivity = useCallback((event: Omit<ActivityEvent, 'id' | 'timestamp' | 'source'>) => {
    const e: ActivityEvent = {
      ...event,
      id: `local-${Date.now()}-${Math.floor(Math.random() * 1000)}`,
      source: 'local',
      timestamp: new Date().toISOString()
    };
    setLocalEvents(prev => [e, ...prev.slice(0, 99)]);
  }, []);

  const activities = useMemo(
    () =>
      mergeActivity({
        server: apiActivity,
        serverTruncated: activityTruncated,
        commands,
        local: localEvents,
        deviceNames: new Map(devices.map(d => [d.id, d.name]))
      }),
    [apiActivity, activityTruncated, commands, localEvents, devices]
  );

  const clearActivities = () => setLocalEvents([]);

  // ---- Device operations -----------------------------------------------------------------
  const saveDevice = async (deviceData: Partial<Device>): Promise<string | null> => {
    try {
      let saved: ApiDevice;
      if (editingDevice) {
        saved = await api.updateDevice(editingDevice.id, toDeviceUpdate(deviceData, editingDevice));
      } else {
        saved = await api.createDevice(toDeviceCreate(deviceData));
      }
      let unbindError: string | null = null;
      if (editingDevice && needsAgentRemoval(deviceData, editingDevice)) {
        try {
          await api.removeDeviceAgent(saved.id);
          saved = { ...saved, agent: undefined };
        } catch (e) {
          unbindError = errMsg(e);
        }
      }
      setApiDevices(prev => (prev.some(d => d.id === saved.id) ? prev.map(d => (d.id === saved.id ? saved : d)) : [...prev, saved]));
      refreshAfterAction(); // the Controller's feed records the change
      if (unbindError) return `Saved, but the agent binding could not be removed: ${unbindError}`;
      setDeviceModalOpen(false);
      setEditingDevice(null);
      return null;
    } catch (e) {
      return errMsg(e);
    }
  };

  const deleteDevice = async (id: string) => {
    const target = devices.find(d => d.id === id);
    if (!target) return;
    try {
      await api.deleteDevice(id);
    } catch (e) {
      pushToast('error', `Could not remove ${target.name}: ${errMsg(e)}`);
      return;
    }
    setApiDevices(prev => prev.filter(d => d.id !== id));
    if (selectedId === id) setSelectedId(null);
    refreshAfterAction();
  };

  const probeDevice = async (id: string): Promise<boolean> => {
    const target = devices.find(d => d.id === id);
    if (!target) return false;
    const started = performance.now();
    try {
      const r = await api.probeDevice(id);
      const latency = r.latency_ms ?? Math.round(performance.now() - started);
      setLatencies(prev => {
        const next = { ...prev };
        if (r.reachable) next[id] = latency; else delete next[id];
        return next;
      });
      setApiDevices(prev =>
        prev.map(d =>
          d.id === id
            ? { ...d, status: r.status, last_checked_at: r.checked_at, last_seen_at: r.reachable ? r.checked_at : d.last_seen_at }
            : d
        )
      );
      addActivity({
        category: 'device',
        deviceId: target.id,
        deviceName: target.name,
        title: r.reachable ? 'TCP Health Probe Passed' : 'TCP Health Probe Failed',
        description: r.reachable
          ? `Target ${target.ip}:${target.monitoring.port} (${target.monitoring.protocolPreset}) responded in ${latency}ms.`
          : `Target ${target.ip}:${target.monitoring.port} did not answer within ${target.monitoring.timeoutMs}ms.`,
        outcome: r.reachable ? 'success' : 'warning',
        details: {
          endpoint: `/api/v1/devices/${target.id}/probe`,
          method: 'POST',
          latencyMs: r.reachable ? latency : undefined,
          responseCode: 200,
          rawOutput: r.reachable ? `connected (${latency}ms)` : r.error ?? 'unreachable'
        }
      });
      return r.reachable;
    } catch (e) {
      pushToast('error', `Probe failed: ${errMsg(e)}`);
      return false;
    }
  };

  // ---- Wake-on-LAN flow: POST /wake, then follow the command until it is terminal ----------
  const [wakeFlow, setWakeFlow] = useState<WakeFlowState>(idleWake);
  const wakeRun = useRef(0);

  const startWakeFlow = async (device: Device) => {
    // At most one wake/shutdown request per device per second (the Controller answers 429 otherwise).
    if (!powerCooldown.tryAcquire(device.id)) {
      pushToast('info', tooManyRequests(device.name));
      return;
    }
    const run = ++wakeRun.current;
    const isCurrent = () => wakeRun.current === run;
    const log = (line: string) => setWakeFlow(prev => (isCurrent() ? { ...prev, logs: [...prev.logs, `[${clockTime()}] ${line}`] } : prev));
    const set = (patch: Partial<WakeFlowState>) => setWakeFlow(prev => (isCurrent() ? { ...prev, ...patch } : prev));

    setWakeFlow({
      active: true,
      deviceId: device.id,
      deviceName: device.name,
      step: 'generating',
      message: 'Asking the Controller to send a Wake-on-LAN packet...',
      progressPercent: 15,
      logs: [
        `[${clockTime()}] Target MAC: ${device.wol.macAddress || device.mac}`,
        `[${clockTime()}] Broadcast: ${device.wol.broadcastAddress || 'auto (subnet broadcast)'}:${device.wol.port}`
      ]
    });

    try {
      const first = await api.wakeDevice(device.id);
      log(`POST /devices/${device.id}/wake -> 202, command ${first.id}`);
      refreshAfterAction();
      // Idempotent API: an already-active wake is returned as is, and waking an online device
      // answers with a command that is already `completed`. Neither is an error.
      const immediate = isTerminal(first);
      let last: ApiCommand['status'] | undefined;
      const final = await follow(first, isCurrent, c => {
        if (c.status === last) return;
        last = c.status;
        if (c.status === 'pending') {
          set({ step: 'generating', message: 'Command queued by the Controller...', progressPercent: 25 });
        } else if (c.status === 'sent' || c.status === 'acknowledged') {
          set({
            step: 'probing',
            message: `Magic packet sent. Waiting for ${device.ip}:${device.monitoring.port} to answer...`,
            progressPercent: 70
          });
        }
        log(`command status: ${c.status}`);
      });
      if (!final) return;
      void refreshDevices().catch(() => undefined);
      void refreshCommands().catch(() => undefined);
      if (final.status === 'completed') {
        set({
          step: 'online_confirmed',
          message: immediate ? 'The device is already online. Nothing to do.' : `Device started! Verified TCP connection on port ${device.monitoring.port}.`,
          progressPercent: 100
        });
        log(immediate ? 'Device is already ONLINE; no packet was sent.' : 'Device status confirmed ONLINE by the monitor.');
      } else if (final.status === 'timed_out' || !isTerminal(final)) {
        set({ step: 'packet_sent_probing_timeout', message: 'Packet sent, but the device did not come online before the deadline.', progressPercent: 70 });
        log('Deadline passed without an online probe. The machine may need more time, or WOL is disabled in its BIOS/NIC.');
      } else {
        set({ step: 'failed', message: final.error ?? 'The Controller could not send the Wake-on-LAN packet.', progressPercent: 25 });
        log(`FAILED${final.error_code ? ` (${final.error_code})` : ''}: ${final.error ?? 'unknown error'}`);
      }
    } catch (e) {
      if (e instanceof ApiError && e.rateLimited) {
        // Throttled, not failed: another request for this device was handled a moment ago.
        powerCooldown.hold(device.id, (e.retryAfterSeconds ?? 1) * 1000);
        setWakeFlow(prev => (isCurrent() ? idleWake : prev));
        pushToast('info', tooManyRequests(device.name));
        return;
      }
      set({ step: 'failed', message: errMsg(e), progressPercent: 15 });
      log(`FAILED: ${errMsg(e)}`);
    }
  };

  const cancelWakeFlow = () => {
    wakeRun.current++; // stop following; the command keeps running on the Controller
    setWakeFlow(idleWake);
  };

  // ---- Shutdown flow: POST /shutdown (signed by the Controller), follow the command -----------
  const [shutdownFlow, setShutdownFlow] = useState<ShutdownFlowState>(idleShutdown);
  const shutdownRun = useRef(0);

  const requestShutdownConfirmation = (device: Device) => {
    shutdownRun.current++;
    setShutdownFlow({
      active: true,
      deviceId: device.id,
      deviceName: device.name,
      step: 'confirming',
      message: `Are you sure you want to shut down ${device.name}? Unsaved work will be lost.`,
      progressPercent: 10,
      logs: [
        `[${clockTime()}] Agent endpoint: ${device.shutdown.agentScheme}://${device.ip}:${device.shutdown.agentPort}`,
        `[${clockTime()}] Agent id: ${device.shutdown.agentId || '(not set)'}`
      ]
    });
  };

  const confirmAndExecuteShutdown = async () => {
    const target = devices.find(d => d.id === shutdownFlow.deviceId);
    if (!target) return;
    // At most one wake/shutdown request per device per second; stay on the confirmation step.
    if (!powerCooldown.tryAcquire(target.id)) {
      pushToast('info', tooManyRequests(target.name));
      return;
    }
    const run = ++shutdownRun.current;
    const isCurrent = () => shutdownRun.current === run;
    const log = (line: string) => setShutdownFlow(prev => (isCurrent() ? { ...prev, logs: [...prev.logs, `[${clockTime()}] ${line}`] } : prev));
    const set = (patch: Partial<ShutdownFlowState>) => setShutdownFlow(prev => (isCurrent() ? { ...prev, ...patch } : prev));

    set({ step: 'connecting', message: 'Requesting a signed shutdown command from the Controller...', progressPercent: 30 });
    try {
      const first = await api.shutdownDevice(target.id);
      log(`POST /devices/${target.id}/shutdown -> 202, command ${first.id}`);
      refreshAfterAction();
      // Idempotent API: an already-active shutdown is returned as is, and shutting down an offline
      // device answers with a command that is already `completed`. Neither is an error.
      const immediate = isTerminal(first);
      let last: ApiCommand['status'] | undefined;
      let ackSeen = false;
      const final = await follow(first, isCurrent, c => {
        if (c.status === last) return;
        last = c.status;
        if (c.status === 'pending') {
          set({ step: 'connecting', message: `Dispatching to the agent at ${target.ip}:${target.shutdown.agentPort}...`, progressPercent: 30 });
        } else if (c.status === 'sent') {
          set({ step: 'authenticating', message: 'Agent is verifying the Ed25519 signature...', progressPercent: 50 });
        } else if (c.status === 'acknowledged') {
          ackSeen = true;
          set({ step: 'waiting_offline', message: `Agent accepted the command. Watching ${target.ip}:${target.monitoring.port} for shutdown...`, progressPercent: 88 });
        }
        log(`command status: ${c.status}`);
      });
      if (!final) return;
      void refreshDevices().catch(() => undefined);
      void refreshCommands().catch(() => undefined);
      if (final.status === 'completed') {
        set({
          step: 'offline_verified',
          message: immediate ? 'The device is already offline. Nothing to do.' : 'Device successfully powered off and confirmed offline.',
          progressPercent: 100
        });
        log(immediate ? 'Device is already OFFLINE; the agent was not contacted.' : 'Device status confirmed OFFLINE by the monitor.');
      } else if (final.status === 'timed_out' || !isTerminal(final)) {
        set({
          step: 'failed',
          message: ackSeen ? 'The agent accepted the command but the device is still online after the deadline.' : 'The agent did not respond before the deadline.',
          progressPercent: ackSeen ? 88 : 50
        });
        log('Deadline passed.');
      } else {
        set({ step: 'failed', message: final.error ?? 'The agent rejected or could not receive the command.', progressPercent: 50 });
        log(`FAILED${final.error_code ? ` (${final.error_code})` : ''}: ${final.error ?? 'unknown error'}`);
      }
    } catch (e) {
      if (e instanceof ApiError && e.rateLimited) {
        // Throttled, not failed: back to the confirmation so the user can retry in a moment.
        powerCooldown.hold(target.id, (e.retryAfterSeconds ?? 1) * 1000);
        set({ step: 'confirming', message: `Are you sure you want to shut down ${target.name}? Unsaved work will be lost.`, progressPercent: 10 });
        pushToast('info', tooManyRequests(target.name));
        return;
      }
      set({ step: 'failed', message: errMsg(e), progressPercent: 30 });
      log(`FAILED: ${errMsg(e)}`);
    }
  };

  const cancelShutdownFlow = () => {
    shutdownRun.current++;
    setShutdownFlow(idleShutdown);
  };

  // ---- DDNS ------------------------------------------------------------------------------
  const ddnsConfigs = useMemo(() => apiDdns.map(c => toDDNSConfig(c, ddnsBusy.has(c.id))), [apiDdns, ddnsBusy]);
  const ddnsConfig = useMemo(
    () => ddnsConfigs.find(c => c.id === activeDdnsId) ?? ddnsConfigs[0] ?? null,
    [ddnsConfigs, activeDdnsId]
  );

  const setBusy = (id: string, on: boolean) =>
    setDdnsBusy(prev => {
      const next = new Set(prev);
      if (on) next.add(id); else next.delete(id);
      return next;
    });

  const triggerDDNSSync = async (id?: string) => {
    const target = id ?? ddnsConfig?.id;
    if (!target) {
      pushToast('info', 'No DDNS record is configured yet.');
      return;
    }
    const cfg = apiDdns.find(c => c.id === target);
    setBusy(target, true);
    try {
      const r = await api.syncDdnsConfig(target);
      refreshAfterAction();
      if (r.result === 'failed') pushToast('error', `DDNS sync failed: ${r.error ?? 'unknown error'}`);
      await refreshDdns().catch(() => undefined);
    } catch (e) {
      pushToast('error', `DDNS sync failed: ${errMsg(e)}`);
    } finally {
      setBusy(target, false);
    }
  };

  const saveDdns = async (id: string | null, draft: DDNSDraft): Promise<string | null> => {
    try {
      const saved: ApiDdnsConfig = id
        ? await api.updateDdnsConfig(id, buildDdnsUpdate(draft))
        : await api.createDdnsConfig(buildDdnsCreate(draft));
      setApiDdns(prev => (prev.some(c => c.id === saved.id) ? prev.map(c => (c.id === saved.id ? saved : c)) : [...prev, saved]));
      setActiveDdnsId(saved.id);
      refreshAfterAction();
      return null;
    } catch (e) {
      return errMsg(e);
    }
  };

  const deleteDdns = async (id: string): Promise<string | null> => {
    try {
      await api.deleteDdnsConfig(id);
      setApiDdns(prev => prev.filter(c => c.id !== id));
      setActiveDdnsId(prev => (prev === id ? null : prev));
      refreshAfterAction();
      return null;
    } catch (e) {
      return errMsg(e);
    }
  };

  // ---- Settings (browser-local) ----------------------------------------------------------
  const updateSettings = (newSettings: Partial<AppSettings>) => setSettings(prev => ({ ...prev, ...newSettings }));

  const systemMetrics = useMemo(() => (apiMetrics ? toSystemMetrics(apiMetrics) : null), [apiMetrics]);

  const connectionIssue = useMemo(() => {
    for (const k of ['devices', 'commands', 'activity', 'metrics']) if (issues[k]) return issues[k];
    return null;
  }, [issues]);

  const onlineCount = devices.filter(d => d.status === 'online').length;
  const totalDevices = devices.length;

  return (
    <AppContext.Provider
      value={{
        username: auth.user?.username ?? '',
        logout: auth.logout,
        serverHealth,
        connectionIssue,
        devicesLoaded,
        toasts,
        dismissToast,
        activeTab,
        setActiveTab,
        ambientMode,
        setAmbientMode,
        theme,
        toggleTheme,
        sidebarCollapsed,
        setSidebarCollapsed,
        devices,
        selectedDevice,
        setSelectedDevice,
        deviceModalOpen,
        setDeviceModalOpen,
        editingDevice,
        setEditingDevice,
        saveDevice,
        deleteDevice,
        probeDevice,
        refreshDevices,
        isPowerLocked: powerCooldown.isLocked,
        wakeFlow,
        startWakeFlow,
        cancelWakeFlow,
        shutdownFlow,
        requestShutdownConfirmation,
        confirmAndExecuteShutdown,
        cancelShutdownFlow,
        ddnsConfigs,
        ddnsConfig,
        setActiveDdnsId,
        triggerDDNSSync,
        saveDdns,
        deleteDdns,
        activities,
        activityTruncated,
        addActivity,
        clearActivities,
        systemMetrics,
        refreshSystemMetrics,
        controllerKey,
        controllerKeyError,
        loadControllerKey,
        settings,
        updateSettings,
        onlineCount,
        totalDevices
      }}
    >
      {children}
    </AppContext.Provider>
  );
};

export const useApp = () => {
  const context = useContext(AppContext);
  if (!context) {
    throw new Error('useApp must be used within an AppProvider');
  }
  return context;
};
