/** sky10 daemon RPC and SSE client. */

export class Sky10Client {
  constructor(rpcUrl) {
    this.rpcUrl = rpcUrl.replace(/\/$/, "");
    this.nextId = 1;
  }

  async rpc(method, params) {
    const res = await fetch(`${this.rpcUrl}/rpc`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        method,
        params: params ?? {},
        id: this.nextId++,
      }),
    });
    const data = await res.json();
    if (data.error) throw new Error(`sky10 RPC ${method}: ${data.error.message}`);
    return data.result;
  }

  async register(name, skills) {
    return this.rpc("agent.register", { name, skills });
  }

  async send(to, sessionId, text, deviceId) {
    return this.rpc("agent.send", {
      to,
      device_id: deviceId,
      session_id: sessionId,
      type: "text",
      content: { text },
    });
  }

  async heartbeat(agentId) {
    await this.rpc("agent.heartbeat", { agent_id: agentId });
  }

  sseUrl() {
    return `${this.rpcUrl}/rpc/events`;
  }
}
