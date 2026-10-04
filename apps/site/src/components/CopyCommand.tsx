import { useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { Button } from './ui/button';

/** A terminal-styled command block with a copy button (the one hydrated island on the landing page). */
export default function CopyCommand({ lines, label }: { lines: string[]; label: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(lines.join('\n'));
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      /* clipboard unavailable: the command is still selectable text */
    }
  }
  return (
    <div className="relative min-w-0 rounded-2xl border border-border bg-terminal text-terminal-foreground">
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2 font-mono text-xs text-terminal-foreground/60">
        <span>{label}</span>
        <Button variant="ghost" size="sm" onClick={copy} className="h-7 px-3 text-xs text-terminal-foreground hover:bg-white/10" aria-live="polite">
          {copied ? <Check /> : <Copy />}
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <pre className="m-0 overflow-x-auto p-4 font-mono text-sm leading-relaxed">
        {lines.map((line) => (
          <div key={line}>
            <span className="select-none text-terminal-accent">$ </span>
            {line}
          </div>
        ))}
      </pre>
    </div>
  );
}
