package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/terminal"
)

// resolveTarget turns either --story ID or @login into a single story id.
//
// When a person has several active items this must never silently pick one:
// interactively it prompts with context, and noninteractively it refuses and
// tells the caller to pass --story.
func resolveTarget(ctx context.Context, sess *session, storyFlag string, args []string,
	action string) (string, []string, error) {
	if storyFlag != "" {
		return storyFlag, args, nil
	}
	if len(args) == 0 || !strings.HasPrefix(args[0], "@") {
		return "", args, usagef("say who or what to %s: gh stories %s @alice \"…\"  or  --story STORY_ID", action, action)
	}
	login := strings.TrimPrefix(args[0], "@")
	rest := args[1:]

	group, err := sess.Client.UserStories(ctx, login)
	if err != nil {
		return "", rest, err
	}
	live := make([]cliapi.StoryItem, 0, len(group.Items))
	for _, it := range group.Items {
		if it.State == "published" {
			live = append(live, it)
		}
	}
	switch len(live) {
	case 0:
		return "", rest, fmt.Errorf("%s has no active Stories you can see", login)
	case 1:
		status("%s → %s %s", action, login, describeItem(live[0]))
		return live[0].ID, rest, nil
	}

	if !interactive() || sess.JSON {
		out("%s has %d active Stories:", login, len(live))
		for _, it := range live {
			out("  %s  %s", it.ID, describeItem(it))
		}
		return "", rest, usagef("more than one Story matches; pass --story STORY_ID")
	}

	out("%s has %d active Stories:", login, len(live))
	for i, it := range live {
		out("  %d) %s  %s", i+1, it.ID, describeItem(it))
	}
	fmt.Fprint(os.Stderr, "Which one? [1] ")
	var answer string
	_, _ = fmt.Fscanln(os.Stdin, &answer)
	answer = strings.TrimSpace(answer)
	idx := 0
	if answer != "" {
		if _, err := fmt.Sscanf(answer, "%d", &idx); err != nil || idx < 1 || idx > len(live) {
			return "", rest, fmt.Errorf("that was not one of the choices")
		}
		idx--
	}
	return live[idx].ID, rest, nil
}

func describeItem(it cliapi.StoryItem) string {
	parts := []string{it.MediaKind}
	if it.PublishedAt != nil {
		parts = append(parts, relativeAge(*it.PublishedAt))
	}
	if it.Caption != "" {
		parts = append(parts, "“"+terminal.SanitizeTruncate(it.Caption, 40)+"”")
	}
	return strings.Join(parts, " · ")
}

func cmdReply(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	service, _, asJSON := globalFlags(fs)
	story := fs.String("story", "", "the Story to reply to")
	parsed, err := parseArgs(fs, args)
	if err != nil {
		return usagef(`gh stories reply @alice "lmao"   or   gh stories reply --story ID "lmao"`)
	}
	sess, err := openSession(*service, *asJSON, "auto")
	if err != nil {
		return err
	}
	storyID, rest, err := resolveTarget(ctx, sess, *story, parsed, "reply")
	if err != nil {
		return err
	}
	body := strings.TrimSpace(strings.Join(rest, " "))
	if body == "" {
		return usagef(`write something: gh stories reply @alice "lmao"`)
	}
	reply, err := sess.Client.Reply(ctx, storyID, body, "")
	if err != nil {
		return err
	}
	if *asJSON {
		return jsonOut(reply)
	}
	status("Sent.")
	return nil
}

func cmdReact(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("react", flag.ContinueOnError)
	service, _, asJSON := globalFlags(fs)
	story := fs.String("story", "", "the Story to react to")
	remove := fs.Bool("remove", false, "remove your reaction")
	parsed, err := parseArgs(fs, args)
	if err != nil {
		return usagef(`gh stories react @alice '❤️'`)
	}
	sess, err := openSession(*service, *asJSON, "auto")
	if err != nil {
		return err
	}
	storyID, rest, err := resolveTarget(ctx, sess, *story, parsed, "react to")
	if err != nil {
		return err
	}
	if *remove {
		if err := sess.Client.ClearReaction(ctx, storyID); err != nil {
			return err
		}
		status("Reaction removed.")
		return nil
	}
	if len(rest) == 0 {
		return usagef("pick one of: %s", strings.Join(domain.Reactions, " "))
	}
	emoji := strings.TrimSpace(rest[0])
	if !domain.ValidReaction(emoji) {
		return usagef("%q is not available. Pick one of: %s", emoji, strings.Join(domain.Reactions, " "))
	}
	reaction, err := sess.Client.SetReaction(ctx, storyID, emoji, "")
	if err != nil {
		return err
	}
	if *asJSON {
		return jsonOut(reaction)
	}
	status("Reacted %s", emoji)
	return nil
}

func cmdDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	service, _, _ := globalFlags(fs)
	parsed, err := parseArgs(fs, args)
	if err != nil || len(parsed) != 1 {
		return usagef("gh stories delete STORY_ID")
	}
	sess, err := openSession(*service, false, "auto")
	if err != nil {
		return err
	}
	if err := sess.Client.DeleteStory(ctx, parsed[0]); err != nil {
		return err
	}
	status("Deleted. It is no longer served, and its files are scheduled for removal.")
	return nil
}

func cmdViewers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("viewers", flag.ContinueOnError)
	service, _, asJSON := globalFlags(fs)
	parsed, err := parseArgs(fs, args)
	if err != nil || len(parsed) != 1 {
		return usagef("gh stories viewers STORY_ID")
	}
	sess, err := openSession(*service, *asJSON, "auto")
	if err != nil {
		return err
	}
	list, err := sess.Client.Viewers(ctx, parsed[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return jsonOut(list)
	}
	if list.Total == 0 {
		out("No views yet.")
		return nil
	}
	out("%d viewer(s)", list.Total)
	for _, v := range list.Viewers {
		line := "  " + terminal.SanitizeTruncate(v.User.Login, 24)
		if v.Reaction != "" {
			line += "  " + v.Reaction
		}
		out("%s", line)
	}
	return nil
}

func cmdInbox(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	service, _, asJSON := globalFlags(fs)
	markRead := fs.Bool("read", false, "mark everything as read")
	if err := fs.Parse(args); err != nil {
		return usagef("gh stories inbox [--read]")
	}
	sess, err := openSession(*service, *asJSON, "auto")
	if err != nil {
		return err
	}
	inbox, err := sess.Client.Inbox(ctx, "", 30)
	if err != nil {
		return err
	}
	if *asJSON {
		if err := jsonOut(inbox); err != nil {
			return err
		}
	} else if len(inbox.Entries) == 0 {
		out("Nothing in your inbox.")
	} else {
		for _, e := range inbox.Entries {
			unread := " "
			if e.ReadAt == nil {
				unread = "●"
			}
			actor := terminal.SanitizeTruncate(e.Actor.Login, 18)
			var detail string
			switch e.Kind {
			case "reply":
				detail = "replied: " + terminal.SanitizeTruncate(e.Body, 48)
			case "reaction":
				detail = "reacted " + e.Emoji
			case "follow":
				detail = "started following you"
			}
			if e.StoryExpired && e.Kind != "follow" {
				detail += "  (Story expired)"
			}
			out("%s %-18s %s", unread, actor, detail)
		}
		out("")
		out("%d unread", inbox.Unread)
	}
	if *markRead {
		if err := sess.Client.InboxRead(ctx, nil, true); err != nil {
			return err
		}
		status("Marked all as read.")
	}
	return nil
}

func cmdReport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	service, _, asJSON := globalFlags(fs)
	story := fs.String("story", "", "the Story to report")
	login := fs.String("user", "", "the account to report")
	reason := fs.String("reason", "other",
		"spam | harassment | nudity | violence | self_harm | illegal | other")
	details := fs.String("details", "", "anything else the moderator should know")
	if err := fs.Parse(args); err != nil {
		return usagef("gh stories report --story STORY_ID [--reason …]")
	}
	if *story == "" && *login == "" {
		return usagef("gh stories report --story STORY_ID   or   --user @alice")
	}
	sess, err := openSession(*service, *asJSON, "auto")
	if err != nil {
		return err
	}
	req := cliapi.ReportRequest{Reason: *reason, Details: *details}
	if *story != "" {
		req.SubjectKind, req.StoryID = "story", *story
	} else {
		req.SubjectKind, req.Login = "user", strings.TrimPrefix(*login, "@")
	}
	report, err := sess.Client.CreateReport(ctx, req)
	if err != nil {
		return err
	}
	if *asJSON {
		return jsonOut(report)
	}
	status("Reported. A moderator will look at it.")
	return nil
}

func jsonOut(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
