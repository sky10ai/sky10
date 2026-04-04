import { BrowserRouter, Routes, Route, Navigate } from "react-router";
import { Layout } from "./components/Layout";
import GettingStarted from "./pages/GettingStarted";
import Devices from "./pages/Devices";
import InviteDevice from "./pages/InviteDevice";
import Agents from "./pages/Agents";
import Network from "./pages/Network";
import KVStore from "./pages/KVStore";
import Drives from "./pages/Drives";
import FileBrowser from "./pages/FileBrowser";
import Bucket from "./pages/Bucket";
import Activity from "./pages/Activity";
import Settings from "./pages/Settings";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Navigate to="/getting-started" replace />} />
          <Route path="getting-started" element={<GettingStarted />} />
          <Route path="devices" element={<Devices />} />
          <Route path="devices/invite" element={<InviteDevice />} />
          <Route path="agents" element={<Agents />} />
          <Route path="network" element={<Network />} />
          <Route path="kv" element={<KVStore />} />
          <Route path="drives" element={<Drives />} />
          <Route path="drives/:name/*" element={<FileBrowser />} />
          <Route path="bucket/*" element={<Bucket />} />
          <Route path="activity" element={<Activity />} />
          <Route path="settings" element={<Settings />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
