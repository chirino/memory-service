import { createRootRoute, Outlet } from "@tanstack/react-router";
import { RequireAuth } from "@/lib/auth";
import { Sidebar } from "@/components/layout/sidebar";

export const Route = createRootRoute({
  component: RootComponent,
});

function RootComponent() {
  return (
    <RequireAuth roles={["admin", "auditor"]}>
      <div className="console-shell flex h-screen flex-col text-foreground md:flex-row">
        <Sidebar />
        <main className="console-main min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
          <Outlet />
        </main>
      </div>
    </RequireAuth>
  );
}

// Made with Bob
