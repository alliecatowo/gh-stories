package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/media"
)

// cmdPost uploads an image or video and waits for it to be published.
func cmdPost(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	service, _, asJSON := globalFlags(fs)
	caption := fs.String("caption", "", "caption shown with the Story")
	description := fs.String("description", "", "accessibility description of the media")
	audience := fs.String("audience", "", "people-i-follow | my-followers | mutuals | list:NAME | public")
	filename := fs.String("filename", "", "advisory filename when reading from stdin")
	noReplies := fs.Bool("no-replies", false, "turn replies off for this Story")
	noReactions := fs.Bool("no-reactions", false, "turn reactions off for this Story")
	wait := fs.Bool("wait", true, "wait for processing to finish")
	rest, err := parseArgs(fs, args)
	if err != nil {
		return usagef("gh stories post <file|-> [--caption …] [--audience …]")
	}
	if len(rest) != 1 {
		return usagef("gh stories post <file|->   (use - to read from stdin)")
	}
	source := rest[0]

	sess, err := openSession(*service, *asJSON, "auto")
	if err != nil {
		return err
	}

	// Spool to a bounded temp file so we can sniff the type, compute a
	// checksum, and know the exact size — all before uploading a single byte.
	spooled, size, sum, err := spool(source, sess.maxUpload())
	if err != nil {
		return err
	}
	defer os.Remove(spooled)

	// Sniff the real type from content. The filename is never trusted.
	f, err := os.Open(spooled)
	if err != nil {
		return err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	f.Close()
	mime, ok := media.Sniff(head[:n])
	if !ok {
		return fmt.Errorf("that file is not a supported image or video (JPEG, PNG, WebP, GIF, MP4 or WebM)")
	}

	advisory := *filename
	if advisory == "" && source != "-" {
		advisory = filepath.Base(source)
	}

	visibility, listName, err := parseAudience(*audience)
	if err != nil {
		return err
	}
	meta := cliapi.UploadIntentRequest{
		MIME: mime, ByteSize: size, Filename: advisory,
		Caption: *caption, AltText: *description,
	}
	if visibility != "" {
		meta.Visibility = visibility
		if listName != "" {
			id, err := resolveAudienceList(ctx, sess, listName)
			if err != nil {
				return err
			}
			meta.AudienceListID = id
		}
	}
	if *noReplies {
		v := false
		meta.AllowReplies = &v
	}
	if *noReactions {
		v := false
		meta.AllowReactions = &v
	}

	body, err := os.Open(spooled)
	if err != nil {
		return err
	}
	defer body.Close()

	// One idempotency key for the whole post: a retry after a dropped
	// connection returns the original Story instead of creating a second one.
	key := uuid.NewString()

	var lastPct int
	announcedProcessing := false
	item, err := sess.Client.Upload(ctx, cliapi.UploadRequest{
		Meta: meta, Reader: body, Size: size, ChecksumSHA256: sum,
		IdempotencyKey: key,
		Progress: func(sent, total int64) {
			if *asJSON || !stderrIsTTY() || total <= 0 {
				return
			}
			pct := int(sent * 100 / total)
			if pct != lastPct {
				lastPct = pct
				fmt.Fprintf(os.Stderr, "\ruploading… %3d%%", pct)
			}
		},
		OnStateChange: func(s *cliapi.StoryItem) {
			if *asJSON {
				return
			}
			// Announce once, not on every poll.
			if s.State == "processing" && !announcedProcessing {
				announcedProcessing = true
				fmt.Fprintf(os.Stderr, "\r")
				status("uploaded. processing…")
			}
		},
	})
	if err != nil {
		return err
	}
	if !*wait {
		return emitPosted(item, *asJSON, false)
	}
	if item.State == "failed" {
		if *asJSON {
			_ = emitPosted(item, true, false)
		}
		return fmt.Errorf("%w: %s", errProcessingFailed, item.FailureMessage)
	}
	return emitPosted(item, *asJSON, true)
}

func emitPosted(item *cliapi.StoryItem, asJSON, published bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(item)
	}
	if published && item.State == "published" {
		when := ""
		if item.ExpiresAt != nil {
			when = " It disappears " + item.ExpiresAt.Local().Format("Mon 15:04") + "."
		}
		status("Posted.%s", when)
	} else {
		status("Accepted for processing. Track it with: gh stories")
	}
	fmt.Fprintln(os.Stdout, item.ID)
	return nil
}

// spool copies the source to a bounded temp file, returning its size and
// checksum. Reading stdin into a bounded file is what makes
// `cat shot.png | gh stories post -` work as a real binary pipeline.
func spool(source string, limit int64) (path string, size int64, checksum string, err error) {
	var src io.Reader
	if source == "-" {
		src = os.Stdin
	} else {
		f, err := os.Open(source)
		if err != nil {
			if os.IsNotExist(err) {
				return "", 0, "", fmt.Errorf("no such file: %s", source)
			}
			return "", 0, "", err
		}
		defer f.Close()
		src = f
	}

	tmp, err := os.CreateTemp("", "gh-stories-upload-*")
	if err != nil {
		return "", 0, "", err
	}
	defer tmp.Close()

	h := sha256.New()
	// limit+1 so we can tell "exactly at the limit" from "over the limit".
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(src, limit+1))
	if err != nil {
		os.Remove(tmp.Name())
		return "", 0, "", err
	}
	if n > limit {
		os.Remove(tmp.Name())
		return "", 0, "", fmt.Errorf("that file is larger than the %d MB limit", limit>>20)
	}
	if n == 0 {
		os.Remove(tmp.Name())
		return "", 0, "", fmt.Errorf("there was nothing to post")
	}
	return tmp.Name(), n, hex.EncodeToString(h.Sum(nil)), nil
}

// parseAudience maps the human flag onto the API's visibility values.
func parseAudience(v string) (visibility, listName string, err error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "", "", nil
	case "people-i-follow", "following":
		return "followers_of_author", "", nil
	case "my-followers", "followers":
		return "author_follows", "", nil
	case "mutuals":
		return "mutuals", "", nil
	case "public":
		return "public", "", nil
	}
	if name, ok := strings.CutPrefix(v, "list:"); ok && name != "" {
		return "custom_list", name, nil
	}
	return "", "", usagef("unknown audience %q. Use people-i-follow, my-followers, mutuals, list:NAME or public", v)
}

func resolveAudienceList(ctx context.Context, sess *session, name string) (string, error) {
	lists, err := sess.Client.AudienceLists(ctx)
	if err != nil {
		return "", err
	}
	for _, l := range lists {
		if strings.EqualFold(l.Name, name) {
			return l.ID, nil
		}
	}
	return "", fmt.Errorf("you do not have an audience list called %q", name)
}

// maxUpload is the client-side ceiling, matching the service's limit.
func (s *session) maxUpload() int64 { return 100 << 20 }

func stderrIsTTY() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
