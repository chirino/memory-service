import { useState, useRef, useEffect } from "react";
import { LogOut } from "lucide-react";
import { useAuth, type AuthUser } from "@/lib/auth";

type UserAvatarProps = {
  user: AuthUser;
};

function getInitials(name?: string, email?: string, userId?: string): string {
  // Try to get initials from name first
  if (name) {
    const parts = name.trim().split(/\s+/);
    if (parts.length >= 2) {
      return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
    }
    return name.slice(0, 2).toUpperCase();
  }
  // Fall back to email
  if (email) {
    const localPart = email.split("@")[0];
    return localPart.slice(0, 2).toUpperCase();
  }
  // Fall back to userId
  if (userId) {
    return userId.slice(0, 2).toUpperCase();
  }
  return "??";
}

function getDisplayName(user: AuthUser): string {
  return user.name || user.email || user.userId;
}

export function UserAvatar({ user }: UserAvatarProps) {
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const auth = useAuth();

  const initials = getInitials(user.name, user.email, user.userId);
  const displayName = getDisplayName(user);

  // Close menu when clicking outside
  useEffect(() => {
    if (!menuOpen) return;

    function handleClickOutside(event: MouseEvent) {
      if (
        menuRef.current &&
        !menuRef.current.contains(event.target as Node) &&
        buttonRef.current &&
        !buttonRef.current.contains(event.target as Node)
      ) {
        setMenuOpen(false);
      }
    }

    document.addEventListener("mousedown", handleClickOutside);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
    };
  }, [menuOpen]);

  // Close menu on escape
  useEffect(() => {
    if (!menuOpen) return;

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setMenuOpen(false);
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [menuOpen]);

  const handleLogout = () => {
    setMenuOpen(false);
    auth.logout();
  };

  return (
    <div className="relative">
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setMenuOpen(!menuOpen)}
        className="flex h-9 w-9 items-center justify-center rounded-full bg-sage text-sm font-medium text-cream transition-all hover:bg-sage/80 focus:outline-none focus:ring-2 focus:ring-sage/50 focus:ring-offset-2 focus:ring-offset-cream"
        aria-label="User menu"
        aria-expanded={menuOpen}
        aria-haspopup="true"
      >
        {initials}
      </button>

      {menuOpen && (
        <div
          ref={menuRef}
          className="absolute right-0 top-full z-50 mt-2 w-64 animate-slide-up overflow-hidden rounded-xl border border-stone/20 bg-cream shadow-xl"
        >
          {/* User info section */}
          <div className="border-b border-stone/10 bg-mist/30 px-4 py-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-sage text-sm font-medium text-cream">
                {initials}
              </div>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium text-ink">{displayName}</p>
                {user.email && user.name && (
                  <p className="truncate text-xs text-stone">{user.email}</p>
                )}
              </div>
            </div>
          </div>

          {/* Menu items */}
          <div className="py-1">
            <button
              type="button"
              onClick={handleLogout}
              className="flex w-full items-center gap-3 px-4 py-3 text-left text-sm text-ink transition-colors hover:bg-mist"
            >
              <LogOut className="h-4 w-4 text-stone" />
              Log out
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
