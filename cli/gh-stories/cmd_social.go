package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/terminal"
)

// relationCommand builds follow/unfollow/mute/unmute/block/unblock.
//
// These act on the Stories graph only. Following here never changes who you
// follow on GitHub.
func relationCommand(verb string) command {
	return func(ctx context.Context, args []string) error {
		fs := flag.NewFlagSet(verb, flag.ContinueOnError)
		service, _, asJSON := globalFlags(fs)
		parsed, err := parseArgs(fs, args)
		if err != nil || len(parsed) != 1 {
			return usagef("gh stories %s @alice", verb)
		}
		login := strings.TrimPrefix(parsed[0], "@")
		sess, err := openSession(*service, *asJSON, "auto")
		if err != nil {
			return err
		}

		var actionErr error
		var done string
		switch verb {
		case "follow":
			_, actionErr = sess.Client.Follow(ctx, login)
			done = "Following %s on Stories. This did not change who you follow on GitHub."
		case "unfollow":
			actionErr = sess.Client.Unfollow(ctx, login)
			done = "Unfollowed %s. A later GitHub import will not undo this."
		case "mute":
			actionErr = sess.Client.Mute(ctx, login)
			done = "Muted %s. They can still see your Stories; you just will not see theirs."
		case "unmute":
			actionErr = sess.Client.Unmute(ctx, login)
			done = "Unmuted %s."
		case "block":
			actionErr = sess.Client.Block(ctx, login)
			done = "Blocked %s. Neither of you can see the other's Stories or interact."
		case "unblock":
			actionErr = sess.Client.Unblock(ctx, login)
			done = "Unblocked %s."
		default:
			return usagef("unknown command %q", verb)
		}
		if actionErr != nil {
			return actionErr
		}
		status(done, terminal.SanitizeTruncate(login, 39))
		return nil
	}
}

// cmdSettings shows and changes account settings, including the audience and
// privacy controls, so none of it requires undocumented API calls.
func cmdSettings(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	service, _, asJSON := globalFlags(fs)
	setAudience := fs.String("default-audience", "",
		"people-i-follow | my-followers | mutuals | list:NAME | public")
	replies := fs.String("replies", "", "on | off — default for new Stories")
	reactions := fs.String("reactions", "", "on | off — default for new Stories")
	hide := fs.String("hide-from", "", "hide your Stories from @login")
	unhide := fs.String("unhide-from", "", "stop hiding your Stories from @login")
	revoke := fs.String("revoke-session", "", "revoke a session by id")
	if err := fs.Parse(args); err != nil {
		return usagef("gh stories settings [--default-audience …] [--replies on|off]")
	}
	sess, err := openSession(*service, *asJSON, "auto")
	if err != nil {
		return err
	}

	// Mutations first, then always show the resulting state.
	if *hide != "" {
		if err := sess.Client.Hide(ctx, strings.TrimPrefix(*hide, "@")); err != nil {
			return err
		}
		status("Hidden from %s.", strings.TrimPrefix(*hide, "@"))
	}
	if *unhide != "" {
		if err := sess.Client.Unhide(ctx, strings.TrimPrefix(*unhide, "@")); err != nil {
			return err
		}
		status("No longer hidden from %s.", strings.TrimPrefix(*unhide, "@"))
	}
	if *revoke != "" {
		if err := sess.Client.RevokeSession(ctx, *revoke); err != nil {
			return err
		}
		status("Session revoked.")
	}

	upd := map[string]any{}
	if *setAudience != "" {
		vis, listName, err := parseAudience(*setAudience)
		if err != nil {
			return err
		}
		upd["default_visibility"] = vis
		if listName != "" {
			id, err := resolveAudienceList(ctx, sess, listName)
			if err != nil {
				return err
			}
			upd["default_audience_list_id"] = id
		}
	}
	if v, err := onOff(*replies); err != nil {
		return err
	} else if v != nil {
		upd["default_allow_replies"] = *v
	}
	if v, err := onOff(*reactions); err != nil {
		return err
	} else if v != nil {
		upd["default_allow_reactions"] = *v
	}
	if len(upd) > 0 {
		if _, err := sess.Client.UpdateSettings(ctx, settingsUpdate(upd)); err != nil {
			return err
		}
		status("Updated.")
	}

	settings, err := sess.Client.Settings(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return jsonOut(settings)
	}

	out("Defaults for new Stories")
	out("  audience           %s", audienceLabel(settings.DefaultVisibility))
	out("  replies            %s", onOffLabel(settings.DefaultAllowReplies))
	out("  reactions          %s", onOffLabel(settings.DefaultAllowReactions))
	out("")
	out("Every Story disappears 24 hours after it is published.")
	out("Private replies are kept for %d days, then deleted.", settings.ReplyRetentionDays)

	if len(settings.AudienceLists) > 0 {
		out("")
		out("Audience lists")
		for _, l := range settings.AudienceLists {
			out("  %-20s %d member(s)", terminal.SanitizeTruncate(l.Name, 20), l.MemberCount)
		}
	}
	printPeople("Hidden from", settings.HiddenFrom)
	printPeople("Muted", settings.Muted)
	printPeople("Blocked", settings.Blocked)

	if len(settings.Sessions) > 0 {
		out("")
		out("Active sessions")
		for _, s := range settings.Sessions {
			marker := " "
			if s.Current {
				marker = "*"
			}
			out("  %s %-36s %-18s %s", marker, s.ID, s.ClientKind,
				terminal.SanitizeTruncate(s.ClientLabel, 32))
		}
		out("")
		out("  * this session. Revoke another with: gh stories settings --revoke-session ID")
	}
	return nil
}

func printPeople(title string, people []cliapi.PublicUser) {
	if len(people) == 0 {
		return
	}
	out("")
	out("%s", title)
	for _, p := range people {
		out("  %s", terminal.SanitizeTruncate(p.Login, 39))
	}
}

func onOff(v string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return nil, nil
	case "on", "yes", "true":
		t := true
		return &t, nil
	case "off", "no", "false":
		f := false
		return &f, nil
	}
	return nil, usagef("expected on or off, got %q", v)
}

func onOffLabel(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func audienceLabel(v string) string {
	switch v {
	case "followers_of_author":
		return "People I follow"
	case "author_follows":
		return "My followers"
	case "mutuals":
		return "Mutuals"
	case "custom_list":
		return "Custom list"
	case "public":
		return "Public — anyone signed in"
	}
	return fmt.Sprintf("%q", v)
}

// settingsUpdate converts the flag-derived map into the typed request.
func settingsUpdate(m map[string]any) cliapi.SettingsUpdate {
	var upd cliapi.SettingsUpdate
	if v, ok := m["default_visibility"].(string); ok {
		upd.DefaultVisibility = &v
	}
	if v, ok := m["default_audience_list_id"].(string); ok {
		upd.DefaultAudienceListID = &v
	}
	if v, ok := m["default_allow_replies"].(bool); ok {
		upd.DefaultAllowReplies = &v
	}
	if v, ok := m["default_allow_reactions"].(bool); ok {
		upd.DefaultAllowReactions = &v
	}
	return upd
}
