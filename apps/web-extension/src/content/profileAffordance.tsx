/**
 * A small, clearly-labelled affordance on a profile page for viewing that
 * person's active Story (when they have one) and following them on
 * Stories — entirely separate from GitHub's own follow button, and never
 * touching GitHub's own follow graph (`onFollow`/`onUnfollow` call only the
 * Stories service via the background).
 */
import { useEffect, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { createShadowMount, type ShadowMount } from "../mount/shadow.js";
import { observeGithubTheme } from "../mount/theme.js";
import { callBackground } from "../messaging/client.js";
import type { OverlayHost } from "./overlayHost.js";

export interface ProfileAffordanceMount {
  destroy(): void;
}

function Affordance(props: { login: string; overlay: OverlayHost }): React.JSX.Element | null {
  const { login, overlay } = props;
  const [hasStory, setHasStory] = useState(false);
  const [following, setFollowing] = useState(false);
  const [signedIn, setSignedIn] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([
      callBackground({ type: "ghs:session/get" }),
      callBackground({ type: "ghs:ring/status", githubUserIds: [], logins: [login] }),
    ]).then(([session, ring]) => {
      if (cancelled) return;
      setSignedIn(session.ok && session.data.signedIn);
      if (ring.ok && ring.data.entries[0]) setHasStory(ring.data.entries[0].has_active);
    });
    return () => {
      cancelled = true;
    };
  }, [login]);

  if (!signedIn) return null;

  return (
    <div className="ghs-root ghs-profile-affordance">
      {hasStory ? (
        <button type="button" className="ghs-profile-affordance__view" onClick={() => void overlay.openViewerForLogin(login)}>
          View Story
        </button>
      ) : null}
      <button
        type="button"
        className="ghs-profile-affordance__follow"
        disabled={busy}
        aria-pressed={following}
        onClick={async () => {
          setBusy(true);
          const action = following ? "unfollow" : "follow";
          const result = await callBackground({ type: "ghs:graph/action", action, login });
          if (result.ok) setFollowing(action === "follow");
          setBusy(false);
        }}
      >
        {following ? "Following on Stories" : "Follow on Stories"}
      </button>
    </div>
  );
}

export function mountProfileAffordance(container: Element, login: string, overlay: OverlayHost): ProfileAffordanceMount {
  const mount: ShadowMount = createShadowMount({ tagName: "ghs-profile-affordance" });
  mount.host.style.cssText = "display: inline-block; margin-left: 8px; vertical-align: middle;";
  container.appendChild(mount.host);
  const stopTheme = observeGithubTheme(mount.host);
  const root: Root = createRoot(mount.container);
  root.render(<Affordance login={login} overlay={overlay} />);
  return {
    destroy() {
      stopTheme();
      root.unmount();
      mount.destroy();
    },
  };
}
