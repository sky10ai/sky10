import { tool, type InferToolInput, type InferToolOutput, type Tool, type ToolSet } from "ai";
import { z } from "zod";
import {
  agent,
  apps,
  identity,
  sandbox,
  secrets,
  skyfs,
  skykv,
  skylink,
  system,
  wallet,
  type AgentListResult,
  type DeviceListResult,
  type DriveListResult,
  type HealthResult,
  type LinkStatus,
  type SandboxListResult,
  type SyncActivityResult,
} from "./rpc";

export type RootAssistantToolPolicy =
  | "approval_required"
  | "disabled"
  | "read_only";

export interface RootAssistantToolMetadata {
  policy: RootAssistantToolPolicy;
  risk: "low" | "medium" | "high";
  rpcMethods: readonly string[];
  title: string;
}

const emptySchema = z.object({}).strict();

function approvalRequiredTool<INPUT, OUTPUT>(config: Tool<INPUT, OUTPUT>): Tool<INPUT, OUTPUT> {
  return tool<INPUT, OUTPUT>({
    ...config,
    needsApproval: true,
  } as Tool<INPUT, OUTPUT>);
}

export const rootAssistantTools = {
  daemon_getHealth: tool({
    description: "Read live daemon health, storage counters, and version details.",
    inputSchema: emptySchema,
    execute: () => system.health(),
  }),
  drives_list: tool({
    description: "List configured drives and their sync health.",
    inputSchema: emptySchema,
    execute: () => skyfs.driveList(),
  }),
  drives_create: approvalRequiredTool({
    description: "Create a new sky10 drive at an explicit local path.",
    inputSchema: z.object({
      name: z.string().min(1).describe("Drive name."),
      path: z.string().min(1).describe("Local folder path for the drive."),
    }).strict(),
    execute: (input) => skyfs.driveCreate(input),
  }),
  drives_start: approvalRequiredTool({
    description: "Start syncing an existing drive.",
    inputSchema: z.object({
      name: z.string().min(1).describe("Drive name."),
    }).strict(),
    execute: (input) => skyfs.driveStart(input),
  }),
  drives_stop: approvalRequiredTool({
    description: "Stop syncing an existing drive.",
    inputSchema: z.object({
      name: z.string().min(1).describe("Drive name."),
    }).strict(),
    execute: (input) => skyfs.driveStop(input),
  }),
  drives_remove: approvalRequiredTool({
    description: "Remove a configured drive from sky10 without deleting arbitrary local files.",
    inputSchema: z.object({
      name: z.string().min(1).describe("Drive name."),
    }).strict(),
    execute: (input) => skyfs.driveRemove(input),
  }),
  files_mkdir: approvalRequiredTool({
    description: "Create a folder in a configured sky10 drive.",
    inputSchema: z.object({
      drive: z.string().min(1).describe("Drive name."),
      path: z.string().min(1).describe("Folder path within the drive."),
    }).strict(),
    execute: (input) => skyfs.mkdir(input),
  }),
  files_remove: approvalRequiredTool({
    description: "Remove a file or folder from a configured sky10 drive.",
    inputSchema: z.object({
      drive: z.string().min(1).describe("Drive name."),
      path: z.string().min(1).describe("Path within the drive."),
    }).strict(),
    execute: (input) => skyfs.remove(input),
  }),
  sync_activity: tool({
    description: "Inspect pending transfers, conflicts, and path issues.",
    inputSchema: emptySchema,
    execute: () => skyfs.syncActivity(),
  }),
  kv_status: tool({
    description: "Inspect the user KV namespace sync status.",
    inputSchema: emptySchema,
    execute: () => skykv.status(),
  }),
  kv_listUserKeys: tool({
    description: "List user KV keys without exposing internal namespaces.",
    inputSchema: z.object({
      namespace: z.string().optional().describe("Optional user namespace."),
      prefix: z.string().optional().describe("Optional key prefix."),
    }).strict(),
    execute: (input) => skykv.list({ ...input, include_internal: false }),
  }),
  network_getStatus: tool({
    description: "Read skylink peer connectivity and delivery health.",
    inputSchema: emptySchema,
    execute: () => skylink.status(),
  }),
  network_connectPeer: approvalRequiredTool({
    description: "Connect this node to an explicit skylink peer address.",
    inputSchema: z.object({
      address: z.string().min(1).describe("Peer multiaddr or supported address."),
    }).strict(),
    execute: (input) => skylink.connect(input),
  }),
  devices_list: tool({
    description: "List current identity device membership.",
    inputSchema: emptySchema,
    execute: () => identity.deviceList(),
  }),
  devices_invite: approvalRequiredTool({
    description: "Create a device invite code.",
    inputSchema: emptySchema,
    execute: () => identity.invite(),
  }),
  devices_join: approvalRequiredTool({
    description: "Join another sky10 identity using an invite code.",
    inputSchema: z.object({
      code: z.string().min(1).describe("Invite code."),
    }).strict(),
    execute: (input) => identity.join(input),
  }),
  devices_approve: approvalRequiredTool({
    description: "Approve pending device membership changes.",
    inputSchema: emptySchema,
    execute: () => identity.approve(),
  }),
  devices_remove: approvalRequiredTool({
    description: "Remove a device by public key.",
    inputSchema: z.object({
      pubkey: z.string().min(1).describe("Device public key."),
    }).strict(),
    execute: (input) => identity.deviceRemove(input),
  }),
  secrets_list: tool({
    description: "List secret summaries without revealing payloads.",
    inputSchema: emptySchema,
    execute: () => secrets.list(),
  }),
  secrets_status: tool({
    description: "Read secret store status.",
    inputSchema: emptySchema,
    execute: () => secrets.status(),
  }),
  secrets_put: approvalRequiredTool({
    description: "Store or update a secret payload.",
    inputSchema: z.object({
      name: z.string().min(1).describe("Secret name."),
      payload: z.string().min(1).describe("Secret value."),
      content_type: z.string().optional().describe("Optional content type."),
      kind: z.string().optional().describe("Secret kind such as api-key."),
      recipient_devices: z.array(z.string()).optional().describe("Explicit recipient device IDs."),
      scope: z.enum(["current", "trusted", "explicit"]).optional().describe("Secret sharing scope."),
    }).strict(),
    execute: (input) => secrets.put(input),
  }),
  secrets_delete: approvalRequiredTool({
    description: "Delete a stored secret.",
    inputSchema: z.object({
      id_or_name: z.string().min(1).describe("Secret ID or name."),
    }).strict(),
    execute: (input) => secrets.delete(input),
  }),
  secrets_rewrap: approvalRequiredTool({
    description: "Change which devices can decrypt an existing secret.",
    inputSchema: z.object({
      id_or_name: z.string().min(1).describe("Secret ID or name."),
      recipient_devices: z.array(z.string()).optional().describe("Explicit recipient device IDs."),
      scope: z.enum(["current", "trusted", "explicit"]).optional().describe("Secret sharing scope."),
    }).strict(),
    execute: (input) => secrets.rewrap(input),
  }),
  secrets_sync: approvalRequiredTool({
    description: "Trigger secret synchronization.",
    inputSchema: emptySchema,
    execute: () => secrets.sync(),
  }),
  agents_list: tool({
    description: "List registered agents.",
    inputSchema: emptySchema,
    execute: () => agent.list(),
  }),
  agents_status: tool({
    description: "Read aggregate agent runtime and delivery policy status.",
    inputSchema: emptySchema,
    execute: () => agent.status(),
  }),
  agents_sendMessage: approvalRequiredTool({
    description: "Send a message to a registered agent.",
    inputSchema: z.object({
      content: z.unknown().describe("Message content."),
      device_id: z.string().optional().describe("Optional target device ID."),
      session_id: z.string().min(1).describe("Conversation session ID."),
      to: z.string().min(1).describe("Agent ID."),
      type: z.string().min(1).describe("Message type."),
    }).strict(),
    execute: (input) => agent.send(input),
  }),
  sandboxes_list: tool({
    description: "List managed sandboxes.",
    inputSchema: emptySchema,
    execute: () => sandbox.list(),
  }),
  sandboxes_get: tool({
    description: "Inspect a single managed sandbox.",
    inputSchema: z.object({
      name: z.string().optional().describe("Sandbox display name."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.get(input),
  }),
  sandboxes_logs: tool({
    description: "Read recent sandbox logs.",
    inputSchema: z.object({
      limit: z.number().int().positive().max(500).optional().describe("Maximum log entries."),
      name: z.string().optional().describe("Sandbox display name."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.logs(input),
  }),
  sandboxes_create: approvalRequiredTool({
    description: "Create a managed sandbox from a template.",
    inputSchema: z.object({
      name: z.string().min(1).describe("Sandbox display name."),
      provider: z.string().min(1).describe("Runtime provider."),
      secret_bindings: z.array(
        z.object({
          env: z.string().min(1).describe("Environment variable name inside the sandbox."),
          secret: z.string().min(1).describe("Stored sky10 secret name or ID."),
        }).strict(),
      )
        .optional()
        .describe("Optional secrets to project before the sandbox boots."),
      template: z.string().min(1).describe("Sandbox template."),
    }).strict(),
    execute: (input) => sandbox.create(input),
  }),
  sandboxes_start: approvalRequiredTool({
    description: "Start a managed sandbox.",
    inputSchema: z.object({
      name: z.string().optional().describe("Sandbox display name."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.start(input),
  }),
  sandboxes_stop: approvalRequiredTool({
    description: "Stop a managed sandbox.",
    inputSchema: z.object({
      name: z.string().optional().describe("Sandbox display name."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.stop(input),
  }),
  sandboxes_delete: approvalRequiredTool({
    description: "Delete a managed sandbox.",
    inputSchema: z.object({
      name: z.string().optional().describe("Sandbox display name."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.delete(input),
  }),
  sandboxes_attachSecret: approvalRequiredTool({
    description: "Attach a stored sky10 secret to a managed sandbox as an environment variable.",
    inputSchema: z.object({
      env: z.string().min(1).describe("Environment variable name inside the sandbox."),
      name: z.string().optional().describe("Sandbox display name."),
      secret: z.string().min(1).describe("Secret name or ID."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.secrets.attach(input),
  }),
  sandboxes_detachSecret: approvalRequiredTool({
    description: "Detach a sandbox environment variable from its stored sky10 secret binding.",
    inputSchema: z.object({
      env: z.string().min(1).describe("Environment variable name inside the sandbox."),
      name: z.string().optional().describe("Sandbox display name."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.secrets.detach(input),
  }),
  sandboxes_syncSecrets: approvalRequiredTool({
    description: "Regenerate a sandbox's projected secret environment file from its secret bindings.",
    inputSchema: z.object({
      name: z.string().optional().describe("Sandbox display name."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.secrets.sync(input),
  }),
  sandboxes_runtimeUpgrade: approvalRequiredTool({
    description: "Upgrade the sky10 runtime inside a managed sandbox.",
    inputSchema: z.object({
      name: z.string().optional().describe("Sandbox display name."),
      slug: z.string().optional().describe("Sandbox slug."),
    }).strict(),
    execute: (input) => sandbox.runtime.upgrade(input),
  }),
  apps_status: tool({
    description: "Inspect a managed app install status.",
    inputSchema: z.object({
      id: z.string().min(1).describe("Managed app ID."),
    }).strict(),
    execute: (input) => apps.status(input),
  }),
  apps_checkUpdate: tool({
    description: "Check whether a managed app has an update.",
    inputSchema: z.object({
      id: z.string().min(1).describe("Managed app ID."),
    }).strict(),
    execute: (input) => apps.checkUpdate(input),
  }),
  apps_install: approvalRequiredTool({
    description: "Install a managed app.",
    inputSchema: z.object({
      id: z.string().min(1).describe("Managed app ID."),
    }).strict(),
    execute: (input) => apps.install(input),
  }),
  apps_uninstall: approvalRequiredTool({
    description: "Uninstall a managed app.",
    inputSchema: z.object({
      id: z.string().min(1).describe("Managed app ID."),
    }).strict(),
    execute: (input) => apps.uninstall(input),
  }),
  system_updateCheck: tool({
    description: "Check whether sky10 updates are available.",
    inputSchema: emptySchema,
    execute: () => system.update.check(),
  }),
  system_updateStatus: tool({
    description: "Read staged update status.",
    inputSchema: emptySchema,
    execute: () => system.update.status(),
  }),
  system_updateDownload: approvalRequiredTool({
    description: "Download a sky10 update for later installation.",
    inputSchema: emptySchema,
    execute: () => system.update.download(),
  }),
  system_updateInstall: approvalRequiredTool({
    description: "Install a staged sky10 update.",
    inputSchema: emptySchema,
    execute: () => system.update.install(),
  }),
  system_restart: approvalRequiredTool({
    description: "Restart the local sky10 daemon.",
    inputSchema: emptySchema,
    execute: () => system.restart(),
  }),
  wallet_status: tool({
    description: "Inspect wallet manager status.",
    inputSchema: emptySchema,
    execute: () => wallet.status(),
  }),
  wallet_list: tool({
    description: "List local wallets.",
    inputSchema: emptySchema,
    execute: () => wallet.list(),
  }),
  wallet_address: tool({
    description: "Read a wallet address for an optional chain.",
    inputSchema: z.object({
      chain: z.string().optional().describe("Optional chain ID."),
      wallet: z.string().min(1).describe("Wallet name."),
    }).strict(),
    execute: (input) => wallet.address(input),
  }),
  wallet_balance: tool({
    description: "Read wallet balances.",
    inputSchema: z.object({
      chain: z.string().optional().describe("Optional chain ID."),
      wallet: z.string().min(1).describe("Wallet name."),
    }).strict(),
    execute: (input) => wallet.balance(input),
  }),
  wallet_deposit: tool({
    description: "Get a deposit address or URL for a wallet.",
    inputSchema: z.object({
      chain: z.string().optional().describe("Optional chain ID."),
      wallet: z.string().min(1).describe("Wallet name."),
    }).strict(),
    execute: (input) => wallet.deposit(input),
  }),
  wallet_install: approvalRequiredTool({
    description: "Install the managed wallet binary.",
    inputSchema: emptySchema,
    execute: () => wallet.install(),
  }),
  wallet_uninstall: approvalRequiredTool({
    description: "Uninstall the managed wallet binary.",
    inputSchema: emptySchema,
    execute: () => wallet.uninstall(),
  }),
  wallet_create: approvalRequiredTool({
    description: "Create a local wallet.",
    inputSchema: z.object({
      name: z.string().min(1).describe("Wallet name."),
    }).strict(),
    execute: (input) => wallet.create(input),
  }),
} satisfies ToolSet;

export type RootAssistantToolName = keyof typeof rootAssistantTools;
export type RootAssistantToolInput<T extends RootAssistantToolName> =
  InferToolInput<(typeof rootAssistantTools)[T]>;
export type RootAssistantToolOutput<T extends RootAssistantToolName> =
  InferToolOutput<(typeof rootAssistantTools)[T]>;

export const rootAssistantToolMetadata = {
  daemon_getHealth: { policy: "read_only", risk: "low", rpcMethods: ["system.health"], title: "Read daemon health" },
  drives_list: { policy: "read_only", risk: "low", rpcMethods: ["skyfs.driveList"], title: "List drives" },
  drives_create: { policy: "approval_required", risk: "medium", rpcMethods: ["skyfs.driveCreate"], title: "Create drive" },
  drives_start: { policy: "approval_required", risk: "medium", rpcMethods: ["skyfs.driveStart"], title: "Start drive" },
  drives_stop: { policy: "approval_required", risk: "medium", rpcMethods: ["skyfs.driveStop"], title: "Stop drive" },
  drives_remove: { policy: "approval_required", risk: "high", rpcMethods: ["skyfs.driveRemove"], title: "Remove drive" },
  files_mkdir: { policy: "approval_required", risk: "medium", rpcMethods: ["skyfs.mkdir"], title: "Create folder" },
  files_remove: { policy: "approval_required", risk: "high", rpcMethods: ["skyfs.remove"], title: "Remove file or folder" },
  sync_activity: { policy: "read_only", risk: "low", rpcMethods: ["skyfs.syncActivity"], title: "Read sync activity" },
  kv_status: { policy: "read_only", risk: "low", rpcMethods: ["skykv.status"], title: "Read KV status" },
  kv_listUserKeys: { policy: "read_only", risk: "medium", rpcMethods: ["skykv.list"], title: "List user KV keys" },
  network_getStatus: { policy: "read_only", risk: "low", rpcMethods: ["skylink.status"], title: "Read network status" },
  network_connectPeer: { policy: "approval_required", risk: "medium", rpcMethods: ["skylink.connect"], title: "Connect peer" },
  devices_list: { policy: "read_only", risk: "low", rpcMethods: ["identity.deviceList"], title: "List devices" },
  devices_invite: { policy: "approval_required", risk: "medium", rpcMethods: ["identity.invite"], title: "Create device invite" },
  devices_join: { policy: "approval_required", risk: "high", rpcMethods: ["identity.join"], title: "Join device" },
  devices_approve: { policy: "approval_required", risk: "medium", rpcMethods: ["identity.approve"], title: "Approve devices" },
  devices_remove: { policy: "approval_required", risk: "high", rpcMethods: ["identity.deviceRemove"], title: "Remove device" },
  secrets_list: { policy: "read_only", risk: "medium", rpcMethods: ["secrets.list"], title: "List secret summaries" },
  secrets_status: { policy: "read_only", risk: "low", rpcMethods: ["secrets.status"], title: "Read secret status" },
  secrets_put: { policy: "approval_required", risk: "high", rpcMethods: ["secrets.put"], title: "Store secret" },
  secrets_delete: { policy: "approval_required", risk: "high", rpcMethods: ["secrets.delete"], title: "Delete secret" },
  secrets_rewrap: { policy: "approval_required", risk: "high", rpcMethods: ["secrets.rewrap"], title: "Rewrap secret" },
  secrets_sync: { policy: "approval_required", risk: "medium", rpcMethods: ["secrets.sync"], title: "Sync secrets" },
  agents_list: { policy: "read_only", risk: "low", rpcMethods: ["agent.list"], title: "List agents" },
  agents_status: { policy: "read_only", risk: "low", rpcMethods: ["agent.status"], title: "Read agent status" },
  agents_sendMessage: { policy: "approval_required", risk: "medium", rpcMethods: ["agent.send"], title: "Send agent message" },
  sandboxes_list: { policy: "read_only", risk: "low", rpcMethods: ["sandbox.list"], title: "List sandboxes" },
  sandboxes_get: { policy: "read_only", risk: "low", rpcMethods: ["sandbox.get"], title: "Inspect sandbox" },
  sandboxes_logs: { policy: "read_only", risk: "medium", rpcMethods: ["sandbox.logs"], title: "Read sandbox logs" },
  sandboxes_create: { policy: "approval_required", risk: "high", rpcMethods: ["sandbox.create"], title: "Create sandbox" },
  sandboxes_start: { policy: "approval_required", risk: "medium", rpcMethods: ["sandbox.start"], title: "Start sandbox" },
  sandboxes_stop: { policy: "approval_required", risk: "medium", rpcMethods: ["sandbox.stop"], title: "Stop sandbox" },
  sandboxes_delete: { policy: "approval_required", risk: "high", rpcMethods: ["sandbox.delete"], title: "Delete sandbox" },
  sandboxes_attachSecret: { policy: "approval_required", risk: "high", rpcMethods: ["sandbox.secrets.attach"], title: "Attach sandbox secret" },
  sandboxes_detachSecret: { policy: "approval_required", risk: "high", rpcMethods: ["sandbox.secrets.detach"], title: "Detach sandbox secret" },
  sandboxes_syncSecrets: { policy: "approval_required", risk: "medium", rpcMethods: ["sandbox.secrets.sync"], title: "Sync sandbox secrets" },
  sandboxes_runtimeUpgrade: { policy: "approval_required", risk: "high", rpcMethods: ["sandbox.runtime.upgrade"], title: "Upgrade sandbox runtime" },
  apps_status: { policy: "read_only", risk: "low", rpcMethods: ["apps.status"], title: "Read app status" },
  apps_checkUpdate: { policy: "read_only", risk: "low", rpcMethods: ["apps.checkUpdate"], title: "Check app update" },
  apps_install: { policy: "approval_required", risk: "high", rpcMethods: ["apps.install"], title: "Install app" },
  apps_uninstall: { policy: "approval_required", risk: "high", rpcMethods: ["apps.uninstall"], title: "Uninstall app" },
  system_updateCheck: { policy: "read_only", risk: "low", rpcMethods: ["system.update.check"], title: "Check update" },
  system_updateStatus: { policy: "read_only", risk: "low", rpcMethods: ["system.update.status"], title: "Read update status" },
  system_updateDownload: { policy: "approval_required", risk: "medium", rpcMethods: ["system.update.download"], title: "Download update" },
  system_updateInstall: { policy: "approval_required", risk: "high", rpcMethods: ["system.update.install"], title: "Install update" },
  system_restart: { policy: "approval_required", risk: "high", rpcMethods: ["system.restart"], title: "Restart daemon" },
  wallet_status: { policy: "read_only", risk: "low", rpcMethods: ["wallet.status"], title: "Read wallet status" },
  wallet_list: { policy: "read_only", risk: "low", rpcMethods: ["wallet.list"], title: "List wallets" },
  wallet_address: { policy: "read_only", risk: "low", rpcMethods: ["wallet.address"], title: "Read wallet address" },
  wallet_balance: { policy: "read_only", risk: "medium", rpcMethods: ["wallet.balance"], title: "Read wallet balance" },
  wallet_deposit: { policy: "read_only", risk: "medium", rpcMethods: ["wallet.deposit"], title: "Read deposit details" },
  wallet_install: { policy: "approval_required", risk: "high", rpcMethods: ["wallet.install"], title: "Install wallet" },
  wallet_uninstall: { policy: "approval_required", risk: "high", rpcMethods: ["wallet.uninstall"], title: "Uninstall wallet" },
  wallet_create: { policy: "approval_required", risk: "medium", rpcMethods: ["wallet.create"], title: "Create wallet" },
} as const satisfies Record<RootAssistantToolName, RootAssistantToolMetadata>;

export const disabledRootAssistantRPCs = [
  "agent.mailbox.ack",
  "agent.mailbox.approve",
  "agent.mailbox.claim",
  "agent.mailbox.complete",
  "agent.mailbox.reject",
  "agent.mailbox.release",
  "agent.mailbox.retry",
  "skyfs.s3Delete",
  "skyfs.s3List",
  "skykv.deleteMatching",
  "wallet.transfer",
] as const;

export const rootAssistantToolNames = Object.keys(rootAssistantTools) as RootAssistantToolName[];
export const rootAssistantReadOnlyToolNames = rootAssistantToolNames.filter(
  (name) => rootAssistantToolMetadata[name].policy === "read_only"
);
export const rootAssistantApprovalRequiredToolNames = rootAssistantToolNames.filter(
  (name) => rootAssistantToolMetadata[name].policy === "approval_required"
);

export async function executeRootAssistantTool<OUTPUT = unknown>(
  name: RootAssistantToolName,
  input: unknown,
  options: { approved?: boolean } = {}
): Promise<OUTPUT> {
  const metadata = rootAssistantToolMetadata[name];
  if (metadata.policy === "approval_required" && !options.approved) {
    throw new Error(`${metadata.title} requires approval before execution.`);
  }

  const selectedTool = rootAssistantTools[name] as Tool<unknown, OUTPUT>;
  if (!selectedTool.execute) {
    throw new Error(`${metadata.title} cannot be executed directly.`);
  }

  const result = await selectedTool.execute(input, {
    messages: [],
    toolCallId: `root-assistant-${name}`,
  });
  return result as OUTPUT;
}

export const rootAssistantToolRunners = {
  agents_list: () => executeRootAssistantTool<AgentListResult>("agents_list", {}),
  daemon_getHealth: () => executeRootAssistantTool<HealthResult>("daemon_getHealth", {}),
  devices_list: () => executeRootAssistantTool<DeviceListResult>("devices_list", {}),
  drives_list: () => executeRootAssistantTool<DriveListResult>("drives_list", {}),
  network_getStatus: () => executeRootAssistantTool<LinkStatus>("network_getStatus", {}),
  sandboxes_list: () => executeRootAssistantTool<SandboxListResult>("sandboxes_list", {}),
  sync_activity: () => executeRootAssistantTool<SyncActivityResult>("sync_activity", {}),
};
