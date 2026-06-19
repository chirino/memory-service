import { createRootRoute, Outlet } from "@tanstack/react-router";
import { RequireAuth } from "@/lib/auth";
import { Sidebar } from "@/components/layout/sidebar";

export const Route = createRootRoute({
  component: RootComponent,
});

function RootComponent() {
  return (
    <RequireAuth roles={["admin", "auditor"]}>
      <div className="flex h-screen">
        <Sidebar />
        <main className="flex-1 overflow-auto">
          <Outlet />
        </main>
      </div>
    </RequireAuth>
  );
}

// Made with Bob
