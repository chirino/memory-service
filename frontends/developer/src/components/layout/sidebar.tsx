import { Link, useRouterState } from "@tanstack/react-router";
import { Brain, MessageSquare, Search, LogOut, ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";
import { useAuth } from "@/lib/auth";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

const navItems = [
  { to: "/conversations", label: "Conversations", icon: MessageSquare },
  { to: "/memories", label: "Memories", icon: Brain },
  { to: "/search", label: "Search", icon: Search },
];

export function Sidebar() {
  const router = useRouterState();
  const auth = useAuth();
  const currentPath = router.location.pathname;

  const isAdmin = auth.hasRole("admin");
  const isAuditor = auth.hasRole("auditor");
  const roleLabel = isAdmin ? "Admin" : isAuditor ? "Auditor" : "User";

  // Get user initials for avatar
  const getInitials = () => {
    const profile = auth.user?.profile;
    const name = profile?.name as string | undefined;
    const email = profile?.email as string | undefined;

    if (name) {
      return name
        .split(" ")
        .map((n) => n[0])
        .join("")
        .toUpperCase()
        .slice(0, 2);
    }
    if (email) {
      return email.slice(0, 2).toUpperCase();
    }
    return "U";
  };

  return (
    <aside className="flex w-60 flex-col border-r border-border bg-background">
      {/* Sidebar Header */}
      <div className="border-b border-border p-4">
        <h1 className="text-lg font-semibold text-foreground">Memory Service</h1>
        <p className="text-sm text-muted-foreground">Developer Console</p>
      </div>

      {/* Navigation */}
      <nav className="flex-1 space-y-1 p-4">
        {navItems.map((item) => {
          const isActive = currentPath.startsWith(item.to);
          const Icon = item.icon;

          return (
            <Link
              key={item.to}
              to={item.to}
              className={cn(
                "flex items-center space-x-3 rounded-md px-3 py-2 transition-colors",
                isActive
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
              )}
            >
              <Icon className="h-5 w-5" strokeWidth={1.5} />
              <span className="text-sm font-medium">{item.label}</span>
            </Link>
          );
        })}
      </nav>

      {/* User Profile */}
      <div className="border-t border-border p-4">
        <DropdownMenu>
          <DropdownMenuTrigger className="w-full">
            <div className="flex items-center gap-3 rounded-md px-2 py-2 hover:bg-accent transition-colors">
              <div className="flex h-8 w-8 items-center justify-center rounded-full bg-primary text-sm font-medium text-primary-foreground">
                {getInitials()}
              </div>
              <div className="min-w-0 flex-1 text-left">
                <p className="truncate text-sm font-medium text-foreground">
                  {(auth.user?.profile?.name as string) || (auth.user?.profile?.email as string) || "User"}
                </p>
                <p className="text-xs text-muted-foreground">{roleLabel}</p>
              </div>
              <ChevronDown className="h-4 w-4 text-muted-foreground" />
            </div>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-56">
            <DropdownMenuLabel>
              <div className="flex flex-col space-y-1">
                <p className="text-sm font-medium">
                  {(auth.user?.profile?.name as string) || "User"}
                </p>
                <p className="text-xs text-muted-foreground">
                  {(auth.user?.profile?.email as string) || ""}
                </p>
              </div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={auth.logout} className="cursor-pointer">
              <LogOut className="mr-2 h-4 w-4" />
              <span>Sign Out</span>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </aside>
  );
}

// Made with Bob
