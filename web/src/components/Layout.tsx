import { Outlet } from "react-router";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { CommandPalette } from "./CommandPalette";

export function Layout() {
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
