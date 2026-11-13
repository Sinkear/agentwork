"use client";

import { useCallback, useEffect, useRef } from "react";

// ── Stick-to-bottom for streaming surfaces ──
//
// Chat / run streams must follow new content (a growing assistant bubble, a
// streaming tool_call_update landing in place, a refetched message row)
// WITHOUT yanking a human who scrolled up to read history. This hook owns
// the container ref and does exactly that:
//   - while the user is within `threshold` px of the bottom, every render
//     where `deps` change pins the container to the bottom;
//   - scrolling away from the bottom opts out until the user scrolls back.
//
// A callback ref is used (not a RefObject) so a conditionally rendered
// container — e.g. the timeline side card's stream, which mounts only when
// expanded — binds its scroll listener when the node actually appears.
export function useStickToBottom<T extends HTMLElement = HTMLDivElement>(
  deps: React.DependencyList,
  opts?: { threshold?: number; enabled?: boolean }
) {
  const thresholdRef = useRef(opts?.threshold ?? 32);
  thresholdRef.current = opts?.threshold ?? 32;
  const enabledRef = useRef(opts?.enabled ?? true);
  enabledRef.current = opts?.enabled ?? true;
  const nodeRef = useRef<T | null>(null);
  const cleanupRef = useRef<(() => void) | null>(null);
  // Default true: a fresh container starts pinned to the bottom (a chat opens
  // at the latest message).
  const stickRef = useRef(true);

  const ref = useCallback((node: T | null) => {
    cleanupRef.current?.();
    cleanupRef.current = null;
    nodeRef.current = node;
    if (!node) return;
    stickRef.current = true;
    const update = () => {
      stickRef.current =
        node.scrollHeight - node.scrollTop - node.clientHeight <= thresholdRef.current;
    };
    update();
    node.addEventListener("scroll", update, { passive: true });
    cleanupRef.current = () => node.removeEventListener("scroll", update);
  }, []);

  // Unbind on unmount (the callback ref also gets null there, but be explicit).
  useEffect(
    () => () => {
      cleanupRef.current?.();
      cleanupRef.current = null;
      nodeRef.current = null;
    },
    []
  );

  // Pin on content growth. `deps` is the CALLER's dependency list (message
  // arrays / stream state), so the exhaustive-deps lint cannot verify it.
  useEffect(() => {
    if (!enabledRef.current) return;
    const el = nodeRef.current;
    if (!el || !stickRef.current) return;
    el.scrollTop = el.scrollHeight;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return ref;
}