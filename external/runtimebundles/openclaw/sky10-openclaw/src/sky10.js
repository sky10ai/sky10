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

  async register(name, skills, tools = []) {
    return this.rpc("agent.register", { name, skills, tools });
  }

  async send(to, sessionId, text, deviceId) {
    return this.sendContent(to, sessionId, { text }, deviceId, "text");
  }

  async sendContent(to, sessionId, content, deviceId, messageType = "text") {
    return this.rpc("agent.send", {
      to,
      device_id: deviceId,
      session_id: sessionId,
      type: messageType,
      content,
    });
  }

  async sendDelta(to, sessionId, text, deviceId, streamId, clientRequestID = "") {
    if (!text) {
      return;
    }
    const content = {
      text,
      stream_id: streamId,
    };
    if (clientRequestID) {
      content.client_request_id = clientRequestID;
    }
    return this.sendContent(to, sessionId, content, deviceId, "delta");
  }

  async heartbeat(agentId) {
    await this.rpc("agent.heartbeat", { agent_id: agentId });
  }

  async updateJobStatus(jobId, workState, message = "", progress) {
    const params = {
      job_id: jobId,
      work_state: workState,
    };
    if (message) {
      params.message = message;
    }
    if (progress !== undefined) {
      params.progress = progress;
    }
    return this.rpc("agent.job.updateStatus", params);
  }

  async completeJob(jobId, output = {}, message = "") {
    const params = {
      job_id: jobId,
      output,
    };
    if (message) {
      params.message = message;
    }
    return this.rpc("agent.job.complete", params);
  }

  async failJob(jobId, code, message) {
    return this.rpc("agent.job.fail", {
      job_id: jobId,
      code,
      message,
    });
  }

  sseUrl() {
    return `${this.rpcUrl}/rpc/events`;
  }
}
