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
	createList := fs.String("create-list", "", "create a custom audience list with this name")
	deleteList := fs.String("delete-list", "", "delete the custom audience list with this name")
	setListMembers := fs.String("list", "", "the custom audience list to change members of")
	members := fs.String("members", "",
		"comma-separated logins for --create-list or --list, e.g. @maya,@sam")
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

	// Distinguish "--members was not given" from "--members was given as
	// empty": the second is how you empty a list, and Go's flag package
	// cannot tell them apart from the value alone.
	membersProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "members" {
			membersProvided = true
		}
	})
	memberLogins := parseMembers(*members)

	if *createList != "" {
		list, err := sess.Client.CreateAudienceList(ctx, *createList, memberLogins)
		if err != nil {
			return err
		}
		status("Created the list %q with %d member(s).",
			terminal.SanitizeTruncate(list.Name, 60), list.MemberCount)
		if len(memberLogins) > list.MemberCount {
			status("Some logins were skipped because no such account has signed up yet.")
		}
	}

	if *setListMembers != "" {
		list, err := findAudienceList(ctx, sess, *setListMembers)
		if err != nil {
			return err
		}
		if !membersProvided {
			return usagef("say who should be on the list: --list %q --members @maya,@sam "+
				"(use --members \"\" to empty it)", *setListMembers)
		}
		if memberLogins == nil {
			// An explicitly empty list is allowed; make it unambiguous to the
			// API that members are being replaced, not left alone.
			memberLogins = []string{}
		}
		updated, err := sess.Client.UpdateAudienceList(ctx, list.ID, nil, memberLogins)
		if err != nil {
			return err
		}
		if updated.MemberCount == 0 {
			status("%q is now empty. Nobody can see Stories posted to it.",
				terminal.SanitizeTruncate(updated.Name, 60))
		} else {
			status("%q now has %d member(s).",
				terminal.SanitizeTruncate(updated.Name, 60), updated.MemberCount)
		}
	}

	if *deleteList != "" {
		list, err := findAudienceList(ctx, sess, *deleteList)
		if err != nil {
			return err
		}
		if err := sess.Client.DeleteAudienceList(ctx, list.ID); err != nil {
			return err
		}
		status("Deleted the list %q. Stories already posted to it keep their audience.",
			terminal.SanitizeTruncate(list.Name, 60))
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

	out("")
	out("Audience lists")
	if len(settings.AudienceLists) == 0 {
		out("  (none) — create one with: gh stories settings --create-list \"close friends\" --members @maya,@sam")
	} else {
		for _, l := range settings.AudienceLists {
			names := make([]string, 0, len(l.Members))
			for _, m := range l.Members {
				names = append(names, m.Login)
			}
			out("  %-20s %d member(s)  %s", terminal.SanitizeTruncate(l.Name, 20),
				l.MemberCount, terminal.SanitizeTruncate(strings.Join(names, ", "), 48))
		}
		out("")
		out("  Post to one with: gh stories post photo.jpg --audience list:NAME")
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

// parseMembers turns "@maya, sam" into ["maya","sam"].
func parseMembers(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if login := strings.TrimPrefix(strings.TrimSpace(p), "@"); login != "" {
			out = append(out, login)
		}
	}
	return out
}

// findAudienceList resolves a list by name, case-insensitively.
func findAudienceList(ctx context.Context, sess *session, name string) (*cliapi.AudienceList, error) {
	lists, err := sess.Client.AudienceLists(ctx)
	if err != nil {
		return nil, err
	}
	for i := range lists {
		if strings.EqualFold(lists[i].Name, name) {
			return &lists[i], nil
		}
	}
	available := make([]string, 0, len(lists))
	for _, l := range lists {
		available = append(available, l.Name)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("you have no audience lists yet. Create one with: gh stories settings --create-list %q", name)
	}
	return nil, fmt.Errorf("you have no audience list called %q. You have: %s",
		name, strings.Join(available, ", "))
}
