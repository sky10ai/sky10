import { BrowserRouter, Routes, Route, Navigate, useParams } from "react-router";
import { Layout } from "./components/Layout";
import { identity } from "./lib/rpc";
import { useRPC } from "./lib/useRPC";
import GettingStarted from "./pages/GettingStarted";
import Devices from "./pages/Devices";
import InviteDevice from "./pages/InviteDevice";
import Agents from "./pages/Agents";
import AgentChat from "./pages/AgentChat";
import AgentConnect from "./pages/AgentConnect";
import Mailbox from "./pages/Mailbox";
import Network from "./pages/Network";
import Sandboxes from "./pages/Sandboxes";
import SandboxDetail from "./pages/SandboxDetail";
import KVStore from "./pages/KVStore";
import Drives from "./pages/Drives";
import FileBrowser from "./pages/FileBrowser";
import Bucket from "./pages/Bucket";
import Activity from "./pages/Activity";
import Settings from "./pages/Settings";
import SettingsApps from "./pages/SettingsApps";
import SettingsSecrets from "./pages/SettingsSecrets";

function HomeRedirect() {
  const { data } = useRPC(() => identity.deviceList(), []);
  const deviceCount = data?.devices?.length ?? 0;
  if (!data) return null;
  return <Navigate to={deviceCount >= 2 ? "/drives" : "/getting-started"} replace />;
}

function SandboxLegacyRedirect() {
  const params = useParams();
  const slug = params.name ? encodeURIComponent(params.name) : "";
  return <Navigate replace to={slug ? `/settings/sandboxes/${slug}` : "/settings/sandboxes"} />;
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<HomeRedirect />} />
          <Route path="getting-started" element={<GettingStarted />} />
          <Route path="devices" element={<Devices />} />
          <Route path="devices/invite" element={<InviteDevice />} />
          <Route path="agents" element={<Agents />} />
          <Route path="agents/create" element={<Navigate replace to="/settings/sandboxes?template=openclaw" />} />
          <Route path="agents/connect" element={<AgentConnect />} />
          <Route path="agents/:agentId" element={<AgentChat />} />
          <Route path="mailbox" element={<Mailbox />} />
          <Route path="sandboxes" element={<Navigate replace to="/settings/sandboxes" />} />
          <Route path="sandboxes/:name" element={<SandboxLegacyRedirect />} />
          <Route path="network" element={<Network />} />
          <Route path="kv" element={<KVStore />} />
          <Route path="drives" element={<Drives />} />
          <Route path="drives/:name/*" element={<FileBrowser />} />
          <Route path="bucket/*" element={<Bucket />} />
          <Route path="activity" element={<Activity />} />
          <Route path="settings/apps" element={<SettingsApps />} />
          <Route path="settings/secrets" element={<SettingsSecrets />} />
          <Route path="settings/sandboxes" element={<Sandboxes />} />
          <Route path="settings/sandboxes/:slug" element={<SandboxDetail />} />
          <Route path="settings" element={<Settings />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
