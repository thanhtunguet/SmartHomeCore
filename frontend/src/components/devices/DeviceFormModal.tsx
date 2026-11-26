import React, { useState, useEffect } from 'react';
import { useApp } from '../../context/AppContext';
import { Device, DeviceType, OperatingSystem } from '../../types';
import { CustomSelect } from '../common/CustomSelect';
import { AgentSetupPanel } from './AgentSetupPanel';
import { AgentDetectPanel } from './AgentDetectPanel';
import { newAgentId } from '../../api/agentCommand';
import {
  X,
  Server,
  Network,
  Activity,
  Zap,
  Power,
  KeyRound,
  Check,
  AlertCircle
} from 'lucide-react';

export const DeviceFormModal: React.FC = () => {
  const {
    deviceModalOpen,
    setDeviceModalOpen,
    editingDevice,
    setEditingDevice,
    saveDevice,
    settings,
    controllerKey,
    controllerKeyError,
    loadControllerKey
  } = useApp();

  const [activeTab, setActiveTab] = useState<'basic' | 'monitoring' | 'wol' | 'shutdown'>('basic');

  // Form Fields
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [type, setType] = useState<DeviceType>('desktop');
  const [os, setOs] = useState<OperatingSystem>('linux');
  const [ip, setIp] = useState('');
  const [mac, setMac] = useState('');

  // Monitoring
  const [monitorEnabled, setMonitorEnabled] = useState(true);
  const [portPreset, setPortPreset] = useState<'SSH' | 'RDP' | 'HTTP' | 'HTTPS' | 'CUSTOM'>('SSH');
  const [monitorPort, setMonitorPort] = useState(22);
  const [intervalSec, setIntervalSec] = useState(30);
  const [timeoutMs, setTimeoutMs] = useState(1500);

  // WOL
  const [wolEnabled, setWolEnabled] = useState(true);
  const [broadcastIp, setBroadcastIp] = useState('');
  const [wolPort, setWolPort] = useState(9);

  // Shutdown Agent
  const [shutdownEnabled, setShutdownEnabled] = useState(false);
  const [agentId, setAgentId] = useState('');
  const [agentPort, setAgentPort] = useState(9750);
  const [agentScheme, setAgentScheme] = useState<'http' | 'https'>('http');
  const [keyCopied, setKeyCopied] = useState(false);

  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (editingDevice) {
      setName(editingDevice.name);
      setDescription(editingDevice.description);
      setType(editingDevice.type);
      setOs(editingDevice.os);
      setIp(editingDevice.ip);
      setMac(editingDevice.mac);

      setMonitorEnabled(editingDevice.monitoring.enabled);
      setPortPreset(editingDevice.monitoring.protocolPreset);
      setMonitorPort(editingDevice.monitoring.port);
      setIntervalSec(editingDevice.monitoring.intervalSeconds);
      setTimeoutMs(editingDevice.monitoring.timeoutMs);

      setWolEnabled(editingDevice.wol.enabled);
      setBroadcastIp(editingDevice.wol.broadcastAddress);
      setWolPort(editingDevice.wol.port || 9);

      setShutdownEnabled(editingDevice.shutdown.enabled);
      setAgentId(editingDevice.shutdown.agentId);
      setAgentPort(editingDevice.shutdown.agentPort || 9750);
      setAgentScheme(editingDevice.shutdown.agentScheme);
    } else {
      // Defaults for new device
      setName('');
      setDescription('');
      setType('desktop');
      setOs('windows');
      setIp('');
      setMac('');

      setMonitorEnabled(true);
      setPortPreset('RDP');
      setMonitorPort(3389);
      setIntervalSec(settings.monitoring.defaultIntervalSec);
      setTimeoutMs(settings.monitoring.defaultTimeoutMs);

      setWolEnabled(true);
      setBroadcastIp('');
      setWolPort(9);

      setShutdownEnabled(false);
      setAgentId('');
      setAgentPort(9750);
      setAgentScheme('http');
    }
    setActiveTab('basic');
    setFormError(null);
    setSubmitting(false);
  }, [editingDevice, deviceModalOpen]);

  // The key the Agent must trust is shown next to the agent settings.
  useEffect(() => {
    if (deviceModalOpen && activeTab === 'shutdown' && shutdownEnabled && !controllerKey) void loadControllerKey();
  }, [deviceModalOpen, activeTab, shutdownEnabled]);

  if (!deviceModalOpen) return null;

  const handlePresetSelect = (preset: 'SSH' | 'RDP' | 'HTTP' | 'HTTPS' | 'CUSTOM') => {
    setPortPreset(preset);
    switch (preset) {
      case 'SSH':
        setMonitorPort(22);
        break;
      case 'RDP':
        setMonitorPort(3389);
        break;
      case 'HTTP':
        setMonitorPort(80);
        break;
      case 'HTTPS':
        setMonitorPort(443);
        break;
      case 'CUSTOM':
        break;
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || submitting) return;
    if (shutdownEnabled && !agentId.trim()) {
      setFormError('Enter the Agent ID (UUID) to enable remote shutdown.');
      setActiveTab('shutdown');
      return;
    }
    if (wolEnabled && !mac.trim()) {
      setFormError('A MAC address is required for Wake-on-LAN.');
      setActiveTab('basic');
      return;
    }

    setSubmitting(true);
    setFormError(null);
    const error = await saveDevice({
      name,
      description,
      type,
      os,
      ip,
      mac: mac.toUpperCase().trim(),
      monitoring: {
        enabled: monitorEnabled,
        port: Number(monitorPort),
        protocolPreset: portPreset,
        intervalSeconds: Number(intervalSec),
        timeoutMs: Number(timeoutMs)
      },
      wol: {
        enabled: wolEnabled,
        macAddress: mac.toUpperCase().trim(),
        broadcastAddress: broadcastIp.trim(),
        port: Number(wolPort)
      },
      shutdown: {
        enabled: shutdownEnabled,
        agentId: agentId.trim(),
        agentPort: Number(agentPort),
        agentScheme,
        agentStatus: shutdownEnabled ? 'unknown' : 'not_configured',
        agentStatusSource: 'inferred'
      }
    });
    if (error) {
      setFormError(error);
      setSubmitting(false);
    }
  };

  const copyKey = async () => {
    if (!controllerKey) return;
    try {
      await navigator.clipboard.writeText(controllerKey.public_key);
      setKeyCopied(true);
      setTimeout(() => setKeyCopied(false), 2000);
    } catch {
      /* clipboard unavailable (non-secure context): the key is selectable text */
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs animate-in fade-in duration-150">
      <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl max-w-xl w-full shadow-2xl overflow-hidden flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="px-5 py-4 border-b border-neutral-200 dark:border-neutral-800 flex items-center justify-between">
          <div>
            <h3 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100">
              {editingDevice ? `Edit Device: ${editingDevice.name}` : 'Register New Device'}
            </h3>
            <p className="text-xs text-neutral-500 dark:text-neutral-400">
              Configure network identity, TCP health probe, Wake-on-LAN, and agent shutdown.
            </p>
          </div>
          <button
            onClick={() => {
              setDeviceModalOpen(false);
              setEditingDevice(null);
            }}
            className="p-1 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-200 rounded-md"
          >
            <X size={16} />
          </button>
        </div>

        {/* Tab Strip */}
        <div className="flex border-b border-neutral-200 dark:border-neutral-800 px-5 bg-neutral-50/50 dark:bg-neutral-900/50 text-xs">
          <button
            type="button"
            onClick={() => setActiveTab('basic')}
            className={`py-2.5 px-3 font-medium border-b-2 transition-colors ${
              activeTab === 'basic'
                ? 'border-emerald-500 text-neutral-900 dark:text-neutral-100'
                : 'border-transparent text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300'
            }`}
          >
            Basic Identity
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('monitoring')}
            className={`py-2.5 px-3 font-medium border-b-2 transition-colors ${
              activeTab === 'monitoring'
                ? 'border-emerald-500 text-neutral-900 dark:text-neutral-100'
                : 'border-transparent text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300'
            }`}
          >
            TCP Monitoring
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('wol')}
            className={`py-2.5 px-3 font-medium border-b-2 transition-colors ${
              activeTab === 'wol'
                ? 'border-emerald-500 text-neutral-900 dark:text-neutral-100'
                : 'border-transparent text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300'
            }`}
          >
            Wake-on-LAN
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('shutdown')}
            className={`py-2.5 px-3 font-medium border-b-2 transition-colors ${
              activeTab === 'shutdown'
                ? 'border-emerald-500 text-neutral-900 dark:text-neutral-100'
                : 'border-transparent text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300'
            }`}
          >
            Remote Shutdown
          </button>
        </div>

        {/* Form Body */}
        <form onSubmit={handleSubmit} className="flex-1 overflow-y-auto p-5 space-y-4">
          {activeTab === 'basic' && (
            <div className="space-y-4 text-xs">
              <div>
                <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                  Device Name *
                </label>
                <input
                  type="text"
                  required
                  placeholder="e.g. Master Desktop PC"
                  value={name}
                  onChange={e => setName(e.target.value)}
                  className="w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 focus:border-emerald-500 text-xs"
                />
              </div>

              <div>
                <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                  Description / Location
                </label>
                <input
                  type="text"
                  placeholder="e.g. Office desk, used for remote rendering"
                  value={description}
                  onChange={e => setDescription(e.target.value)}
                  className="w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 focus:border-emerald-500 text-xs"
                />
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                    Device Type
                  </label>
                  <CustomSelect
                    value={type}
                    onChange={val => setType(val as DeviceType)}
                    options={[
                      { value: 'desktop', label: 'Desktop PC' },
                      { value: 'server', label: 'Server' },
                      { value: 'nas', label: 'NAS / Storage' },
                      { value: 'laptop', label: 'Laptop' },
                      { value: 'tv', label: 'Smart TV' },
                      { value: 'iot', label: 'Home Automation / IoT' },
                      { value: 'router', label: 'Router / Gateway' }
                    ]}
                  />
                </div>

                <div>
                  <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                    Operating System
                  </label>
                  <CustomSelect
                    value={os}
                    onChange={val => setOs(val as OperatingSystem)}
                    options={[
                      { value: 'windows', label: 'Windows 11 / 10' },
                      { value: 'linux', label: 'Linux' },
                      { value: 'macos', label: 'macOS' },
                      { value: 'android', label: 'Android' },
                      { value: 'freebsd', label: 'FreeBSD (TrueNAS)' },
                      { value: 'embedded', label: 'Embedded / Other' }
                    ]}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                    IP Address or Hostname *
                  </label>
                  <input
                    type="text"
                    required
                    placeholder="192.168.1.100"
                    value={ip}
                    onChange={e => setIp(e.target.value)}
                    className="w-full font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                  />
                </div>

                <div>
                  <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                    MAC Address (needed for Wake-on-LAN)
                  </label>
                  <input
                    type="text"
                    placeholder="AA:BB:CC:DD:EE:FF"
                    value={mac}
                    onChange={e => setMac(e.target.value)}
                    className="w-full font-mono uppercase px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                  />
                </div>
              </div>
            </div>
          )}

          {activeTab === 'monitoring' && (
            <div className="space-y-4 text-xs">
              <div className="p-3 bg-neutral-50 dark:bg-neutral-800/50 rounded-lg border border-neutral-200 dark:border-neutral-800 text-neutral-600 dark:text-neutral-400 flex items-start gap-2">
                <AlertCircle size={15} className="shrink-0 text-emerald-500 mt-0.5" />
                <p>
                  HomeCore checks device status via lightweight TCP SYN connection attempts. Select a port running a resident service (e.g. SSH on port 22, RDP on port 3389).
                </p>
              </div>

              <div>
                <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1.5">
                  Protocol Preset
                </label>
                <div className="grid grid-cols-5 gap-1.5">
                  {(['SSH', 'RDP', 'HTTP', 'HTTPS', 'CUSTOM'] as const).map(preset => (
                    <button
                      key={preset}
                      type="button"
                      onClick={() => handlePresetSelect(preset)}
                      className={`py-1.5 px-2 rounded-lg text-xs font-medium border text-center transition-colors ${
                        portPreset === preset
                          ? 'border-emerald-500 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                          : 'border-neutral-200 dark:border-neutral-700 text-neutral-600 dark:text-neutral-400 hover:bg-neutral-100 dark:hover:bg-neutral-800'
                      }`}
                    >
                      {preset}
                    </button>
                  ))}
                </div>
              </div>

              <div className="grid grid-cols-3 gap-3">
                <div>
                  <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                    TCP Port
                  </label>
                  <input
                    type="number"
                    min="1"
                    max="65535"
                    value={monitorPort}
                    onChange={e => setMonitorPort(Number(e.target.value))}
                    className="w-full font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                  />
                </div>

                <div>
                  <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                    Interval (sec)
                  </label>
                  <input
                    type="number"
                    min="5"
                    max="3600"
                    value={intervalSec}
                    onChange={e => setIntervalSec(Number(e.target.value))}
                    className="w-full font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                  />
                </div>

                <div>
                  <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                    Timeout (ms)
                  </label>
                  <input
                    type="number"
                    min="100"
                    max="10000"
                    step="100"
                    value={timeoutMs}
                    onChange={e => setTimeoutMs(Number(e.target.value))}
                    className="w-full font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                  />
                </div>
              </div>
            </div>
          )}

          {activeTab === 'wol' && (
            <div className="space-y-4 text-xs">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={wolEnabled}
                  onChange={e => setWolEnabled(e.target.checked)}
                  className="rounded text-emerald-600 focus:ring-emerald-500"
                />
                <span className="font-medium text-neutral-800 dark:text-neutral-200">
                  Enable Wake-on-LAN for this device
                </span>
              </label>

              {wolEnabled && (
                <div className="space-y-3 pt-2">
                  <div>
                    <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                      Broadcast Address (Subnet Directed)
                    </label>
                    <input
                      type="text"
                      placeholder="192.168.1.255"
                      value={broadcastIp}
                      onChange={e => setBroadcastIp(e.target.value)}
                      className="w-full font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                    />
                    <p className="text-[11px] text-neutral-400 mt-1">
                      Typically ends in .255 for standard /24 home networks.
                    </p>
                  </div>

                  <div>
                    <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                      UDP Destination Port
                    </label>
                    <input
                      type="number"
                      value={wolPort}
                      onChange={e => setWolPort(Number(e.target.value))}
                      className="w-24 font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                    />
                  </div>
                </div>
              )}
            </div>
          )}

          {activeTab === 'shutdown' && (
            <div className="space-y-4 text-xs">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={shutdownEnabled}
                  onChange={e => {
                    setShutdownEnabled(e.target.checked);
                    // A new binding starts with a fresh id, so the command below is ready to copy.
                    if (e.target.checked && !agentId.trim()) setAgentId(newAgentId());
                  }}
                  className="rounded text-emerald-600 focus:ring-emerald-500"
                />
                <span className="font-medium text-neutral-800 dark:text-neutral-200">
                  Enable HomeCore Remote Shutdown Agent integration
                </span>
              </label>

              {!shutdownEnabled && editingDevice?.shutdown.enabled && (
                <p className="text-[11px] text-amber-600 dark:text-amber-400 flex items-start gap-1.5">
                  <AlertCircle size={13} className="shrink-0 mt-0.5" />
                  <span>Saving will remove the agent binding; remote shutdown stops working for this device.</span>
                </p>
              )}

              {shutdownEnabled && (
                <div className="space-y-3 pt-2">
                  <p className="text-[11px] text-neutral-500 dark:text-neutral-400 leading-relaxed">
                    The agent runs on the device itself ({ip || 'device IP'}). Commands are signed by the
                    Controller with Ed25519; no shared secret is entered here.
                  </p>
                  <div>
                    <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                      Agent ID (UUID)
                    </label>
                    <input
                      type="text"
                      placeholder="generated below, or the id of an agent that is already installed"
                      value={agentId}
                      onChange={e => setAgentId(e.target.value)}
                      pattern="[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
                      title="UUID, e.g. 0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
                      className="w-full font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                    />
                  </div>
                  <div className="grid grid-cols-3 gap-3">
                    <div>
                      <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                        Agent Port
                      </label>
                      <input
                        type="number"
                        min="1"
                        max="65535"
                        value={agentPort}
                        onChange={e => setAgentPort(Number(e.target.value))}
                        className="w-full font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs"
                      />
                    </div>
                    <div className="col-span-2">
                      <label className="block font-medium text-neutral-700 dark:text-neutral-300 mb-1">
                        Scheme
                      </label>
                      <CustomSelect
                        value={agentScheme}
                        onChange={val => setAgentScheme(val as 'http' | 'https')}
                        options={[
                          { value: 'http', label: 'HTTP (use a VPN / trusted LAN)' },
                          { value: 'https', label: 'HTTPS (agent TLS enabled)' }
                        ]}
                      />
                    </div>
                  </div>

                  <div className="p-3 rounded-lg bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800">
                    <AgentDetectPanel
                      host={ip}
                      port={agentPort}
                      scheme={agentScheme}
                      agentId={agentId}
                      onUseId={setAgentId}
                      fillOnFirstLook={!editingDevice?.shutdown.enabled}
                    />
                  </div>

                  <div className="p-3 rounded-lg bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800">
                    <AgentSetupPanel
                      agentId={agentId}
                      port={agentPort}
                      scheme={agentScheme}
                      deviceOs={os}
                      onGenerate={() => setAgentId(newAgentId())}
                    />
                  </div>

                  <div className="p-3 rounded-lg bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800 space-y-1.5">
                    <div className="flex items-center justify-between">
                      <span className="font-medium text-neutral-700 dark:text-neutral-300 flex items-center gap-1.5">
                        <KeyRound size={13} />
                        <span>Controller public key (only for an agent configured with a file: <code>trusted_controllers</code>)</span>
                      </span>
                      {controllerKey && (
                        <button type="button" onClick={copyKey} className="text-emerald-600 dark:text-emerald-400 hover:underline text-[11px] font-medium shrink-0 ml-2">
                          {keyCopied ? 'Copied' : 'Copy'}
                        </button>
                      )}
                    </div>
                    {controllerKey ? (
                      <div className="font-mono text-[11px] text-neutral-600 dark:text-neutral-400 break-all select-all">
                        key_id {controllerKey.key_id} · {controllerKey.algorithm}<br />
                        {controllerKey.public_key}
                      </div>
                    ) : (
                      <p className="text-[11px] text-neutral-400">
                        {controllerKeyError ?? 'Loading…'}
                      </p>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}

          {formError && (
            <div role="alert" className="flex items-start gap-2 p-2.5 rounded-lg text-xs bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/20">
              <AlertCircle size={14} className="shrink-0 mt-0.5" />
              <span>{formError}</span>
            </div>
          )}

          {/* Dialog Action Buttons */}
          <div className="pt-4 border-t border-neutral-200 dark:border-neutral-800 flex justify-end gap-2.5">
            <button
              type="button"
              onClick={() => {
                setDeviceModalOpen(false);
                setEditingDevice(null);
              }}
              className="px-4 py-2 text-xs font-medium text-neutral-700 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 rounded-lg transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={submitting}
              className="px-4 py-2 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg shadow-sm transition-colors disabled:opacity-50"
            >
              {submitting ? 'Saving…' : editingDevice ? 'Save Changes' : 'Register Device'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
