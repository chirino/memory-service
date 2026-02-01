import { useState, useRef, useEffect } from "react";
import { ChevronDown, Crown, Wrench, Pencil, Eye } from "lucide-react";
import type { AccessLevel } from "@/client";

type AccessLevelSelectProps = {
  value: AccessLevel;
  onChange: (level: AccessLevel) => void;
  allowedLevels: AccessLevel[];
  disabled?: boolean;
  /** Direction to open the dropdown. Defaults to "up" */
  openDirection?: "up" | "down";
};

const ACCESS_LEVEL_CONFIG: Record<AccessLevel, { label: string; description: string; icon: typeof Crown }> = {
  owner: {
    label: "Owner",
    description: "Full control",
    icon: Crown,
  },
  manager: {
    label: "Manager",
    description: "Can share with others",
    icon: Wrench,
  },
  writer: {
    label: "Writer",
    description: "Can send messages",
    icon: Pencil,
  },
  reader: {
    label: "Reader",
    description: "View only",
    icon: Eye,
  },
};

export function AccessLevelSelect({
  value,
  onChange,
  allowedLevels,
  disabled = false,
  openDirection = "up",
}: AccessLevelSelectProps) {
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // Close dropdown when clicking outside
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    }

    if (isOpen) {
      document.addEventListener("mousedown", handleClickOutside);
      return () => {
        document.removeEventListener("mousedown", handleClickOutside);
      };
    }
  }, [isOpen]);

  // Close on escape key
  useEffect(() => {
    function handleEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setIsOpen(false);
      }
    }

    if (isOpen) {
      document.addEventListener("keydown", handleEscape);
      return () => {
        document.removeEventListener("keydown", handleEscape);
      };
    }
  }, [isOpen]);

  const currentConfig = ACCESS_LEVEL_CONFIG[value];

  // If no allowed levels or only the current level, show as static text
  if (allowedLevels.length === 0 || disabled) {
    return <span className="flex items-center gap-1.5 text-sm text-stone">{currentConfig.label}</span>;
  }

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className="flex items-center gap-1 rounded-lg border border-stone/20 bg-cream px-2.5 py-1.5 text-sm text-ink transition-colors hover:border-stone/40 hover:bg-mist"
        aria-haspopup="listbox"
        aria-expanded={isOpen}
      >
        <span>{currentConfig.label}</span>
        <ChevronDown className={`h-3.5 w-3.5 text-stone transition-transform ${isOpen ? "rotate-180" : ""}`} />
      </button>

      {isOpen && (
        <div
          className={`absolute right-0 z-50 w-48 overflow-hidden rounded-xl border border-stone/20 bg-cream shadow-xl ${
            openDirection === "up" ? "bottom-full mb-1" : "top-full mt-1"
          }`}
        >
          <div className="py-1" role="listbox">
            {allowedLevels.map((level) => {
              const config = ACCESS_LEVEL_CONFIG[level];
              const Icon = config.icon;
              const isSelected = level === value;

              return (
                <button
                  key={level}
                  type="button"
                  role="option"
                  aria-selected={isSelected}
                  onClick={() => {
                    onChange(level);
                    setIsOpen(false);
                  }}
                  className={`flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors ${
                    isSelected ? "bg-mist/50" : "hover:bg-mist/50"
                  }`}
                >
                  <Icon
                    className={`h-4 w-4 ${
                      level === "owner" ? "text-terracotta" : level === "manager" ? "text-sage" : "text-stone"
                    }`}
                  />
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium text-ink">{config.label}</p>
                    <p className="text-xs text-stone">{config.description}</p>
                  </div>
                  {isSelected && (
                    <span className="text-sage">
                      <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                      </svg>
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * Static display of an access level (for read-only views)
 */
export function AccessLevelBadge({ level, showIcon = true }: { level: AccessLevel; showIcon?: boolean }) {
  const config = ACCESS_LEVEL_CONFIG[level];
  const Icon = config.icon;

  return (
    <span
      className={`flex items-center gap-1.5 text-sm ${
        level === "owner" ? "text-terracotta" : level === "manager" ? "text-sage" : "text-stone"
      }`}
    >
      {showIcon && <Icon className="h-4 w-4" />}
      <span>{config.label}</span>
    </span>
  );
}
