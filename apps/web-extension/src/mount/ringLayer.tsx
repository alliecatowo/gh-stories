/**
 * One shared React root for every avatar ring on the page.
 *
 * A busy GitHub page has hundreds of avatars. Giving each its own React root
 * costs a scheduler, an event-listener set and a fibre tree apiece, and every
 * one of them has to be unmounted when GitHub drops the avatar. Instead the
 * rings render as portals into their own (cheap) shadow containers from a
 * single root that exists only while at least one ring does.
 */
import { createRoot, type Root } from "react-dom/client";
import { createPortal } from "react-dom";
import { useSyncExternalStore, type ReactNode } from "react";

interface Entry {
  key: number;
  container: Element;
  node: ReactNode;
}

let nextKey = 1;
let entries: Entry[] = [];
const listeners = new Set<() => void>();
let root: Root | null = null;

function emit(): void {
  for (const listener of listeners) listener();
}
function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
function snapshot(): Entry[] {
  return entries;
}

function RingLayer(): React.JSX.Element {
  const current = useSyncExternalStore(subscribe, snapshot, snapshot);
  return <>{current.map((entry) => createPortal(entry.node, entry.container, String(entry.key)))}</>;
}

export interface RingPortal {
  update(node: ReactNode): void;
  remove(): void;
}

/** Renders `node` into `container` through the shared root. */
export function mountRingPortal(container: Element, node: ReactNode): RingPortal {
  const key = nextKey++;
  entries = [...entries, { key, container, node }];
  if (!root) {
    // Detached on purpose: the root's own container never appears in the
    // page; only the portals' targets do.
    root = createRoot(document.createElement("div"));
    root.render(<RingLayer />);
  }
  emit();
  let removed = false;
  return {
    update(next) {
      if (removed) return;
      entries = entries.map((entry) => (entry.key === key ? { ...entry, node: next } : entry));
      emit();
    },
    remove() {
      if (removed) return;
      removed = true;
      entries = entries.filter((entry) => entry.key !== key);
      emit();
      if (entries.length === 0 && root) {
        const old = root;
        root = null;
        old.unmount();
      }
    },
  };
}

/** Test hook: live rings and whether the shared root exists. */
export function ringLayerStats(): { rings: number; hasRoot: boolean } {
  return { rings: entries.length, hasRoot: root !== null };
}
