import { Outlet, useLocation } from "react-router";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { CommandPalette } from "./CommandPalette";

export function Layout() {
  const location = useLocation();
  const isStartRoute = location.pathname === "/start";

  if (isStartRoute) {
    return (
      <>
        <main className="min-h-screen bg-surface text-on-surface">
          <Outlet />
        </main>
        <CommandPalette />
      </>
    );
  }

  return (
    <>
      <Sidebar />
      <main className="ml-64 flex min-h-screen flex-col bg-surface text-on-surface">
        <Header />
        <Outlet />
      </main>
      <CommandPalette />
    </>
  );
}
