import React, { useState, useRef, useEffect } from 'react';
import { ChevronDown, Check } from 'lucide-react';

export interface SelectOption {
  value: string;
  label: string;
  sublabel?: string;
  badge?: string;
  icon?: React.ReactNode;
}

interface CustomSelectProps {
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  /** Hide the selected option's sublabel on the closed trigger so it is as tall as a text input; the list still shows it. */
  compactTrigger?: boolean;
}

export const CustomSelect: React.FC<CustomSelectProps> = ({
  value,
  onChange,
  options,
  placeholder = 'Select an option...',
  disabled = false,
  className = '',
  compactTrigger = false
}) => {
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const selectedOption = options.find(opt => opt.value === value);

  // Close when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };

    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
    }
    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
    };
  }, [isOpen]);

  // Close on Escape key
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        setIsOpen(false);
      }
    };
    if (isOpen) {
      window.addEventListener('keydown', handleKeyDown);
    }
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [isOpen]);

  return (
    <div className={`relative ${className}`} ref={containerRef}>
      {/* Trigger Button */}
      <button
        type="button"
        disabled={disabled}
        onClick={() => setIsOpen(prev => !prev)}
        aria-haspopup="listbox"
        aria-expanded={isOpen}
        className={`w-full flex items-center justify-between gap-2 px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border rounded-lg text-xs font-mono transition-colors text-left ${
          isOpen
            ? 'border-emerald-500 ring-1 ring-emerald-500/30'
            : 'border-neutral-200 dark:border-neutral-700 hover:border-neutral-300 dark:hover:border-neutral-600'
        } ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}
      >
        <div className="flex items-center gap-2 min-w-0 truncate">
          {selectedOption?.icon}
          <div className="truncate">
            <span className="text-neutral-900 dark:text-neutral-100 font-medium truncate block">
              {selectedOption ? selectedOption.label : placeholder}
            </span>
            {selectedOption?.sublabel && !compactTrigger && (
              <span className="text-[10px] text-neutral-400 font-sans block truncate">
                {selectedOption.sublabel}
              </span>
            )}
          </div>
        </div>

        <div className="flex items-center gap-1.5 shrink-0 ml-1">
          {selectedOption?.badge && (
            <span className="text-[10px] font-sans px-1.5 py-0.2 rounded bg-neutral-200 dark:bg-neutral-700 text-neutral-700 dark:text-neutral-300">
              {selectedOption.badge}
            </span>
          )}
          <ChevronDown
            size={14}
            className={`text-neutral-400 transition-transform duration-150 ${
              isOpen ? 'rotate-180 text-emerald-500' : ''
            }`}
          />
        </div>
      </button>

      {/* Dropdown Menu Popover */}
      {isOpen && (
        <div
          role="listbox"
          className="absolute z-40 left-0 right-0 mt-1 max-h-60 overflow-y-auto bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-700 rounded-lg shadow-xl py-1 text-xs animate-in fade-in zoom-in-95 duration-100"
        >
          {options.map(option => {
            const isSelected = option.value === value;
            return (
              <button
                key={option.value}
                type="button"
                role="option"
                aria-selected={isSelected}
                onClick={() => {
                  onChange(option.value);
                  setIsOpen(false);
                }}
                className={`w-full flex items-center justify-between px-3 py-2 text-left transition-colors font-mono ${
                  isSelected
                    ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 font-semibold'
                    : 'text-neutral-700 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800'
                }`}
              >
                <div className="flex items-center gap-2 min-w-0 truncate">
                  {option.icon}
                  <div className="truncate">
                    <span className="block truncate">{option.label}</span>
                    {option.sublabel && (
                      <span className="text-[10px] text-neutral-400 font-sans block truncate">
                        {option.sublabel}
                      </span>
                    )}
                  </div>
                </div>

                <div className="flex items-center gap-2 shrink-0 ml-2">
                  {option.badge && (
                    <span className="text-[10px] font-sans px-1.5 py-0.2 rounded bg-neutral-100 dark:bg-neutral-800 text-neutral-600 dark:text-neutral-400">
                      {option.badge}
                    </span>
                  )}
                  {isSelected && <Check size={14} className="text-emerald-500 shrink-0" />}
                </div>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
};
