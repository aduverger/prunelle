package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduverger/prunelle/server/internal/review"
)

// Access policy (docs/decisions.md D2): the server listens on loopback and is exposed through
// Tailscale Serve, which adds X-Forwarded-* headers and, for user identities, Tailscale-User-Login.
// A proxied request must carry a login that is the machine's Tailscale owner or explicitly allowed.
// A direct loopback request without proxy headers comes from a local process, which is trusted as the owner.
// Every request must also name a host that DNS rebinding cannot produce: a browser page that points its own
// domain at 127.0.0.1 sends that domain as Host, and may add proxy headers itself. Direct requests must use
// localhost or a loopback IP; proxied ones an IP or a Tailscale MagicDNS name (*.ts.net).

const loginHeader = "Tailscale-User-Login"

var proxyHeaders = []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "Forwarded", loginHeader}

// OwnerLookup returns the Tailscale login of the machine's owner.
type OwnerLookup func(ctx context.Context) (string, error)

type Authorizer struct {
	allowedLogins map[string]bool
	lookupOwner   OwnerLookup

	mu          sync.Mutex
	owner       string
	lastAttempt time.Time
}

const ownerRetryInterval = time.Minute

func NewAuthorizer(allowedLogins []string, lookupOwner OwnerLookup) *Authorizer {
	allowed := map[string]bool{}
	for _, login := range allowedLogins {
		allowed[login] = true
	}
	return &Authorizer{allowedLogins: allowed, lookupOwner: lookupOwner}
}

// Middleware rejects callers that the access policy does not admit. publicPaths skip the check.
func (a *Authorizer) Middleware(next http.Handler, publicPaths ...string) http.Handler {
	public := map[string]bool{}
	for _, p := range publicPaths {
		public[p] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if public[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		caller, code, message := a.check(r)
		if code != "" {
			writeError(w, statusByCode[code], code, message, nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

// check returns the caller's login, or the error code and message rejecting the request.
func (a *Authorizer) check(r *http.Request) (caller, code, message string) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return "", "PERMISSION_DENIED", "only loopback connections are accepted"
	}
	hostname := hostOnly(r.Host)
	if !proxied(r) {
		if !loopbackHost(hostname) {
			return "", "PERMISSION_DENIED", "direct requests must address the server as localhost or a loopback IP"
		}
		if owner := a.ownerLogin(r.Context()); owner != "" {
			return owner, "", ""
		}
		return localCaller, "", ""
	}
	if !tailnetHost(hostname) {
		return "", "PERMISSION_DENIED", "proxied requests must address the server by its Tailscale name or an IP"
	}
	login := r.Header.Get(loginHeader)
	if login == "" {
		return "", "UNAUTHENTICATED", "proxied request without a Tailscale user identity"
	}
	if a.allowedLogins[login] || login == a.ownerLogin(r.Context()) {
		return login, "", ""
	}
	return "", "PERMISSION_DENIED", fmt.Sprintf("%s is not allowed on this server", login)
}

// localCaller names a local process when the machine's Tailscale owner is unknown.
const localCaller = "local"

type callerKey struct{}

// callerFrom returns the author of the changes a request makes: the human the access check admitted.
func callerFrom(ctx context.Context) review.Author {
	login, _ := ctx.Value(callerKey{}).(string)
	return review.Author{Kind: "HUMAN", ID: login}
}

func proxied(r *http.Request) bool {
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

func hostOnly(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = strings.Trim(hostport, "[]")
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tailnetHost accepts names that cannot be rebound to 127.0.0.1: IP literals and MagicDNS names,
// whose DNS Tailscale controls.
func tailnetHost(host string) bool {
	return net.ParseIP(host) != nil || strings.HasSuffix(host, ".ts.net")
}

// ownerLogin returns the cached owner, retrying a failed or empty lookup at most once a minute.
func (a *Authorizer) ownerLogin(ctx context.Context) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.owner != "" || time.Since(a.lastAttempt) < ownerRetryInterval {
		return a.owner
	}
	a.lastAttempt = time.Now()
	owner, err := a.lookupOwner(ctx)
	if err != nil {
		slog.Warn("cannot determine Tailscale owner; only allowed_logins are admitted", "err", err)
		return ""
	}
	a.owner = owner
	return owner
}

// TailscaleOwner asks the local tailscale CLI which user owns this machine.
// A tagged machine has no owning user; its owner login is empty.
func TailscaleOwner(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json", "--peers=false").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	var status struct {
		Self struct {
			UserID int64
			Tags   []string
		}
		User map[string]struct{ LoginName string }
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", fmt.Errorf("parse tailscale status: %w", err)
	}
	if len(status.Self.Tags) > 0 {
		return "", fmt.Errorf("this machine is tagged and has no owning user")
	}
	user, ok := status.User[strconv.FormatInt(status.Self.UserID, 10)]
	if !ok || user.LoginName == "" {
		return "", fmt.Errorf("tailscale status has no login for user %d", status.Self.UserID)
	}
	return user.LoginName, nil
}
