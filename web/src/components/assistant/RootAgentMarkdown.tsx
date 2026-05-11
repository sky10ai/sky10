import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

function joinClasses(...classes: Array<string | undefined>) {
  return classes.filter(Boolean).join(" ");
}

const markdownComponents: Components = {
  a: ({ children, href }) => (
    <a
      className="text-primary underline decoration-primary/40 underline-offset-2"
      href={href}
      rel="noopener noreferrer"
      target="_blank"
    >
      {children}
    </a>
  ),
  blockquote: ({ children }) => (
    <blockquote className="my-2 border-l-2 border-outline-variant/50 pl-3 text-secondary">
      {children}
    </blockquote>
  ),
  code: ({ children, className }) =>
    className ? (
      <code className={joinClasses(className, "text-xs text-on-surface")}>
        {children}
      </code>
    ) : (
      <code className="rounded bg-surface-container-low px-1.5 py-0.5 text-xs text-on-surface">
        {children}
      </code>
    ),
  h1: ({ children }) => (
    <h1 className="mb-2 text-base font-bold leading-6 text-on-surface">
      {children}
    </h1>
  ),
  h2: ({ children }) => (
    <h2 className="mb-1.5 text-sm font-bold leading-6 text-on-surface">
      {children}
    </h2>
  ),
  h3: ({ children }) => (
    <h3 className="mb-1 text-sm font-semibold leading-6 text-on-surface">
      {children}
    </h3>
  ),
  hr: () => <hr className="my-3 border-outline-variant/20" />,
  input: ({ checked, type }) => (
    <input
      checked={checked}
      className="mr-1.5 align-middle accent-primary"
      disabled
      readOnly
      type={type}
    />
  ),
  li: ({ children }) => <li className="mb-0.5 pl-0.5">{children}</li>,
  ol: ({ children }) => (
    <ol className="mb-2 ml-5 list-decimal space-y-0.5">{children}</ol>
  ),
  p: ({ children }) => (
    <p className="mb-2 whitespace-pre-wrap last:mb-0">{children}</p>
  ),
  pre: ({ children }) => (
    <pre className="my-2 overflow-x-auto rounded-lg border border-outline-variant/20 bg-surface-container-low px-3 py-2">
      {children}
    </pre>
  ),
  strong: ({ children }) => (
    <strong className="font-semibold text-on-surface">{children}</strong>
  ),
  table: ({ children }) => (
    <div className="my-2 overflow-x-auto">
      <table className="min-w-full border-collapse text-left text-xs">
        {children}
      </table>
    </div>
  ),
  td: ({ children }) => (
    <td className="border border-outline-variant/20 px-2 py-1 align-top">
      {children}
    </td>
  ),
  th: ({ children }) => (
    <th className="border border-outline-variant/20 bg-surface-container-low px-2 py-1 font-semibold">
      {children}
    </th>
  ),
  ul: ({ children }) => (
    <ul className="mb-2 ml-5 list-disc space-y-0.5">{children}</ul>
  ),
};

interface RootAgentMarkdownProps {
  className?: string;
  fallback?: string;
  text: string;
}

export function RootAgentMarkdown({
  className,
  fallback = "",
  text,
}: RootAgentMarkdownProps) {
  const content = text.trim() ? text : fallback;
  if (!content) return null;

  return (
    <div className={joinClasses("min-w-0 text-sm leading-6 text-on-surface", className)}>
      <Markdown components={markdownComponents} remarkPlugins={[remarkGfm]}>
        {content}
      </Markdown>
    </div>
  );
}
