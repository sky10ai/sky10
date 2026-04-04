import { Icon } from "../components/Icon";

export default function Agents() {
  return (
    <div className="flex-1 flex items-center justify-center p-8">
      <div className="text-center space-y-4 max-w-sm">
        <Icon name="smart_toy" className="text-5xl text-secondary" />
        <h1 className="text-2xl font-bold text-on-surface">Agents</h1>
        <p className="text-secondary">
          Agent coordination is coming soon. Agents will discover each other
          over the P2P network and collaborate on tasks.
        </p>
      </div>
    </div>
  );
}
