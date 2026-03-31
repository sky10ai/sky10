import { Outlet } from "react-router";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { CommandPalette } from "./CommandPalette";

export function Layout() {
  return (
    <>
      <Sidebar />
      <main className="ml-64 min-h-screen flex flex-col">
        <Header />
        <Outlet />
      </main>
      <CommandPalette />
    </>
  );
}
