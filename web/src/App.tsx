import { BrowserRouter, Routes, Route, Navigate } from "react-router";
import { Layout } from "./components/Layout";
import Drives from "./pages/Drives";
import FileBrowser from "./pages/FileBrowser";
import KVStore from "./pages/KVStore";
import Devices from "./pages/Devices";
import InviteDevice from "./pages/InviteDevice";
import Network from "./pages/Network";
import Settings from "./pages/Settings";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Navigate to="/drives" replace />} />
          <Route path="drives" element={<Drives />} />
          <Route path="drives/:name/*" element={<FileBrowser />} />
          <Route path="kv" element={<KVStore />} />
          <Route path="devices" element={<Devices />} />
          <Route path="devices/invite" element={<InviteDevice />} />
          <Route path="network" element={<Network />} />
          <Route path="settings" element={<Settings />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
