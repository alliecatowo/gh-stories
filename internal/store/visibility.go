package store

// visibleToViewer is THE definition of "this caller may see this Story item".
//
// There is exactly one copy of these rules, expressed as a SQL fragment, and
// every read path — single item, feed, ring status, media gateway, viewers,
// replies, reactions — composes this same fragment. A new route cannot
// accidentally skip a rule, because there is no second place to skip it in.
//
// Parameters, in order:
//
//	$1 :: uuid        viewer's Stories user id
//	$2 :: bigint      viewer's GitHub numeric id
//	$3 :: timestamptz authoritative "now" (injectable clock)
//
// The fragment assumes `story_items si` and the author's `users au` are in
// scope, joined as:
//
//	FROM story_items si JOIN users au ON au.id = si.author_user_id
//
// Rules, in the order they are evaluated:
//
//  1. The item is published, has a publication time, and now < expires_at.
//     The boundary is exclusive: at exactly expires_at the item is expired.
//     This is checked on every read and write independently of the background
//     cleanup job, so restarting a worker or database cannot revive an item.
//  2. The author's account is neither suspended nor deleted.
//  3. The owner can always see their own live item.
//  4. For everyone else: no block in either direction, no author hide rule
//     against this viewer, and the item's audience admits them, evaluated
//     against the CURRENT graph so follow changes take effect immediately.
//
// Mute is deliberately absent: muting affects the muting user's presentation,
// not the other person's access permissions.
const visibleToViewer = `(
	si.state = 'published'
	AND si.published_at IS NOT NULL
	AND si.expires_at IS NOT NULL
	AND $3::timestamptz < si.expires_at
	AND au.suspended_at IS NULL
	AND au.deleted_at IS NULL
	AND (
		si.author_user_id = $1::uuid
		OR (
			NOT EXISTS (
				SELECT 1 FROM blocks b
				WHERE (b.blocker_user_id = $1::uuid AND b.blocked_github_user_id = au.github_user_id)
				   OR (b.blocker_user_id = si.author_user_id AND b.blocked_github_user_id = $2::bigint)
			)
			AND NOT EXISTS (
				SELECT 1 FROM hide_rules h
				WHERE h.author_user_id = si.author_user_id
				  AND h.hidden_github_user_id = $2::bigint
			)
			AND CASE si.visibility
				-- "People I follow": signed-in users the AUTHOR follows.
				WHEN 'followers_of_author' THEN EXISTS (
					SELECT 1 FROM relationships r
					WHERE r.follower_user_id = si.author_user_id
					  AND r.followee_github_user_id = $2::bigint
					  AND r.state = 'active')
				-- "My followers": signed-in users who follow the author.
				WHEN 'author_follows' THEN EXISTS (
					SELECT 1 FROM relationships r
					WHERE r.follower_user_id = $1::uuid
					  AND r.followee_github_user_id = au.github_user_id
					  AND r.state = 'active')
				WHEN 'mutuals' THEN
					EXISTS (SELECT 1 FROM relationships r
						WHERE r.follower_user_id = si.author_user_id
						  AND r.followee_github_user_id = $2::bigint
						  AND r.state = 'active')
					AND EXISTS (SELECT 1 FROM relationships r
						WHERE r.follower_user_id = $1::uuid
						  AND r.followee_github_user_id = au.github_user_id
						  AND r.state = 'active')
				WHEN 'custom_list' THEN EXISTS (
					SELECT 1 FROM audience_list_members m
					JOIN audience_lists al ON al.id = m.list_id
					WHERE m.list_id = si.audience_list_id
					  AND al.owner_user_id = si.author_user_id
					  AND al.deleted_at IS NULL
					  AND m.github_user_id = $2::bigint)
			-- "Public — anyone, no sign-in required". Authenticated callers
			-- match here; anonymous callers are served by PublicStory, which
			-- applies the same published/expiry/author checks without a graph.
			WHEN 'public' THEN true
				ELSE false
			END
		)
	)
)`

// VisibilityPredicate exposes the fragment to tests that assert there is only
// one copy of the rules.
func VisibilityPredicate() string { return visibleToViewer }
