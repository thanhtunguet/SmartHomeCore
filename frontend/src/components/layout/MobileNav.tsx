import React from 'react';
import { useApp } from '../../context/AppContext';
import {
  LayoutDashboard,
  HardDrive,
  Globe,
  ArrowLeftRight,
  Activity,
  Cpu,
  Settings
} from 'lucide-react';

export const MobileNav: React.FC = () => {
  const { activeTab, setActiveTab } = useApp();

  const navItems = [
    { id: 'dashboard' as const, label: 'Dashboard', icon: LayoutDashboard },
    { id: 'devices' as const, label: 'Devices', icon: HardDrive },
    { id: 'ddns' as const, label: 'DDNS', icon: Globe },
    { id: 'portforward' as const, label: 'Tunnel', icon: ArrowLeftRight },
    { id: 'activity' as const, label: 'Activity', icon: Activity },
    { id: 'system' as const, label: 'System', icon: Cpu },
    { id: 'settings' as const, label: 'Settings', icon: Settings },
  ];

  return (
    <nav className="md:hidden fixed bottom-0 left-0 right-0 z-30 bg-white/95 dark:bg-neutral-950/95 backdrop-blur-md border-t border-neutral-200 dark:border-neutral-800 pb-safe">
      <div className="flex items-center justify-around h-14 px-1">
        {navItems.map(item => {
          const Icon = item.icon;
          const isActive = activeTab === item.id;

          return (
            <button
              key={item.id}
              onClick={() => setActiveTab(item.id)}
              className={`flex flex-col items-center justify-center flex-1 py-1 px-1 rounded-md transition-colors ${
                isActive
                  ? 'text-emerald-500 font-medium'
                  : 'text-neutral-500 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-neutral-100'
              }`}
            >
              <Icon size={18} className="shrink-0" />
              <span className="text-[10px] mt-0.5 tracking-tight truncate max-w-[50px]">
                {item.label}
              </span>
            </button>
          );
        })}
      </div>
    </nav>
  );
};
