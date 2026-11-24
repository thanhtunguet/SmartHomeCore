import React from 'react';
import { useApp } from '../../context/AppContext';
import {
  Sun,
  Moon,
  Clock,
  Plus,
  RefreshCw,
  Search,
  Menu,
  ShieldCheck
} from 'lucide-react';

interface TopBarProps {
  onOpenCommandPalette: () => void;
  onToggleMobileMenu?: () => void;
}

export const TopBar: React.FC<TopBarProps> = ({
  onOpenCommandPalette,
  onToggleMobileMenu
}) => {
  const {
    activeTab,
    theme,
    toggleTheme,
    setAmbientMode,
    setDeviceModalOpen,
    setEditingDevice,
    triggerDDNSSync,
    ddnsConfig
  } = useApp();

  const getTabTitle = () => {
    switch (activeTab) {
      case 'dashboard':
        return 'Infrastructure Dashboard';
      case 'devices':
        return 'Device Management';
      case 'ddns':
        return 'Cloudflare DDNS';
      case 'activity':
        return 'Event Activity';
      case 'system':
        return 'Host & Process Metrics';
      case 'settings':
        return 'Server Settings';
    }
  };

  return (
    <header className="h-14 border-b border-neutral-200 dark:border-neutral-800 bg-white/80 dark:bg-neutral-950/80 backdrop-blur-md px-4 sm:px-6 flex items-center justify-between gap-4 sticky top-0 z-20">
      {/* Zone 1: Breadcrumb and Title */}
      <div className="flex items-center gap-3 min-w-0">
        {onToggleMobileMenu && (
          <button
            onClick={onToggleMobileMenu}
            className="p-1.5 -ml-1 text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-neutral-100 rounded-lg hover:bg-neutral-100 dark:hover:bg-neutral-900 md:hidden"
            aria-label="Toggle navigation menu"
          >
            <Menu size={18} />
          </button>
        )}

        <div className="flex items-center gap-2 text-sm text-neutral-500 dark:text-neutral-400 truncate">
          <span className="font-semibold text-neutral-900 dark:text-neutral-100 hidden sm:inline">
            HomeCore
          </span>
          <span className="hidden sm:inline" aria-hidden="true">/</span>
          <span className="text-neutral-700 dark:text-neutral-300 font-medium truncate">
            {getTabTitle()}
          </span>
        </div>
      </div>

      {/* Zone 2: Search Trigger */}
      <div className="flex flex-1 items-center gap-2 min-w-0">
        <button
          onClick={onOpenCommandPalette}
          className="hidden md:flex w-full items-center gap-2 px-3 py-1.5 text-xs text-neutral-500 dark:text-neutral-400 bg-neutral-100 dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-lg hover:border-neutral-300 dark:hover:border-neutral-700 hover:text-neutral-700 dark:hover:text-neutral-200 transition-colors"
        >
          <Search size={14} className="text-neutral-400" />
          <span className="flex-1 text-left truncate">Search devices, actions...</span>
          <kbd className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-neutral-200 dark:bg-neutral-800 text-neutral-600 dark:text-neutral-300">
            ⌘K
          </kbd>
        </button>
      </div>

      {/* Zone 3: Actions (Ambient Mode, Theme, Primary Action) */}
      <div className="flex items-center gap-2 shrink-0">
        {/* Ambient Clock Launcher for Standby / Phone Display */}
        <button
          onClick={() => setAmbientMode(true)}
          title="Open Ambient Clock Screensaver (for OLED / Wall mount display)"
          aria-label="Ambient Display"
          className="flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium text-neutral-600 dark:text-neutral-300 hover:text-neutral-900 dark:hover:text-neutral-100 rounded-lg hover:bg-neutral-100 dark:hover:bg-neutral-900 transition-colors"
        >
          <Clock size={15} />
          <span className="hidden lg:inline">Ambient Display</span>
        </button>

        {/* Theme Toggle */}
        <button
          onClick={toggleTheme}
          aria-label="Toggle dark/light mode"
          title={`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`}
          className="p-2 text-neutral-600 dark:text-neutral-400 hover:text-neutral-900 dark:hover:text-neutral-100 rounded-lg hover:bg-neutral-100 dark:hover:bg-neutral-900 transition-colors"
        >
          {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
        </button>

        {/* Tab-Contextual Primary Action */}
        {activeTab === 'ddns' ? (
          <button
            onClick={() => triggerDDNSSync()}
            aria-label="Sync DNS"
            title="Sync DNS"
            disabled={!ddnsConfig || ddnsConfig.status === 'updating'}
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg transition-colors disabled:opacity-50 whitespace-nowrap"
          >
            <RefreshCw size={14} className={ddnsConfig?.status === 'updating' ? 'animate-spin' : ''} />
            <span className="hidden lg:inline">Sync DNS</span>
          </button>
        ) : (
          <button
            onClick={() => {
              setEditingDevice(null);
              setDeviceModalOpen(true);
            }}
            aria-label="Add Device"
            title="Add Device"
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg transition-colors whitespace-nowrap shadow-sm shadow-emerald-950/20"
          >
            <Plus size={14} />
            <span className="hidden lg:inline">Add Device</span>
          </button>
        )}
      </div>
    </header>
  );
};
