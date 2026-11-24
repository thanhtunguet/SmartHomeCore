import React, { useState } from 'react';
import { AppProvider, useApp } from './context/AppContext';
import { AuthProvider, useAuth } from './context/AuthContext';
import { LoginScreen } from './components/auth/LoginScreen';
import { StatusBanner } from './components/layout/StatusBanner';
import { Toasts } from './components/layout/Toasts';
import { TopBar } from './components/layout/TopBar';
import { Sidebar } from './components/layout/Sidebar';
import { MobileNav } from './components/layout/MobileNav';

// Screens
import { DashboardScreen } from './components/dashboard/DashboardScreen';
import { DevicesScreen } from './components/devices/DevicesScreen';
import { DDNSScreen } from './components/ddns/DDNSScreen';
import { PortForwardScreen } from './components/portforward/PortForwardScreen';
import { ActivityScreen } from './components/activity/ActivityScreen';
import { SystemScreen } from './components/system/SystemScreen';
import { SettingsScreen } from './components/settings/SettingsScreen';

// Modals & Overlays
import { WakeFlowModal } from './components/devices/WakeFlowModal';
import { ShutdownFlowModal } from './components/devices/ShutdownFlowModal';
import { DeviceDetailSheet } from './components/devices/DeviceDetailSheet';
import { DeviceFormModal } from './components/devices/DeviceFormModal';
import { CommandPalette } from './components/common/CommandPalette';
import { AmbientClockMode } from './components/ambient/AmbientClockMode';

const AppContent: React.FC = () => {
  const { activeTab, selectedDevice, setSelectedDevice, ambientMode, sidebarCollapsed, setSidebarCollapsed } = useApp();
  const [commandPaletteOpen, setCommandPaletteOpen] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);

  return (
    <div className="min-h-screen bg-neutral-100 dark:bg-neutral-950 text-neutral-900 dark:text-neutral-100 flex flex-col md:flex-row transition-colors">
      {/* Desktop & Tablet Sidebar */}
      {/* Below lg the sidebar floats over the content (fixed) and the main pane keeps a
          constant collapsed-width margin; from lg up it sits in the flex flow again. */}
      <Sidebar className="hidden md:flex shrink-0 fixed inset-y-0 left-0 z-30 lg:sticky lg:top-0 lg:z-auto" />

      {/* Tablet backdrop: tap outside the expanded, overlapping sidebar to collapse it */}
      {!sidebarCollapsed && (
        <div
          className="hidden md:block lg:hidden fixed inset-0 z-20 bg-black/30"
          onClick={() => setSidebarCollapsed(true)}
          aria-hidden="true"
        ></div>
      )}

      {/* Mobile Drawer Backdrop & Sidebar */}
      {mobileMenuOpen && (
        <div className="fixed inset-0 z-40 md:hidden flex">
          <div
            className="fixed inset-0 bg-black/50 backdrop-blur-xs"
            onClick={() => setMobileMenuOpen(false)}
          ></div>
          <div className="relative z-50 w-64 h-full bg-white dark:bg-neutral-900 shadow-xl">
            <Sidebar onCloseMobile={() => setMobileMenuOpen(false)} />
          </div>
        </div>
      )}

      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-w-0 pb-16 md:pb-6 md:ml-16 lg:ml-0">
        <TopBar
          onOpenCommandPalette={() => setCommandPaletteOpen(true)}
          onToggleMobileMenu={() => setMobileMenuOpen(true)}
        />

        <main className="flex-1 p-4 sm:p-6 lg:p-8 overflow-y-auto">
          <StatusBanner />
          {activeTab === 'dashboard' && <DashboardScreen />}
          {activeTab === 'devices' && <DevicesScreen />}
          {activeTab === 'ddns' && <DDNSScreen />}
          {activeTab === 'portforward' && <PortForwardScreen />}
          {activeTab === 'activity' && <ActivityScreen />}
          {activeTab === 'system' && <SystemScreen />}
          {activeTab === 'settings' && <SettingsScreen />}
        </main>
      </div>

      {/* Mobile Bottom Navigation */}
      <MobileNav />

      {/* Global Modals & Command Overlays */}
      <CommandPalette
        isOpen={commandPaletteOpen}
        onClose={() => setCommandPaletteOpen(false)}
      />

      <Toasts />
      <WakeFlowModal />
      <ShutdownFlowModal />
      <DeviceFormModal />

      {selectedDevice && (
        <DeviceDetailSheet
          device={selectedDevice}
          onClose={() => setSelectedDevice(null)}
        />
      )}

      {/* Android Screensaver / Standby Ambient Mode */}
      {ambientMode && <AmbientClockMode />}
    </div>
  );
};

/** Shows the login screen until the Controller has accepted credentials. */
const AuthGate: React.FC = () => {
  const { status } = useAuth();
  if (status === 'loading') {
    return <div className="min-h-screen flex items-center justify-center bg-neutral-100 dark:bg-neutral-950 text-xs text-neutral-500">Connecting…</div>;
  }
  if (status === 'anonymous') return <LoginScreen />;
  return (
    <AppProvider>
      <AppContent />
    </AppProvider>
  );
};

export default function App() {
  return (
    <AuthProvider>
      <AuthGate />
    </AuthProvider>
  );
}
