import React from 'react';
import { DeviceType, OperatingSystem } from '../../types';
import {
  Monitor,
  Server,
  Laptop,
  Tv,
  HardDrive,
  Cpu,
  Router
} from 'lucide-react';

interface DeviceIconProps {
  type: DeviceType;
  os?: OperatingSystem;
  className?: string;
  size?: number;
}

export const DeviceIcon: React.FC<DeviceIconProps> = ({
  type,
  className = '',
  size = 18
}) => {
  switch (type) {
    case 'desktop':
      return <Monitor size={size} className={className} />;
    case 'server':
      return <Server size={size} className={className} />;
    case 'laptop':
      return <Laptop size={size} className={className} />;
    case 'tv':
      return <Tv size={size} className={className} />;
    case 'nas':
      return <HardDrive size={size} className={className} />;
    case 'router':
      return <Router size={size} className={className} />;
    case 'iot':
    default:
      return <Cpu size={size} className={className} />;
  }
};
