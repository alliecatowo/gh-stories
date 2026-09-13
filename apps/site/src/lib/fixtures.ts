/**
 * Entirely fictional, entirely in-memory sample data for `/demo/`.
 * Nothing here is fetched from, or sent to, the live service — see
 * `apps/site/public/samples/README.md` for asset provenance.
 */
import {
  STORY_LIFETIME_MS,
  VISIBILITY_LABELS,
  type AuthorGroup,
  type PublicUser,
  type StoryItem,
} from '@gh-stories/contracts';
import { withBase } from './base';

// @gh-stories/ui renders `author.avatar_url` (and, further down, each
// variant's `url`) directly as an <img>/<video> `src` with no
// transformation of its own — so every asset path handed to it from these
// fixtures must already carry the site's base path.
function user(login: string, id: number, avatar: string): PublicUser {
  return {
    github_user_id: id,
    login,
    avatar_url: withBase(avatar),
    profile_url: '#',
    has_account: true,
  };
}

function isoHoursAgo(hours: number): string {
  return new Date(Date.now() - hours * 60 * 60 * 1000).toISOString();
}

function isoFromPublished(publishedIso: string): string {
  return new Date(new Date(publishedIso).getTime() + STORY_LIFETIME_MS).toISOString();
}

function item(partial: {
  id: string;
  author: PublicUser;
  caption?: string;
  altText: string;
  visibility: StoryItem['visibility'];
  hoursAgo: number;
  media: string;
  seen?: boolean;
}): StoryItem {
  const published_at = isoHoursAgo(partial.hoursAgo);
  return {
    id: partial.id,
    author: partial.author,
    state: 'published',
    caption: partial.caption,
    alt_text: partial.altText,
    visibility: partial.visibility,
    audience_label: partial.visibility ? VISIBILITY_LABELS[partial.visibility] : undefined,
    allow_replies: true,
    allow_reactions: true,
    media_kind: 'image',
    variants: [
      {
        kind: 'image',
        url: partial.media,
        mime: 'image/svg+xml',
        width: 800,
        height: 1000,
      },
    ],
    published_at,
    expires_at: isoFromPublished(published_at),
    seen: partial.seen ?? false,
    is_owner: false,
    viewer_count: undefined,
    my_reaction: undefined,
  };
}

const otterframes = user('otterframes', 900101, '/samples/avatar-otterframes.svg');
const boxcat = user('boxcat', 900102, '/samples/avatar-boxcat.svg');
const ramenroute = user('ramenroute', 900103, '/samples/avatar-ramenroute.svg');
export const you = user('you', 900199, '/samples/avatar-you.svg');

/** Returns a fresh fixture set with timestamps anchored to "now", so the
 * 24-hour countdown always demos correctly regardless of when the page
 * loads. */
export function sampleGroups(): AuthorGroup[] {
  return [
    {
      author: otterframes,
      has_unseen: true,
      items: [
        item({
          id: 'demo-concert-1',
          author: otterframes,
          caption: 'front row, worth the queue',
          altText: 'Illustrated concert scene: a silhouetted crowd faces a lit stage under magenta and amber lights.',
          visibility: 'public',
          hoursAgo: 2,
          media: '/samples/concert-1.svg',
        }),
        item({
          id: 'demo-concert-2',
          author: otterframes,
          altText: 'Text card reading "3am. worth it."',
          visibility: 'public',
          hoursAgo: 1.5,
          media: '/samples/concert-2.svg',
        }),
      ],
    },
    {
      author: boxcat,
      has_unseen: true,
      items: [
        item({
          id: 'demo-cat-1',
          author: boxcat,
          caption: 'the box arrived. so did he',
          altText: 'Illustrated scene: a cat sitting inside a cardboard box.',
          visibility: 'followers_of_author',
          hoursAgo: 6,
          media: '/samples/cat-1.svg',
        }),
        item({
          id: 'demo-cat-2',
          author: boxcat,
          altText: 'Text card reading "the box is mine now."',
          visibility: 'followers_of_author',
          hoursAgo: 5,
          media: '/samples/cat-2.svg',
        }),
      ],
    },
    {
      author: ramenroute,
      has_unseen: false,
      items: [
        item({
          id: 'demo-food-1',
          author: ramenroute,
          caption: 'day 3 of the ramen project',
          altText: 'Illustrated scene: a bowl of ramen with steam rising, chopsticks resting on the side.',
          visibility: 'mutuals',
          hoursAgo: 14,
          media: '/samples/food-1.svg',
          seen: true,
        }),
        item({
          id: 'demo-food-2',
          author: ramenroute,
          altText: 'Text card reading "reheated. still good."',
          visibility: 'mutuals',
          hoursAgo: 13,
          media: '/samples/food-2.svg',
          seen: true,
        }),
      ],
    },
  ];
}

/** The set of sample photos the visitor can "post" from the simulated
 * composer. Reuses the same fictional art used in the sample sequences. */
export const simulatedPostChoices: { label: string; media: string; altText: string }[] = [
  { label: 'The concert photo', media: '/samples/concert-1.svg', altText: 'Illustrated concert scene: a silhouetted crowd faces a lit stage under magenta and amber lights.' },
  { label: 'The cat-in-a-box photo', media: '/samples/cat-1.svg', altText: 'Illustrated scene: a cat sitting inside a cardboard box.' },
  { label: 'The ramen photo', media: '/samples/food-1.svg', altText: 'Illustrated scene: a bowl of ramen with steam rising, chopsticks resting on the side.' },
];

export function makeSimulatedItem(choice: (typeof simulatedPostChoices)[number], visibility: StoryItem['visibility']): StoryItem {
  const published_at = new Date().toISOString();
  return {
    id: `demo-you-${Date.now()}`,
    author: you,
    state: 'published',
    caption: 'posted from the sample demo (simulated)',
    alt_text: choice.altText,
    visibility,
    audience_label: visibility ? VISIBILITY_LABELS[visibility] : undefined,
    allow_replies: true,
    allow_reactions: true,
    media_kind: 'image',
    variants: [{ kind: 'image', url: choice.media, mime: 'image/svg+xml', width: 800, height: 1000 }],
    published_at,
    expires_at: isoFromPublished(published_at),
    seen: true,
    is_owner: true,
    viewer_count: 0,
    my_reaction: undefined,
  };
}
