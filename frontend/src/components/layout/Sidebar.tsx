import React from 'react';
import { useApp } from '../../context/AppContext';
import {
  LayoutDashboard,
  HardDrive,
  Globe,
  ArrowLeftRight,
  Activity,
  Cpu,
  Settings,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  Shield,
  Wifi,
  LogOut
} from 'lucide-react';

interface SidebarProps {
  className?: string;
  onCloseMobile?: () => void;
}

export const Sidebar: React.FC<SidebarProps> = ({
  className = '',
  onCloseMobile
}) => {
  const {
    activeTab,
    setActiveTab,
    sidebarCollapsed: sidebarCollapsedState,
    setSidebarCollapsed,
    onlineCount,
    totalDevices,
    systemMetrics,
    connectionIssue,
    username,
    logout
  } = useApp();

  // The mobile drawer (onCloseMobile set) is always shown expanded.
  const sidebarCollapsed = sidebarCollapsedState && !onCloseMobile;

  const navItems = [
    { id: 'dashboard' as const, label: 'Dashboard', icon: LayoutDashboard },
    { id: 'devices' as const, label: 'Devices', icon: HardDrive, count: `${onlineCount}/${totalDevices}` },
    { id: 'ddns' as const, label: 'DDNS', icon: Globe },
    { id: 'portforward' as const, label: 'Tunnel', icon: ArrowLeftRight },
    { id: 'activity' as const, label: 'Activity', icon: Activity },
    { id: 'system' as const, label: 'System', icon: Cpu },
    { id: 'settings' as const, label: 'Settings', icon: Settings },
  ];

  const handleSelectTab = (tab: typeof activeTab) => {
    setActiveTab(tab);
    if (onCloseMobile) onCloseMobile();
  };

  return (
    <aside
      className={`h-screen bg-neutral-50 dark:bg-neutral-900 border-r border-neutral-200 dark:border-neutral-800 flex flex-col justify-between transition-all duration-200 ${
        sidebarCollapsed ? 'w-16' : 'w-60'
      } ${className}`}
    >
      <div>
        {/* Brand Header */}
        <div className="h-14 flex items-center justify-between px-4 border-b border-neutral-200 dark:border-neutral-800">
          {!sidebarCollapsed ? (
            <div className="flex items-center gap-2.5">
              <div className="w-8 h-8 rounded-lg bg-neutral-900 dark:bg-neutral-100 flex items-center justify-center text-white dark:text-neutral-900 font-bold text-base shadow-sm">
                H
              </div>
              <div>
                <span className="font-semibold text-sm tracking-tight text-neutral-900 dark:text-neutral-100 block leading-tight">
                  HomeCore
                </span>
                <span className="text-[11px] text-neutral-500 dark:text-neutral-400 block leading-tight">
                  Node Controller
                </span>
              </div>
            </div>
          ) : (
            <div className="w-8 h-8 mx-auto rounded-lg bg-neutral-900 dark:bg-neutral-100 flex items-center justify-center text-white dark:text-neutral-900 font-bold text-base shadow-sm">
              H
            </div>
          )}

          {/* Desktop Collapse Toggle */}
          <button
            onClick={() => setSidebarCollapsed(prev => !prev)}
            className="hidden md:flex p-1.5 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-300 rounded-md hover:bg-neutral-200 dark:hover:bg-neutral-800 transition-colors"
            title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          >
            {sidebarCollapsed ? <ChevronRight size={16} /> : <ChevronLeft size={16} />}
          </button>
        </div>

        {/* Navigation Links */}
        <nav className="p-3 space-y-1">
          {navItems.map(item => {
            const Icon = item.icon;
            const isActive = activeTab === item.id;

            return (
              <button
                key={item.id}
                onClick={() => handleSelectTab(item.id)}
                className={`w-full flex items-center gap-3 px-3 py-2 rounded-lg text-xs font-medium transition-colors ${
                  isActive
                    ? 'bg-neutral-200 dark:bg-neutral-800 text-neutral-900 dark:text-neutral-100 font-semibold shadow-xs'
                    : 'text-neutral-600 dark:text-neutral-400 hover:bg-neutral-100 dark:hover:bg-neutral-800/60 hover:text-neutral-900 dark:hover:text-neutral-200'
                }`}
                title={sidebarCollapsed ? item.label : undefined}
              >
                <Icon size={17} className={`shrink-0 ${isActive ? 'text-emerald-500' : 'text-neutral-400'}`} />
                {!sidebarCollapsed && (
                  <span className="truncate flex-1 text-left">{item.label}</span>
                )}
                {!sidebarCollapsed && item.count && (
                  <span className="text-[11px] font-mono text-neutral-400 tabular-nums">
                    {item.count}
                  </span>
                )}
              </button>
            );
          })}
        </nav>
      </div>

      {/* Footer Info & API Docs Link */}
      <div className="p-3 border-t border-neutral-200 dark:border-neutral-800">
        {!sidebarCollapsed ? (
          <div className="space-y-2">
            <a
              href="/api/docs/"
              target="_blank"
              rel="noreferrer"
              className="flex items-center justify-between px-2.5 py-1.5 text-xs text-neutral-500 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-neutral-100 rounded-md hover:bg-neutral-100 dark:hover:bg-neutral-800/60 transition-colors"
            >
              <span className="flex items-center gap-2">
                <Shield size={14} className="text-neutral-400" />
                <span>REST API Docs</span>
              </span>
              <ExternalLink size={12} className="text-neutral-400" />
            </a>

            <div className="px-2.5 py-1 text-[11px] text-neutral-400 dark:text-neutral-500">
              <div className="flex items-center justify-between gap-2">
                <span className="shrink-0">Controller</span>
                <span
                  className="min-w-0 truncate font-mono text-neutral-600 dark:text-neutral-400"
                  title={systemMetrics?.localIps[0] ?? window.location.hostname}
                >
                  {systemMetrics?.localIps[0] ?? window.location.hostname}
                </span>
              </div>
            </div>

            <div className="flex items-center justify-between px-2.5 py-1 text-[11px] text-neutral-500 dark:text-neutral-400">
              <span className="truncate" title={`Signed in as ${username}`}>
                {username}
              </span>
              <button
                onClick={() => void logout()}
                title="Sign out"
                aria-label="Sign out"
                className="p-1 rounded-md hover:bg-neutral-100 dark:hover:bg-neutral-800/60 hover:text-neutral-900 dark:hover:text-neutral-100"
              >
                <LogOut size={13} />
              </button>
            </div>
          </div>
        ) : (
          <div className="flex justify-center">
            <span className={`w-2 h-2 rounded-full ${connectionIssue ? 'bg-amber-500' : 'bg-emerald-500'}`} title={connectionIssue ? 'API issue' : 'Connected'}></span>
          </div>
        )}
      </div>
    </aside>
  );
};
