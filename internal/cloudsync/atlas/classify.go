package atlas

import (
	"context"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"

	"awesome-reminder/internal/cloudsync"
)

// ErrBadURI is returned by Open for anything but a mongodb:// or
// mongodb+srv:// string.
var ErrBadURI = errors.New("That isn't a MongoDB connection string. It should start with mongodb+srv://")

const (
	textOffline     = "No connection to Atlas."
	textAuth        = "Atlas rejected the username or password."
	textPermissions = "This database user can't read and write " + Database + "."
	textUnreachable = "Can't reach your Atlas cluster. It may be paused (resume it in Atlas), or this network's IP isn't on the Atlas access list."
)

// credentials matches scheme://user:password@ in any text.
var credentials = regexp.MustCompile(`(mongodb(?:\+srv)?://[^/\s:@]*:)[^\s?]*@`)

// Redact replaces the password in a connection string (or in any text that
// contains one) with "••••".
func Redact(uri string) string { return credentials.ReplaceAllString(uri, "${1}••••@") }

// Host returns the first host of a connection string without credentials or
// port, or "" if it cannot be read.
func Host(uri string) string {
	rest, ok := cutScheme(uri)
	if !ok {
		return ""
	}
	// The authority ends at the first / or ? after the last @ (a password may
	// hold an unescaped one).
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.Index(rest, ","); i >= 0 {
		rest = rest[:i]
	}
	if h, _, err := net.SplitHostPort(rest); err == nil {
		rest = h
	}
	return strings.Trim(rest, "[]")
}

func cutScheme(uri string) (string, bool) {
	for _, p := range []string{"mongodb+srv://", "mongodb://"} {
		if rest, ok := strings.CutPrefix(uri, p); ok {
			return rest, true
		}
	}
	return "", false
}

// redactedError hides a URI that the driver may echo in its message while
// keeping the error chain for Classify.
type redactedError struct{ err error }

func (e redactedError) Error() string { return Redact(e.err.Error()) }
func (e redactedError) Unwrap() error { return e.err }

// Classify maps an error to a sync state and a user-facing reason. host is
// probed on a server-selection timeout to tell a paused cluster or a blocked
// IP (the name resolves) from no network (it does not). An empty host counts
// as not resolving.
func Classify(err error, host string) (cloudsync.State, string) {
	var ce mongo.ServerError
	var sel topology.ServerSelectionError
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.Is(err, ErrBadURI):
		return cloudsync.StateError, ErrBadURI.Error()
	case errors.As(err, &ce) && ce.HasErrorCode(18):
		return cloudsync.StateError, textAuth
	case errors.As(err, &ce) && (ce.HasErrorCode(13) || (ce.HasErrorCode(8000) && authorizationText(err))):
		return cloudsync.StateError, textPermissions
	case errors.As(err, &sel):
		if resolves(host) {
			return cloudsync.StateError, textUnreachable
		}
		return cloudsync.StateOffline, textOffline
	case errors.As(err, &dns), errors.Is(err, context.DeadlineExceeded), errors.As(err, &op), errors.Is(err, io.EOF):
		return cloudsync.StateOffline, textOffline
	}
	return cloudsync.StateError, Redact(err.Error())
}

// authorizationText reports whether a server message names an authorization
// failure. Atlas uses code 8000 for other failures too (e.g. an M0 over its
// space quota).
func authorizationText(err error) bool {
	m := strings.ToLower(err.Error())
	for _, k := range []string{"not allowed", "not authorized", "unauthorized", "permission"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

func resolves(host string) bool {
	if host == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	return err == nil && len(addrs) > 0
}
