import { Outlet, useLocation } from "react-router";
import { useEffect } from "react";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { CommandPalette } from "./CommandPalette";

export function Layout() {
  const location = useLocation();
  const isStartRoute = location.pathname.startsWith("/start");

  useEffect(() => {
    if (!location.hash) return;
    const id = decodeURIComponent(location.hash.slice(1));
    const frame = window.requestAnimationFrame(() => {
      document
        .getElementById(id)
        ?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [location.hash, location.pathname]);

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
      <div className="flex min-h-screen bg-surface text-on-surface">
        <Sidebar />
        <div className="relative flex min-h-screen min-w-0 flex-1 flex-col">
          <div className="pointer-events-none absolute right-6 top-5 z-30 sm:right-8 lg:right-10">
            <div className="pointer-events-auto">
              <Header />
            </div>
          </div>
          <main className="flex min-h-0 flex-1 flex-col">
            <Outlet />
          </main>
        </div>
      </div>
      <CommandPalette />
    </>
  );
}
