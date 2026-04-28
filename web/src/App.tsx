import {
  BrowserRouter,
  Routes,
  Route,
  Navigate,
  useParams,
} from "react-router";
import { Layout } from "./components/Layout";
import Start from "./pages/Start";
import StartSetup from "./pages/StartSetup";
import GettingStarted from "./pages/GettingStarted";
import Devices from "./pages/Devices";
import InviteDevice from "./pages/InviteDevice";
import Agents from "./pages/Agents";
import AgentChat from "./pages/AgentChat";
import AgentConnect from "./pages/AgentConnect";
import AgentCreate from "./pages/AgentCreate";
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
import SettingsCodex from "./pages/SettingsCodex";
import SettingsMessaging from "./pages/SettingsMessaging";
import SettingsSecrets from "./pages/SettingsSecrets";
import SettingsServices from "./pages/SettingsServices";
import SettingsVisuals from "./pages/SettingsVisuals";
import CodexChat from "./pages/CodexChat";
import Wallet from "./pages/Wallet";

function RootRedirect() {
  return <Navigate to="/agents" replace />;
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
          <Route index element={<RootRedirect />} />
          <Route path="start" element={<Start />} />
          <Route path="start/setup" element={<StartSetup />} />
          <Route path="codex" element={<CodexChat />} />
          <Route path="getting-started" element={<GettingStarted />} />
          <Route path="agents" element={<Agents />} />
          <Route path="agents/create" element={<AgentCreate />} />
          <Route path="agents/connect" element={<AgentConnect />} />
          <Route path="agents/:agentId" element={<AgentChat />} />
          <Route path="drives" element={<Drives />} />
          <Route path="drives/:name/*" element={<FileBrowser />} />
          <Route path="bucket/*" element={<Bucket />} />
          <Route path="devices" element={<Navigate replace to="/settings/devices" />} />
          <Route path="devices/invite" element={<Navigate replace to="/settings/devices/invite" />} />
          <Route path="wallet" element={<Navigate replace to="/settings/wallet" />} />
          <Route path="mailbox" element={<Navigate replace to="/settings/mailbox" />} />
          <Route path="sandboxes" element={<Navigate replace to="/settings/sandboxes" />} />
          <Route path="sandboxes/:name" element={<SandboxLegacyRedirect />} />
          <Route path="network" element={<Navigate replace to="/settings/network" />} />
          <Route path="kv" element={<Navigate replace to="/settings/kv" />} />
          <Route path="activity" element={<Navigate replace to="/settings/activity" />} />
          <Route path="settings/mailbox" element={<Mailbox />} />
          <Route path="settings/network" element={<Network />} />
          <Route path="settings/kv" element={<KVStore />} />
          <Route path="settings/activity" element={<Activity />} />
          <Route path="settings/apps" element={<SettingsApps />} />
          <Route path="settings/codex" element={<SettingsCodex />} />
          <Route path="settings/messaging" element={<SettingsMessaging />} />
          <Route path="settings/devices" element={<Devices />} />
          <Route path="settings/devices/invite" element={<InviteDevice />} />
          <Route path="settings/secrets" element={<SettingsSecrets />} />
          <Route path="settings/services" element={<SettingsServices />} />
          <Route path="settings/visuals" element={<SettingsVisuals />} />
          <Route path="settings/wallet" element={<Wallet />} />
          <Route path="settings/sandboxes" element={<Sandboxes />} />
          <Route path="settings/sandboxes/:slug" element={<SandboxDetail />} />
          <Route path="settings" element={<Settings />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
