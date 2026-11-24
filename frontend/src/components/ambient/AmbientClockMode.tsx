import React, { useState, useEffect, useRef } from 'react';
import { useApp } from '../../context/AppContext';

export const AmbientClockMode: React.FC = () => {
  const { ambientMode, setAmbientMode } = useApp();

  const [currentTime, setCurrentTime] = useState(new Date());
  const boxRef = useRef<HTMLDivElement>(null);

  // Position and velocity for billiard bounce physics
  const physicsRef = useRef({
    x: 60,
    y: 80,
    vx: 80, // pixels per second
    vy: 65,
    width: 380,
    height: 120,
    lastTime: performance.now()
  });

  // Ticker for current time
  useEffect(() => {
    if (!ambientMode) return;

    const timer = setInterval(() => {
      setCurrentTime(new Date());
    }, 1000);

    return () => clearInterval(timer);
  }, [ambientMode]);

  // Billiard bouncing physics loop via requestAnimationFrame
  useEffect(() => {
    if (!ambientMode) return;

    // Center initially based on viewport
    const initialWidth = boxRef.current?.offsetWidth || 380;
    const initialHeight = boxRef.current?.offsetHeight || 120;
    physicsRef.current.width = initialWidth;
    physicsRef.current.height = initialHeight;
    physicsRef.current.x = Math.max(20, Math.floor((window.innerWidth - initialWidth) / 2));
    physicsRef.current.y = Math.max(20, Math.floor((window.innerHeight - initialHeight) / 2));
    physicsRef.current.lastTime = performance.now();

    // Randomize initial billiard trajectory angle
    const speed = 75; // px / sec
    const angle = (Math.PI / 4) + (Math.random() - 0.5) * (Math.PI / 3);
    const dirX = Math.random() > 0.5 ? 1 : -1;
    const dirY = Math.random() > 0.5 ? 1 : -1;
    physicsRef.current.vx = Math.cos(angle) * speed * dirX;
    physicsRef.current.vy = Math.sin(angle) * speed * dirY;

    let animId: number;

    const animate = (now: number) => {
      const dt = Math.min(0.1, (now - physicsRef.current.lastTime) / 1000); // delta time in seconds
      physicsRef.current.lastTime = now;

      // Measure current dimensions
      if (boxRef.current) {
        physicsRef.current.width = boxRef.current.offsetWidth;
        physicsRef.current.height = boxRef.current.offsetHeight;
      }

      const p = physicsRef.current;
      const winW = window.innerWidth;
      const winH = window.innerHeight;

      const padding = 12;
      const minX = padding;
      const maxX = Math.max(minX, winW - p.width - padding);
      const minY = padding;
      const maxY = Math.max(minY, winH - p.height - padding);

      // Advance position
      p.x += p.vx * dt;
      p.y += p.vy * dt;

      // Billiard rebound on left / right screen edge
      if (p.x >= maxX) {
        p.x = maxX;
        p.vx = -Math.abs(p.vx);
      } else if (p.x <= minX) {
        p.x = minX;
        p.vx = Math.abs(p.vx);
      }

      // Billiard rebound on top / bottom screen edge
      if (p.y >= maxY) {
        p.y = maxY;
        p.vy = -Math.abs(p.vy);
      } else if (p.y <= minY) {
        p.y = minY;
        p.vy = Math.abs(p.vy);
      }

      // Direct GPU transform update for 60fps buttery movement
      if (boxRef.current) {
        boxRef.current.style.transform = `translate3d(${p.x}px, ${p.y}px, 0)`;
      }

      animId = requestAnimationFrame(animate);
    };

    animId = requestAnimationFrame(animate);

    // Keyboard dismiss on Escape or any key
    const handleKeyDown = (e: KeyboardEvent) => {
      setAmbientMode(false);
    };

    window.addEventListener('keydown', handleKeyDown);

    return () => {
      cancelAnimationFrame(animId);
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [ambientMode, setAmbientMode]);

  if (!ambientMode) return null;

  // Format digital hours and minutes
  const hours = currentTime.getHours().toString().padStart(2, '0');
  const minutes = currentTime.getMinutes().toString().padStart(2, '0');
  const seconds = currentTime.getSeconds().toString().padStart(2, '0');

  const dateString = currentTime.toLocaleDateString(undefined, {
    weekday: 'long',
    month: 'short',
    day: 'numeric'
  });

  return (
    <div
      onClick={() => setAmbientMode(false)}
      className="fixed inset-0 z-50 bg-black select-none cursor-pointer overflow-hidden"
      title="Tap anywhere to exit Ambient Mode"
    >
      {/* Floating Billiard Bouncing Clock */}
      <div
        ref={boxRef}
        className="absolute top-0 left-0 will-change-transform flex flex-col items-start px-4 py-2"
        style={{ transform: `translate3d(${physicsRef.current.x}px, ${physicsRef.current.y}px, 0)` }}
      >
        {/* Large Digital Clock */}
        <div className="flex items-baseline font-mono tracking-tighter text-neutral-100 opacity-90 drop-shadow-md">
          <span className="text-6xl sm:text-7xl md:text-8xl font-light tabular-nums leading-none">
            {hours}:{minutes}
          </span>
          <span className="text-xl sm:text-2xl md:text-3xl font-light text-neutral-500 ml-2 tabular-nums">
            :{seconds}
          </span>
        </div>

        {/* Date string */}
        <div className="text-sm sm:text-base md:text-lg font-light text-neutral-400 tracking-wide mt-1 pl-1">
          {dateString}
        </div>
      </div>
    </div>
  );
};
