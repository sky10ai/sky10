import { useCallback, useEffect, useRef, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "xterm";
import { sandboxTerminalURL } from "../lib/rpc";
import "xterm/css/xterm.css";

type TerminalState = "idle" | "connecting" | "connected" | "disconnected" | "error";

interface SandboxTerminalProps {
  slug: string;
  enabled: boolean;
}

function stateLabel(state: TerminalState) {
  switch (state) {
    case "connecting":
      return "Connecting";
    case "connected":
      return "Connected";
    case "disconnected":
      return "Disconnected";
    case "error":
      return "Error";
    default:
      return "Waiting";
  }
}

function stateClasses(state: TerminalState) {
  switch (state) {
    case "connecting":
      return "bg-primary/10 text-primary";
    case "connected":
      return "bg-secondary-container text-on-secondary-container";
    case "error":
      return "bg-error-container/30 text-error";
    default:
      return "bg-surface-container text-secondary";
  }
}

export function SandboxTerminal({ slug, enabled }: SandboxTerminalProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [state, setState] = useState<TerminalState>("idle");
  const [message, setMessage] = useState<string>(enabled ? "Connecting terminal..." : "Terminal becomes available once the sandbox is running.");
  const [retryToken, setRetryToken] = useState(0);

  useEffect(() => {
    if (!containerRef.current) return;

    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: "\"SFMono-Regular\", \"JetBrains Mono\", \"Menlo\", monospace",
      fontSize: 12,
      lineHeight: 1.25,
      theme: {
        background: "#111315",
        foreground: "#d7dadc",
        cursor: "#f4f7f8",
        black: "#111315",
        red: "#ff8a80",
        green: "#85f2a5",
        yellow: "#f6d06d",
        blue: "#8ab4f8",
        magenta: "#c58af9",
        cyan: "#7dd3fc",
        white: "#f4f7f8",
        brightBlack: "#6b7280",
        brightRed: "#ff9d94",
        brightGreen: "#b9fbcf",
        brightYellow: "#ffe39a",
        brightBlue: "#b1cdff",
        brightMagenta: "#d9b3ff",
        brightCyan: "#b7f0ff",
        brightWhite: "#ffffff",
      },
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(containerRef.current);
    fitAddon.fit();

    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;

    return () => {
      terminalRef.current = null;
      fitAddonRef.current = null;
      terminal.dispose();
    };
  }, []);

  useEffect(() => {
    const terminal = terminalRef.current;
    const fitAddon = fitAddonRef.current;
    const container = containerRef.current;
    if (!terminal || !fitAddon || !container) return;

    terminal.reset();
    terminal.clear();

    if (!enabled) {
      setState("idle");
      setMessage("Terminal becomes available once the sandbox is running.");
      terminal.writeln("Terminal becomes available once the sandbox is running.");
      return;
    }

    setState("connecting");
    setMessage("Connecting terminal...");
    terminal.writeln(`Connecting to ${slug}...`);

    const socket = new WebSocket(sandboxTerminalURL(slug));
    socket.binaryType = "arraybuffer";
    const decoder = new TextDecoder();

    const send = (payload: unknown) => {
      if (socket.readyState !== WebSocket.OPEN) return;
      socket.send(JSON.stringify(payload));
    };

    const fit = () => {
      fitAddon.fit();
      send({ type: "resize", cols: terminal.cols, rows: terminal.rows });
    };

    const dataDisposable = terminal.onData((data) => {
      send({ type: "input", data });
    });

    const resizeObserver = new ResizeObserver(() => {
      fit();
    });
    resizeObserver.observe(container);

    const handleWindowResize = () => fit();
    window.addEventListener("resize", handleWindowResize);

    socket.onopen = () => {
      setState("connected");
      setMessage("Connected");
      fit();
      terminal.focus();
    };

    socket.onmessage = (event) => {
      if (typeof event.data === "string") {
        terminal.write(event.data);
        return;
      }
      if (event.data instanceof ArrayBuffer) {
        terminal.write(decoder.decode(new Uint8Array(event.data), { stream: true }));
      }
    };

    socket.onerror = () => {
      setState("error");
      setMessage("Terminal connection failed.");
    };

    socket.onclose = (event) => {
      if (event.code === 1000) {
        setState("disconnected");
        setMessage("Terminal disconnected.");
        return;
      }
      setState("error");
      setMessage(event.reason || "Terminal connection closed.");
      terminal.writeln("");
      terminal.writeln(event.reason || "Terminal connection closed.");
    };

    return () => {
      resizeObserver.disconnect();
      window.removeEventListener("resize", handleWindowResize);
      dataDisposable.dispose();
      socket.close(1000, "terminal closed");
    };
  }, [enabled, retryToken, slug]);

  const handleReconnect = useCallback(() => {
    setRetryToken((value) => value + 1);
  }, []);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className={`rounded-full px-3 py-1 text-xs font-semibold ${stateClasses(state)}`}>
          {stateLabel(state)}
        </span>
        <button
          className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
          onClick={handleReconnect}
          type="button"
        >
          Reconnect
        </button>
      </div>
      <p className="text-sm text-secondary">{message}</p>
      <div className="overflow-hidden rounded-2xl border border-outline-variant/10 bg-[#111315]">
        <div className="h-[520px] w-full p-3">
          <div className="h-full w-full" ref={containerRef} />
        </div>
      </div>
    </div>
  );
}
