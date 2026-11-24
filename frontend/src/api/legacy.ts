/**
 * URLs of the legacy single-device API (`GET /api/turn-on`, `/api/turn-off`, `/api/is-online`),
 * for configuring old apps that predate multi-device support. Docs: ADR-017.
 */
import type { Device } from '../types';

export interface LegacyEndpoint {
  key: 'turnOn' | 'turnOff' | 'isOnline';
  label: string;
  /** What the old app receives. */
  hint: string;
  /** Path and query only (`/api/turn-on?device=…`); the origin is shown and copied separately. */
  path: string;
}

/**
 * The value of the `device` query parameter: the device id when it is an integer (the old
 * numbering), otherwise the device name. Ids issued by the Controller are UUIDs, so in practice the
 * name is used; the Controller resolves a name case-insensitively.
 */
export const legacyDeviceRef = (device: Pick<Device, 'id' | 'name'>): string =>
  /^\d+$/.test(device.id) ? device.id : device.name;

/** The three endpoints of one device, as paths only: the old app is given the origin separately. */
export const legacyEndpoints = (device: Pick<Device, 'id' | 'name'>): LegacyEndpoint[] => {
  const q = `?device=${encodeURIComponent(legacyDeviceRef(device))}`;
  return [
    { key: 'turnOn', label: 'Turn on', hint: 'Wake-on-LAN; answers true', path: `/api/turn-on${q}` },
    { key: 'turnOff', label: 'Turn off', hint: 'Signed shutdown; answers true', path: `/api/turn-off${q}` },
    { key: 'isOnline', label: 'Is online', hint: 'Answers true or false', path: `/api/is-online${q}` }
  ];
};
