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
      document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
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
      <Sidebar />
      <main className="ml-64 flex min-h-screen flex-col bg-surface text-on-surface">
        <Header />
        <Outlet />
      </main>
      <CommandPalette />
    </>
  );
}
