import { Link, useRouterState } from "@tanstack/react-router";
import { Brain, MessageCircle, Search, LogOut, ChevronDown, Sprout } from "lucide-react";
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
  { to: "/conversations", label: "Conversations", icon: MessageCircle },
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
    <aside className="console-sidebar flex w-full shrink-0 flex-col md:w-[260px]">
      <div className="px-4 py-4 md:px-5 md:pb-10 md:pt-9">
        <div className="flex items-start gap-3">
          <Sprout className="mt-1 h-7 w-7 text-primary" strokeWidth={1.35} />
          <div>
            <h1 className="console-title text-xl text-foreground">Memory Service</h1>
            <p className="console-subtitle mt-1 text-sm">Developer Console</p>
          </div>
        </div>
      </div>

      <nav className="flex gap-1 px-3 pb-3 md:flex-1 md:flex-col md:gap-2 md:space-y-3 md:px-4 md:pb-0">
        {navItems.map((item) => {
          const isActive = currentPath.startsWith(item.to);
          const Icon = item.icon;

          return (
            <Link
              key={item.to}
              to={item.to}
              className={cn(
                "flex min-w-0 shrink items-center gap-1.5 rounded-lg px-2.5 py-3 text-xs font-medium transition-colors md:shrink-0 md:gap-4 md:px-4 md:py-4 md:text-sm",
                isActive
                  ? "bg-sage-soft/55 text-primary shadow-[0_1px_0_rgba(43,39,34,0.03)]"
                  : "text-stone hover:bg-white/55 hover:text-foreground",
              )}
            >
              <Icon className="h-5 w-5" strokeWidth={1.55} />
              <span>{item.label}</span>
            </Link>
          );
        })}
      </nav>

      <div className="m-4 hidden border-t border-[rgba(43,39,34,0.12)] pt-6 md:block">
        <DropdownMenu>
          <DropdownMenuTrigger className="w-full">
            <div className="flex items-center gap-3 rounded-xl px-1 py-2 transition-colors hover:bg-white/55">
              <div className="flex h-11 w-11 items-center justify-center rounded-full bg-sage-soft text-sm font-semibold text-primary">
                {getInitials()}
              </div>
              <div className="min-w-0 flex-1 text-left">
                <p className="truncate text-sm font-medium text-foreground">
                  {(auth.user?.profile?.name as string) || (auth.user?.profile?.email as string) || "User"}
                </p>
                <p className="text-xs text-muted-foreground">{roleLabel}</p>
              </div>
              <ChevronDown className="h-4 w-4 text-stone" strokeWidth={1.6} />
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
