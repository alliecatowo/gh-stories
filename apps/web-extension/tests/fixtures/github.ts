/**
 * Fictional, sanitized GitHub-like markup.
 *
 * Deliberately hand-written rather than scraped: nothing here is a capture of
 * a real page, a real account or anyone's private repository. It mirrors the
 * *structure* real GitHub uses (a profile anchor wrapping an avatar image with
 * an avatars.githubusercontent.com/u/<id> source) so the adapters are tested
 * against the shape they actually have to survive, not against themselves.
 */
export const avatarHTML = (login: string, id: number, extra = ''): string =>
  `<a class="d-inline-block" href="/${login}" data-hovercard-type="user" ${extra}>
     <img class="avatar" src="https://avatars.githubusercontent.com/u/${id}?s=80&amp;v=4"
          alt="@${login}" width="40" height="40">
   </a>`;

export const commentTimeline = `
<div class="js-discussion">
  <div class="TimelineItem">
    <div class="TimelineItem-avatar">${avatarHTML('maya', 4242)}</div>
    <div class="comment-body">Looks good to me.</div>
  </div>
  <div class="TimelineItem">
    <div class="timeline-comment-avatar">${avatarHTML('sam', 5150)}</div>
    <div class="comment-body">Shipping it.</div>
  </div>
</div>`;

export const withBotAndOrg = `
<div class="js-discussion">
  <div class="TimelineItem-avatar">
    ${avatarHTML('dependabot%5Bbot%5D', 49699333)}
  </div>
  <div class="TimelineItem-avatar">
    <a href="/apps/github-actions"><img class="avatar"
       src="https://avatars.githubusercontent.com/in/15368?v=4" alt="github-actions"></a>
  </div>
  <div class="TimelineItem-avatar">
    <a href="/ghost"><img class="avatar"
       src="https://avatars.githubusercontent.com/u/10137?v=4" alt="@ghost"></a>
  </div>
  <div class="TimelineItem-avatar">
    <a href="/orgs/acme/people"><img class="avatar"
       src="https://avatars.githubusercontent.com/u/999?v=4" alt="acme"></a>
  </div>
  <div class="TimelineItem-avatar">
    <img src="/images/modules/marketing/hero.png" alt="A marketing illustration">
  </div>
</div>`;

export const dashboard = `
<div data-target="feed-container.feed">
  <div class="feed-item">${avatarHTML('alice', 1001)}</div>
</div>`;
